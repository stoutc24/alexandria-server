package main

// The 2026-11-10 security review's findings, against the real route table:
// one high, five medium, four low. Each test fails with its fix taken out.

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/handlers"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// gzipZeros is n zero bytes, gzipped as small as gzip goes: about a
// thousandth of n.
func gzipZeros(t *testing.T, n int) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 1<<20)
	for left := n; left > 0; left -= len(chunk) {
		if _, err := zw.Write(chunk[:min(left, len(chunk))]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 1 (high). A compressed body is refused however its Content-Encoding is
// split across header lines. The 2026-11-02 check read the first line and
// Fiber unpacks by the last, so "identity" then "gzip" was unpacked with no
// limit and no credential: 49 KB took the process from 38 MB to 216 MB.
func TestACompressedBodyIsRefusedHoweverItsHeadersAreSplit(t *testing.T) {
	s := newTestServer(t)
	bomb := gzipZeros(t, 12<<20)
	if len(bomb) > middleware.SmallBodyLimit {
		t.Fatalf("the bomb is %d bytes: the size limit would refuse it first", len(bomb))
	}
	cases := []struct {
		name  string
		lines map[string][]string
	}{
		{"identity, then gzip", map[string][]string{"Content-Encoding": {"identity", "gzip"}}},
		{"gzip, then identity", map[string][]string{"Content-Encoding": {"gzip", "identity"}}},
		{"three lines, the last brotli", map[string][]string{"Content-Encoding": {"identity", "identity", "br"}}},
		{"an empty line, then deflate", map[string][]string{"Content-Encoding": {"", "deflate"}}},
		{"the name in two spellings", map[string][]string{
			"content-encoding": {"identity"}, "CONTENT-ENCODING": {"gzip"}}},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("POST", "/api/v1/accounts/login", bytes.NewReader(bomb))
		req.Header.Set("Content-Type", "application/json")
		for name, values := range tc.lines {
			// Assigned, not Set: the name goes out as written.
			req.Header[name] = values
		}
		resp, err := s.app.Test(req, -1)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		body := new(bytes.Buffer)
		_, _ = body.ReadFrom(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnsupportedMediaType || !strings.Contains(body.String(), "encoded-body") {
			t.Errorf("%s: %d %s — want 415 encoded-body", tc.name, resp.StatusCode, body)
		}
	}
}

// 2 (medium). The admin cookie counts only for the admin page's own
// requests. SameSite=Strict keeps it off other SITES, and a site ignores
// the port: a page on another port of the same machine, or a sibling
// subdomain, sent it with a form post that approved its own link code.
func TestTheAdminCookieCountsOnlyForTheAdminPagesOwnRequests(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	r := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	c := findCookie(r.resp, "alx_admin")
	if c == nil {
		t.Fatalf("no admin cookie: %v", r)
	}
	withCookie := func(extra map[string]string) map[string]string {
		h := map[string]string{"Cookie": "alx_admin=" + c.Value}
		for k, v := range extra {
			h[k] = v
		}
		return h
	}
	code, _ := s.linkRequest("Someone else's TV")
	approve := map[string]string{"code": code, "name": "Mallory"}

	// Another page: a neighbour on the same site, a stranger, a browser
	// from before Sec-Fetch-Site naming another port or host, a sandbox.
	// httptest sends these to the host example.com.
	foreign := []map[string]string{
		{"Sec-Fetch-Site": "same-site"},
		{"Sec-Fetch-Site": "cross-site"},
		{"Sec-Fetch-Site": "same-site", "Origin": "http://example.com"},
		{"Origin": "http://example.com:8096"},
		{"Origin": "http://nas.example.com"},
		{"Origin": "null"},
	}
	for _, hdr := range foreign {
		r := s.do("POST", approvePath, approve, withCookie(hdr))
		if r.status != http.StatusForbidden || r.str("reason") != "cross-origin" {
			t.Fatalf("approve from %v: %d %v, want 403 cross-origin", hdr, r.status, r)
		}
		// Reads too: the cookie simply does not count.
		r = s.do("GET", "/api/v1/admin/profiles", nil, withCookie(hdr))
		if r.status != http.StatusForbidden || r.str("reason") != "cross-origin" {
			t.Fatalf("list people from %v: %d %v, want 403 cross-origin", hdr, r.status, r)
		}
		// And where any token is taken (the catalogue).
		r = s.do("GET", "/api/v1/catalog/summary", nil, withCookie(hdr))
		if r.status != http.StatusForbidden || r.str("reason") != "cross-origin" {
			t.Fatalf("catalogue from %v: %d %v, want 403 cross-origin", hdr, r.status, r)
		}
	}
	// The form post the review used, as a browser sends one.
	raw, err := s.sendRaw("POST", approvePath, "application/x-www-form-urlencoded",
		"code="+code+"&name=Mallory", withCookie(map[string]string{"Sec-Fetch-Site": "same-site"}))
	if err != nil {
		t.Fatal(err)
	}
	if raw.status != http.StatusForbidden || raw.str("reason") != "cross-origin" {
		t.Fatalf("the form post: %d %v, want 403 cross-origin", raw.status, raw)
	}
	people := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, withCookie(nil))
	if n, _ := people.body["count"].(float64); n != 0 {
		t.Fatalf("a refused approval made a person: %v", people)
	}

	// The admin page itself, the person at the address bar, an older
	// browser on the same origin, and a script that holds the cookie.
	own := []map[string]string{
		{"Sec-Fetch-Site": "same-origin"},
		{"Sec-Fetch-Site": "none"},
		{"Sec-Fetch-Site": "same-origin", "Origin": "http://example.com"},
		{"Origin": "http://example.com"},
		{},
	}
	for _, hdr := range own {
		s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, withCookie(hdr))
		s.expectStatus(http.StatusOK, "GET", "/api/v1/catalog/summary", nil, withCookie(hdr))
	}
	s.expectStatus(http.StatusOK, "POST", approvePath,
		map[string]string{"code": code, "name": "Chris"},
		withCookie(map[string]string{"Sec-Fetch-Site": "same-origin"}))

	// A phone's token is not a cookie: no browser attaches one by itself.
	phone := s.signIn(e, true, nil)
	hdr := bearer(phone.token)
	hdr["Sec-Fetch-Site"] = "cross-site"
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, hdr)
}

// 2, behind a proxy that rewrites Host: an older browser's Origin is
// matched against the configured public address as well.
func TestTheAdminCookieBehindAProxyThatRewritesHost(t *testing.T) {
	s := newTestServer(t, func(c *config.Config) { c.PublicURL = "https://alexandria.home.example.net" })
	e := enrollDirect(t)
	r := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	c := findCookie(r.resp, "alx_admin")
	if c == nil {
		t.Fatalf("no admin cookie: %v", r)
	}
	cookie := func(origin string) map[string]string {
		return map[string]string{"Cookie": "alx_admin=" + c.Value, "Origin": origin}
	}
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil,
		cookie("https://alexandria.home.example.net"))
	if r := s.do("GET", "/api/v1/admin/profiles", nil,
		cookie("https://alexandria.home.example.net:8443")); r.status != http.StatusForbidden {
		t.Fatalf("another port of the public address: %d %v, want 403", r.status, r)
	}
}

// 3 (medium). Room for a large body only for a caller who has shown a
// credential. The server set aside the whole declared length, up to 8 MiB,
// as the headers arrived, on every route: forty idle connections held
// about 290 MB.
func TestRoomForALargeBodyOnlyWithACredential(t *testing.T) {
	s := newTestServer(t)
	// Linked while the environment token still approves.
	device := s.linkedDevice("Chris", "Chris's iPhone")
	e := enrollDirect(t)
	big := `{"username":"` + strings.Repeat("x", 64<<10) + `"}`
	tooLarge := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "body size exceeds") {
			t.Fatalf("%s: %v, want the body refused for its size", what, err)
		}
	}
	_, err := s.sendRaw("POST", "/api/v1/accounts/login", "application/json", big, nil)
	tooLarge("no credential", err)
	_, err = s.sendRaw("POST", "/api/v1/catalog/sync", "application/json", big, bearer("alxp_"+strings.Repeat("0", 64)))
	tooLarge("a made-up device token", err)
	_, err = s.sendRaw("POST", "/api/v1/catalog/sync", "application/json", big, bearer("alxa_"+strings.Repeat("0", 64)))
	tooLarge("a made-up admin sign-in", err)
	_, err = s.sendRaw("POST", "/api/v1/catalog/sync", "application/json", big,
		map[string]string{"Cookie": "alx_admin=" + strings.Repeat("0", 64)})
	tooLarge("a made-up admin cookie", err)

	// Within the small limit, anyone's body is still read: the sign-in
	// answers for itself.
	small := `{"username":"` + strings.Repeat("x", 8<<10) + `","password":"not it"}`
	r, err := s.sendRaw("POST", "/api/v1/accounts/login", "application/json", small, nil)
	if err != nil || r.status != http.StatusUnauthorized {
		t.Fatalf("an 8 KB sign-in with no credential: %d %v (%v), want the sign-in's own 401", r.status, r, err)
	}

	// Somebody: a linked device, a signed-in phone, the admin's cookie.
	phone := s.signIn(e, true, nil)
	login := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	cookie := findCookie(login.resp, "alx_admin")
	if cookie == nil {
		t.Fatalf("no admin cookie: %v", login)
	}
	sync := `{"source":"abs","items":[],"padding":"` + strings.Repeat("x", 1<<20) + `"}`
	for what, hdr := range map[string]map[string]string{
		"a linked device":   device,
		"a signed-in phone": bearer(phone.token),
		"the admin cookie":  {"Cookie": "alx_admin=" + cookie.Value},
	} {
		r, err := s.sendRaw("POST", "/api/v1/catalog/sync", "application/json", sync, hdr)
		if err != nil {
			t.Fatalf("%s sending 1 MiB: %v, want it read", what, err)
		}
		if r.status == http.StatusRequestEntityTooLarge {
			t.Fatalf("%s sending 1 MiB: 413", what)
		}
	}
}

// 4 (medium). An IPv6 visitor is counted by its /64: counted address by
// address, one home's /64 had a fresh limit for every request.
func TestAnIPv6VisitorIsCountedByIts64(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	enrollDirect(t)
	for i := 0; i < 10; i++ {
		r := s.do("POST", loginPath, wrongGuess, fromClient(fmt.Sprintf("2001:db8:1:2::%x", i+1)))
		if r.status != http.StatusUnauthorized {
			t.Fatalf("wrong sign-in %d from one /64: %d %v, want 401", i+1, r.status, r)
		}
	}
	r := s.do("POST", loginPath, wrongGuess, fromClient("2001:db8:1:2:ffff:ffff:ffff:ffff"))
	if r.status != http.StatusTooManyRequests || string(r.raw) != limitedReply {
		t.Fatalf("an eleventh address of the same /64: %d %v, want the visitor's own limit", r.status, r)
	}
	// The next /64 is another visitor; so is any IPv4 address.
	for _, addr := range []string{"2001:db8:1:3::1", "198.51.100.7"} {
		if r := s.do("POST", loginPath, wrongGuess, fromClient(addr)); r.status != http.StatusUnauthorized {
			t.Fatalf("a first wrong sign-in from %s: %d %v, want 401", addr, r.status, r)
		}
	}
}

// 5 (medium). Two-step's switches and the password change check the
// password with the sign-in's defences. They skipped the pause, so a copied
// device token could guess the password there without end, and the right
// guess turned two-step off.
func TestTwoStepsSwitchesPauseLikeTheSignIn(t *testing.T) {
	s := newTestServer(t, withSecretsKey, behindTrustedProxy)
	h := setUpHousehold(t, s)
	from := func(i int) map[string]string {
		hdr := bearer(h.samPhone)
		hdr["X-Forwarded-For"] = fmt.Sprintf("203.0.113.%d", i+1)
		return hdr
	}
	for i := 0; i < repository.LoginFailuresBeforeLock; i++ {
		s.refused(http.StatusUnauthorized, "bad-credentials", "POST", "/api/v1/accounts/totp/disable",
			map[string]any{"password": fmt.Sprintf("guess number %d", i)}, from(i))
	}
	// Paused: the right password is not looked at, at any of the three.
	s.refused(http.StatusTooManyRequests, "locked", "POST", "/api/v1/accounts/totp/disable",
		map[string]any{"password": samPassword}, from(20))
	s.refused(http.StatusTooManyRequests, "locked", "POST", "/api/v1/accounts/password",
		map[string]any{"currentPassword": samPassword, "newPassword": "another long enough one"}, from(21))
	var enabled int
	if err := database.DB.QueryRow(`SELECT totp_enabled FROM account_logins WHERE profile_id = ?`,
		h.sam).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 {
		t.Fatal("two-step was switched off while the sign-in was paused")
	}

	// Switching it on is checked the same way (Dana has none yet).
	dana := s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "dana", "password": samPassword, "device": newTestDevice(t).body("Dana's tablet"),
	}, fromClient("203.0.113.40")).str("deviceToken")
	if dana == "" {
		t.Fatal("Dana could not sign in")
	}
	setup := s.do("POST", "/api/v1/accounts/totp/setup", nil, bearer(dana))
	fromDana := func(i int) map[string]string {
		hdr := bearer(dana)
		hdr["X-Forwarded-For"] = fmt.Sprintf("203.0.113.%d", 50+i)
		return hdr
	}
	for i := 0; i < repository.LoginFailuresBeforeLock; i++ {
		s.refused(http.StatusUnauthorized, "bad-credentials", "POST", "/api/v1/accounts/totp/enable",
			map[string]any{"password": fmt.Sprintf("guess number %d", i), "code": "000000"}, fromDana(i))
	}
	s.refused(http.StatusTooManyRequests, "locked", "POST", "/api/v1/accounts/totp/enable", map[string]any{
		"password": samPassword, "code": codeFor(t, setup.str("secret"), currentStep())}, fromDana(30))
}

// 5. Those checks draw on the allowance every visitor shares, as the
// sign-in does: once it is spent, nothing slow runs for anyone.
func TestTwoStepsSwitchesDrawOnTheSharedAllowance(t *testing.T) {
	s := newTestServer(t, withSecretsKey, behindTrustedProxy)
	h := setUpHousehold(t, s)
	spent := false
	for i := 0; i < 300 && !spent; i++ {
		r := s.do("POST", "/api/v1/accounts/login", map[string]any{
			"username": fmt.Sprintf("nobody%d", i), "password": "not it",
			"device": map[string]any{"label": "x"}}, visitor(i))
		spent = r.status == http.StatusTooManyRequests && r.str("reason") == "busy"
	}
	if !spent {
		t.Fatal("300 wrong sign-ins never spent the shared allowance")
	}
	hdr := bearer(h.samPhone)
	hdr["X-Forwarded-For"] = "198.51.100.9"
	s.refused(http.StatusTooManyRequests, "busy", "POST", "/api/v1/accounts/totp/disable",
		map[string]any{"password": samPassword}, hdr)
}

// 5. A name nobody has pauses exactly as a real one does. A real sign-in
// pauses after ten wrong answers and says so; a made-up name used to
// answer 401 for ever, which told the two apart.
func TestAMadeUpUsernamePausesLikeARealOne(t *testing.T) {
	s := newTestServer(t, withSecretsKey, behindTrustedProxy)
	setUpHousehold(t, s)
	answers := func(username string, from int) []string {
		var out []string
		for i := 0; i < repository.LoginFailuresBeforeLock+3; i++ {
			r := s.do("POST", "/api/v1/accounts/login", map[string]any{
				"username": username, "password": "not the password",
				"device": map[string]any{"label": "x"},
			}, fromClient(fmt.Sprintf("203.0.%d.%d", from, i+1)))
			_, timed := r.body["retryAfterSeconds"]
			out = append(out, fmt.Sprintf("%d %s retryAfter=%v", r.status, r.str("reason"), timed))
		}
		return out
	}
	realName, madeUp := answers("sam", 113), answers("Nobody.Here", 114)
	if !reflect.DeepEqual(realName, madeUp) {
		t.Fatalf("a real name and a made-up one answer differently:\n real    %v\n made up %v", realName, madeUp)
	}
	if got := realName[repository.LoginFailuresBeforeLock]; got != "429 locked retryAfter=true" {
		t.Fatalf("answer %d is %q, want the pause", repository.LoginFailuresBeforeLock+1, got)
	}
	// The pause is on the name as the database compares it.
	r := s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "  nobody.here ", "password": "x", "device": map[string]any{"label": "x"},
	}, fromClient("203.0.115.1"))
	if r.status != http.StatusTooManyRequests || r.str("reason") != "locked" {
		t.Fatalf("the same made-up name in another case: %d %v, want 429 locked", r.status, r)
	}
}

// 8 (low). Signing ONE of a person's devices out ends the Manage server
// sign-in that device made with their account. The phone's link went and
// its admin sign-in stayed.
func TestSigningADeviceOutEndsTheManageServerSignInItMade(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	h := setUpHousehold(t, s)
	e := enrollDirect(t)
	owner := bearer(s.signIn(e, true, nil).token)
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("full"), owner)
	r := s.samSignsIn(h, samPassword)
	if r.status != http.StatusCreated {
		t.Fatalf("Sam's Manage server sign-in: %d %v", r.status, r)
	}
	sams := bearer(r.str("token"))
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, sams)

	me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, bearer(h.samPhone))
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/devices/"+me.str("deviceKey"), nil, owner)

	s.refused(http.StatusUnauthorized, "device-token-invalid", "GET", "/api/v1/admin/profiles", nil, sams)
	// The owner's own phone is no one's device: untouched.
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, owner)
}

// 8 (low). "Sign out all devices" also drops a link approved for that
// person but not yet collected: that device walked in a moment later.
func TestSignOutAllDevicesAlsoDropsALinkNotYetCollected(t *testing.T) {
	s := newTestServer(t)
	env := bearer(testAPIToken)
	chris := s.linkedDevice("Chris", "Chris's iPhone")
	people := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, env)
	id := people.body["profiles"].([]any)[0].(map[string]any)["id"].(string)

	code, claimSecret := s.linkRequest("A TV about to collect")
	s.expectStatus(http.StatusOK, "POST", approvePath, map[string]string{"code": code, "profileId": id}, env)
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/profiles/"+id+"/devices", nil, env)

	r := s.do("POST", "/api/v1/link/claim", map[string]string{"claimSecret": claimSecret}, nil)
	if r.status == http.StatusOK || r.str("token") != "" {
		t.Fatalf("the approved link was collected after sign out all: %d %v", r.status, r)
	}
	if r := s.do("GET", "/api/v1/devices/me", nil, chris); r.status != http.StatusUnauthorized {
		t.Fatalf("Chris's phone after sign out all: %d %v, want 401", r.status, r)
	}
}

// 9 (low). A right step-up code forgives the wrong ones before it: only
// ten in a row sign a phone out (the ten-in-a-row half is
// TestAPhonesWrongStepUpsAreItsOwnAndTenInARowSignItOut).
func TestARightStepUpCodeForgivesTheWrongOnes(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	e := enrollDirect(t)
	phone := s.signIn(e, true, nil)
	// Each request from another address: the sign-in rate limit counts a
	// client's requests, and this is about the phone's codes.
	sent := 0
	from := func() map[string]string {
		sent++
		hdr := bearer(phone.token)
		hdr["X-Forwarded-For"] = fmt.Sprintf("203.0.113.%d", sent)
		return hdr
	}
	wrong := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			s.expectRefusal("step-up-failed", "POST", "/api/v1/admin/backup",
				map[string]string{"code": wrongCode(t, e.secret)}, from())
		}
	}
	half := handlers.StepUpWrongBeforeSignOut / 2
	wrong(half)
	s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/backup",
		map[string]string{"code": freshCode(t, e.secret)}, from())
	wrong(half)
	// Ten wrong in all, never ten in a row: still signed in.
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, from())
	if !deviceRowExists(t, phone.id()) {
		t.Fatal("the phone was signed out without ten wrong codes in a row")
	}
}

// 7 (low). An invite's short code is stored under a key the database does
// not hold. It was a plain SHA-256 of 8 characters: whoever held a copy of
// the database could try every code in under two hours and become the
// invited person.
func TestAnInviteCodeIsStoredUnderAKeyTheDatabaseDoesNotHold(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	t.Cleanup(func() { repository.SetInviteCodeKey(testAPIToken) })
	_, invite := setUpSam(t, s)
	code, token := invite["code"].(string), invite["token"].(string)

	var stored string
	if err := database.DB.QueryRow(
		`SELECT code_hash FROM account_invites WHERE used_at IS NULL`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	plain := sha256.Sum256([]byte(repository.NormalizeInviteCode(code)))
	if stored == hex.EncodeToString(plain[:]) {
		t.Fatal("the code is stored as its plain SHA-256: a copy of the database gives it away")
	}
	check := func(invite string) response {
		return s.do("POST", "/api/v1/accounts/invite/check", map[string]string{"invite": invite}, nil)
	}
	if r := check(code); r.status != http.StatusOK || r.str("profileName") != "Sam Rivera" {
		t.Fatalf("the code no longer opens its invite: %d %v", r.status, r)
	}
	// A code is never matched as a token, nor a token's hash as a code.
	if r := check(stored); r.status != http.StatusNotFound {
		t.Fatalf("the stored value opened the invite: %d %v", r.status, r)
	}

	// The same rows under another server's key: the code says nothing,
	// and the link, which needs no key, still works.
	repository.SetInviteCodeKey("some other server's admin token")
	if r := check(code); r.status != http.StatusNotFound || r.str("reason") != "invite-invalid" {
		t.Fatalf("the code under another key: %d %v, want 404 invite-invalid", r.status, r)
	}
	if r := check(token); r.status != http.StatusOK {
		t.Fatalf("the invite link's token: %d %v", r.status, r)
	}
}

// 7 (low). A phone's copy of the database carries no invite still waiting;
// the browser's full backup keeps it.
func TestAPhonesBackupCarriesNoWaitingInvite(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	setUpSam(t, s)
	e := enrollDirect(t)
	phone := s.signIn(e, true, nil)
	waiting := func(data []byte) int {
		t.Helper()
		var n int
		if err := openBackup(t, data).QueryRow(
			`SELECT COUNT(*) FROM account_invites WHERE used_at IS NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	r := s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/backup",
		map[string]string{"code": freshCode(t, e.secret)}, bearer(phone.token))
	if n := waiting(r.raw); n != 0 {
		t.Fatalf("the phone's copy holds %d waiting invite(s)", n)
	}
	login := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	c := findCookie(login.resp, "alx_admin")
	full := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/backup", nil,
		map[string]string{"Cookie": "alx_admin=" + c.Value})
	if n := waiting(full.raw); n != 1 {
		t.Fatalf("the full backup holds %d waiting invite(s), want 1", n)
	}
}

// The listen address: IPv4 as before, IPv6 when an IPv6 address is named.
func TestListenNetworkFollowsTheListenAddress(t *testing.T) {
	for addr, want := range map[string]string{
		"": "tcp4", "127.0.0.1": "tcp4", "192.168.1.9": "tcp4", "::1": "tcp6", "fd00::9": "tcp6",
	} {
		if got := listenNetwork(addr); got != want {
			t.Errorf("listenNetwork(%q) = %q, want %q", addr, got, want)
		}
	}
}
