package main

// The 2026-10 security review's findings, against the real route table:
// #1 rate limits behind a proxy and the admin login's guess allowances,
// #2 the follow key, #3 a refused report and the party's queue, #4 the
// claimed device label.
//
// The guess allowances refill with real time here, so these tests check
// only what refilling cannot change; the exact arithmetic is
// adminauth's, against a frozen clock.

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

const statusPath = "/api/v1/admin/auth/status"

// behindTrustedProxy names app.Test's peer (0.0.0.0) in
// ALEXANDRIA_TRUSTED_PROXIES, so X-Forwarded-For says who the client is.
func behindTrustedProxy(c *config.Config) {
	c.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/32")}
}

// fromClient is the header a proxy adds for the client it forwards.
func fromClient(addr string) map[string]string {
	return map[string]string{"X-Forwarded-For": addr}
}

// visitor is the i-th of many attacking addresses, ten requests each so
// none meets its own 10-a-minute limit.
func visitor(i int) map[string]string {
	return fromClient(fmt.Sprintf("203.0.%d.%d", 113+i/10/250, 1+(i/10)%250))
}

var wrongGuess = map[string]string{"password": "not the password at all", "code": "123456"}

// linkedDevice links a new device to a new person and returns its token's
// header.
func (s *testServer) linkedDevice(person, label string) map[string]string {
	s.t.Helper()
	code, claimSecret := s.linkRequest(label)
	s.expectStatus(http.StatusOK, "POST", approvePath,
		map[string]string{"code": code, "name": person}, bearer(testAPIToken))
	claimed := s.expectStatus(http.StatusOK, "POST", "/api/v1/link/claim",
		map[string]string{"claimSecret": claimSecret}, nil)
	return bearer(claimed.str("token"))
}

// spendSignIns sends wrong guesses from ever more addresses until the
// sign-ins' shared allowance refuses one, and returns that refusal. It
// fails unless that happens after the burst, and no later than the
// guesses that refilling during the run could have added.
func (s *testServer) spendSignIns() response {
	s.t.Helper()
	start := time.Now()
	for i := 0; i < 300; i++ {
		r := s.do("POST", loginPath, wrongGuess, visitor(i))
		if r.status == http.StatusUnauthorized {
			continue
		}
		if r.status != http.StatusTooManyRequests || r.str("reason") != "guess-limit" {
			s.t.Fatalf("wrong guess %d: %d %v, want 401 or 429 guess-limit", i+1, r.status, r)
		}
		refilled := int(time.Since(start)/adminauth.SignInGuessRefill) + 1
		if i < adminauth.SignInGuessBurst || i > adminauth.SignInGuessBurst+refilled {
			s.t.Fatalf("the allowance refused wrong guess %d; want it after %d, at most %d more",
				i+1, adminauth.SignInGuessBurst, refilled)
		}
		return r
	}
	s.t.Fatal("300 wrong guesses from 30 addresses were never refused")
	return response{}
}

// #1. Without ALEXANDRIA_TRUSTED_PROXIES nothing changes: every request
// from one peer spends one budget, whatever X-Forwarded-For claims.
func TestForwardedForFromAnUntrustedPeerBuysNoFreshLimit(t *testing.T) {
	s := newTestServer(t)
	for i := 1; i <= 10; i++ {
		s.expectStatus(http.StatusOK, "GET", statusPath, nil,
			fromClient(fmt.Sprintf("203.0.113.%d", i)))
	}
	r := s.do("GET", statusPath, nil, fromClient("198.51.100.1"))
	if r.status != http.StatusTooManyRequests || string(r.raw) != limitedReply {
		t.Fatalf("11th request with a fresh X-Forwarded-For: %d %v, want the limiter's 429", r.status, r)
	}
}

// #1. Behind a named proxy each client has a budget of its own: one
// visitor spending theirs leaves everyone else's, for signing in and for
// linking.
func TestBehindATrustedProxyEachClientHasItsOwnLimit(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	attacker := fromClient("203.0.113.7")
	for i := 1; i <= 10; i++ {
		s.expectStatus(http.StatusOK, "GET", statusPath, nil, attacker)
	}
	if r := s.do("GET", statusPath, nil, attacker); r.status != http.StatusTooManyRequests {
		t.Fatalf("the attacker's 11th: %d %v, want 429", r.status, r)
	}
	// Writing someone else's address left of their own spends nobody
	// else's budget: the proxy's entry is the last one.
	if r := s.do("GET", statusPath, nil, fromClient("198.51.100.2, 203.0.113.7")); r.status != http.StatusTooManyRequests {
		t.Fatalf("a forged left-hand entry escaped the attacker's limit: %d %v", r.status, r)
	}
	s.expectStatus(http.StatusOK, "GET", statusPath, nil, fromClient("198.51.100.2"))

	for i := 1; i <= 30; i++ {
		if r := s.do("POST", "/api/v1/link/request", nil, attacker); r.status == http.StatusTooManyRequests {
			t.Fatalf("the attacker's link request %d was already limited", i)
		}
	}
	if r := s.do("POST", "/api/v1/link/request", nil, attacker); r.status != http.StatusTooManyRequests {
		t.Fatalf("the attacker's 31st link request: %d %v, want 429", r.status, r)
	}
	if r := s.do("POST", "/api/v1/link/request", nil, fromClient("198.51.100.2")); r.status == http.StatusTooManyRequests {
		t.Fatalf("someone else's link request was limited by the attacker's: %v", r)
	}
}

// #1, the second review's sequence: one stranger's wrong guesses, at the
// most their own limit allows, must not lock the owner out. They meet
// their own limit; the owner signs in from elsewhere.
func TestOneVisitorCannotSpendTheSignInAllowance(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	e := enrollDirect(t)
	stranger := fromClient("203.0.113.7")
	for i := 1; i <= 10; i++ {
		s.expectStatus(http.StatusUnauthorized, "POST", loginPath, wrongGuess, stranger)
	}
	if r := s.do("POST", loginPath, wrongGuess, stranger); r.status != http.StatusTooManyRequests ||
		string(r.raw) != limitedReply {
		t.Fatalf("the stranger's 11th: %d %v, want their own limit's 429", r.status, r)
	}
	s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, fromClient("198.51.100.2"))
}

// #1. Many visitors together meet the sign-ins' shared allowance, so
// more addresses buy no more guesses.
func TestManyVisitorsTogetherMeetTheSignInAllowance(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	enrollDirect(t)
	r := s.spendSignIns()
	if r.header.Get("Retry-After") == "" {
		t.Fatalf("a guess-limit refusal without Retry-After: %v", r)
	}
	// The phone's sign-in draws on the same allowance.
	r = s.do("POST", deviceTokenPath, map[string]any{
		"password": "not the password at all", "code": "123456",
		"deviceLabel": "x", "persistent": true}, fromClient("198.51.100.3"))
	if r.status != http.StatusTooManyRequests || r.str("reason") != "guess-limit" {
		t.Fatalf("a phone's sign-in right after: %d %v, want 429 guess-limit", r.status, r)
	}
}

// #1, the review's other half: strangers who spend the sign-ins'
// allowance cannot stop a phone already signed in from backing up.
func TestStrangersCannotBlockASignedInPhonesStepUps(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	e := enrollDirect(t)
	phone := s.signIn(e, true, nil)
	s.spendSignIns()
	r := s.do("POST", "/api/v1/admin/backup",
		map[string]string{"code": freshCode(t, e.secret)}, bearer(phone.token))
	if r.status != http.StatusOK {
		t.Fatalf("a signed-in phone's backup while strangers guess: %d %v", r.status, r)
	}
}

// #1. A phone's wrong step-up codes spend its own allowance only: from
// as many addresses as it likes, a stolen phone token gets ten, and the
// owner's other phone and sign-in are untouched.
func TestAPhonesWrongStepUpsSpendOnlyItsOwnAllowance(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	e := enrollDirect(t)
	stolen := s.signIn(e, true, nil)
	other := s.signIn(e, true, nil)
	for i := 0; i < adminauth.StepUpGuessBurst; i++ {
		hdr := bearer(stolen.token)
		hdr["X-Forwarded-For"] = fmt.Sprintf("203.0.113.%d", i+1)
		s.expectRefusal("step-up-failed", "POST", "/api/v1/admin/backup",
			map[string]string{"code": wrongCode(t, e.secret)}, hdr)
	}
	hdr := bearer(stolen.token)
	hdr["X-Forwarded-For"] = "203.0.113.200"
	r := s.do("POST", "/api/v1/admin/backup", map[string]string{"code": freshCode(t, e.secret)}, hdr)
	if r.status != http.StatusTooManyRequests || r.str("reason") != "guess-limit" {
		t.Fatalf("the stolen token's next step-up, right code and all: %d %v, want 429 guess-limit", r.status, r)
	}

	if r := s.do("POST", "/api/v1/admin/backup",
		map[string]string{"code": freshCode(t, e.secret)}, bearer(other.token)); r.status != http.StatusOK {
		t.Fatalf("the owner's other phone: %d %v", r.status, r)
	}
	s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, fromClient("198.51.100.2"))
}

// #1. A right answer costs nothing, so signing in again and again never
// runs the allowance down.
func TestARightAnswerSpendsNoGuess(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	e := enrollDirect(t)
	for i := 0; i < adminauth.SignInGuessBurst-1; i++ {
		s.expectStatus(http.StatusUnauthorized, "POST", loginPath, wrongGuess, visitor(i))
	}
	for i := 1; i <= 5; i++ {
		s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
			"password": testPassword, "code": freshCode(t, e.secret)},
			fromClient(fmt.Sprintf("198.51.100.%d", i)))
	}
	// The last guess of the burst is still a 401: five right answers spent
	// none of it.
	s.expectStatus(http.StatusUnauthorized, "POST", loginPath, wrongGuess, fromClient("198.51.100.99"))
}

// #1. New link requests from everyone together are capped too, so many
// addresses cannot grow the list of waiting devices without end. One
// visitor never reaches it; claims, which add nothing, are not counted.
func TestLinkRequestsFromEveryoneTogetherAreCapped(t *testing.T) {
	s := newTestServer(t, behindTrustedProxy)
	for i := 0; i < 120; i++ {
		if r := s.do("POST", "/api/v1/link/request", nil, visitor(i/3)); r.status != http.StatusOK {
			t.Fatalf("link request %d from its own address: %d %v", i+1, r.status, r)
		}
	}
	r := s.do("POST", "/api/v1/link/request", nil, fromClient("198.51.100.5"))
	if r.status != http.StatusTooManyRequests ||
		r.str("error") != "too many devices are asking to link right now; wait a minute" {
		t.Fatalf("the 121st link request this minute: %d %v, want everyone's 429", r.status, r)
	}
	s.expectStatus(http.StatusNotFound, "POST", "/api/v1/link/claim",
		map[string]string{"claimSecret": "not-a-real-secret"}, fromClient("198.51.100.6"))
}

// #2, at the door: a follow key is a short id, not a payload.
func TestFollowRefusesAnOversizedDeviceKey(t *testing.T) {
	s := newTestServer(t)
	tv := s.linkedDevice("Chris", "Apple TV")
	s.expectStatus(http.StatusBadRequest, "POST", "/api/v1/nowplaying/follow",
		map[string]string{"deviceKey": strings.Repeat("k", nowplaying.MaxDeviceKeyLen+1)}, tv)
	s.expectStatus(http.StatusOK, "POST", "/api/v1/nowplaying/follow",
		map[string]string{"deviceKey": strings.Repeat("k", nowplaying.MaxDeviceKeyLen)}, tv)
}

// #3, the reviewer's sequence: a second device of the same person PUTs
// a queue while another device leads, is refused seat-held, and the
// party's queue is still the leader's.
func TestASeatHeldReportCannotReplaceThePartysQueue(t *testing.T) {
	s := newTestServer(t)
	phone := s.linkedDevice("Chris", "iPhone")
	paired := s.expectStatus(http.StatusCreated, "POST", "/api/v1/devices",
		map[string]string{"label": "Apple TV"}, phone)
	tv := bearer(paired.str("token"))

	led := s.expectStatus(http.StatusOK, "PUT", "/api/v1/nowplaying/", map[string]any{
		"itemId": "a", "title": "A", "isPlaying": true, "basedOnRevision": 0,
		"queue": []map[string]string{{"itemId": "a"}, {"itemId": "b"}},
	}, phone)
	state, _ := led.body["state"].(map[string]any)
	refused := s.expectStatus(http.StatusConflict, "PUT", "/api/v1/nowplaying/", map[string]any{
		"itemId": "evil", "title": "Evil", "isPlaying": true, "basedOnRevision": state["revision"],
		"queue": []map[string]string{{"itemId": "evil"}},
	}, tv)
	if refused.str("code") != "seat-held" {
		t.Fatalf("second device: %v, want seat-held", refused)
	}
	q := s.expectStatus(http.StatusOK, "GET", "/api/v1/nowplaying/queue", nil, phone)
	items, _ := q.body["items"].([]any)
	var ids []string
	for _, it := range items {
		m, _ := it.(map[string]any)
		id, _ := m["itemId"].(string)
		ids = append(ids, id)
	}
	if got := strings.Join(ids, ","); got != "a,b" {
		t.Fatalf("the party's queue after a seat-held report: %q, want a,b", got)
	}
}

// #4: /link/claim kept whatever label a device sent; every other way a
// label arrives cuts it at 80.
func TestClaimCapsTheDeviceLabel(t *testing.T) {
	s := newTestServer(t)
	code, claimSecret := s.linkRequest("TV")
	s.expectStatus(http.StatusOK, "POST", approvePath,
		map[string]string{"code": code, "name": "Chris"}, bearer(testAPIToken))
	claimed := s.expectStatus(http.StatusOK, "POST", "/api/v1/link/claim", map[string]string{
		"claimSecret": claimSecret, "deviceLabel": strings.Repeat("x", 100_000)}, nil)
	me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, bearer(claimed.str("token")))
	if n := len(me.str("label")); n != 80 {
		t.Fatalf("the claimed label is %d bytes, want 80", n)
	}
}

// Party audio (2026-10-31), against the real routes: a person's party
// audio is that person's alone, who set it is the token's label, and a
// signed-out device is told so on every party-audio route.
func TestPartyAudioIsThePersonsOwn(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	kateTV := bearer(s.expectStatus(http.StatusCreated, "POST", "/api/v1/devices",
		map[string]string{"label": "Kate's TV"}, kate).str("token"))
	chris := s.linkedDevice("Chris", "Chris's iPhone")

	s.expectStatus(http.StatusOK, "PUT", "/api/v1/nowplaying/", map[string]any{
		"itemId": "a", "title": "A", "isPlaying": true, "basedOnRevision": 0}, kate)
	s.expectStatus(http.StatusOK, "POST", "/api/v1/nowplaying/follow",
		map[string]any{"role": "follower", "features": []string{"volume"}}, kateTV)

	// Chris has no party, and Kate's is not his to reach.
	if r := s.do("POST", "/api/v1/nowplaying/volume", map[string]any{"volume": 5}, chris); r.status != http.StatusNotFound ||
		r.str("code") != "no-session" {
		t.Fatalf("Chris with no party: %d %v", r.status, r)
	}

	r := s.expectStatus(http.StatusOK, "POST", "/api/v1/nowplaying/volume", map[string]any{"volume": 40}, kateTV)
	state, _ := r.body["state"].(map[string]any)
	if state["partyVolume"] != float64(40) || state["partyVolumeByLabel"] != "Kate's TV" {
		t.Fatalf("Kate's TV set the party: %v", r)
	}

	// Chris leading his own party sees none of Kate's.
	s.expectStatus(http.StatusOK, "PUT", "/api/v1/nowplaying/", map[string]any{
		"itemId": "b", "title": "B", "isPlaying": true, "basedOnRevision": 0}, chris)
	mine := s.expectStatus(http.StatusOK, "GET", "/api/v1/nowplaying/", nil, chris)
	if _, has := mine.body["partyVolume"]; has {
		t.Fatalf("Chris sees Kate's party volume: %v", mine)
	}
	s.expectStatus(http.StatusOK, "POST", "/api/v1/nowplaying/volume", map[string]any{"volume": 5}, chris)
	theirs := s.expectStatus(http.StatusOK, "GET", "/api/v1/nowplaying/", nil, kate)
	if theirs.body["partyVolume"] != float64(40) {
		t.Fatalf("Chris's volume reached Kate's party: %v", theirs)
	}

	// Signed out, Kate's TV is told so on every party-audio route.
	me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, kateTV)
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/devices/"+me.str("deviceKey"), nil, bearer(testAPIToken))
	for _, c := range partyAudioRoutes {
		if r := s.do(c.method, c.path, map[string]any{"volume": 40}, kateTV); r.status != http.StatusUnauthorized ||
			r.str("code") != "token-revoked" {
			t.Errorf("%s %s with a revoked token: %d %v, want 401 token-revoked", c.method, c.path, r.status, r)
		}
	}
}

// testClip is a canonical announcement: 16 kHz mono 16-bit PCM, n
// samples, with a 44-byte header.
func testClip(n int) string {
	le := binary.LittleEndian
	b := make([]byte, 44+2*n)
	copy(b, "RIFF")
	le.PutUint32(b[4:], uint32(36+2*n))
	copy(b[8:], "WAVEfmt ")
	le.PutUint32(b[16:], 16)
	le.PutUint16(b[20:], 1)
	le.PutUint16(b[22:], 1)
	le.PutUint32(b[24:], 16000)
	le.PutUint32(b[28:], 32000)
	le.PutUint16(b[32:], 2)
	le.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	le.PutUint32(b[40:], uint32(2*n))
	for i := 44; i < len(b); i++ {
		b[i] = byte(i * 7)
	}
	return string(b)
}

// A clip is its own party's alone: another person's token gets exactly the
// answer a made-up id gets, and can neither fetch nor stop it.
func TestAnnouncementsAreThePartysOwn(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	chris := s.linkedDevice("Chris", "Chris's iPhone")
	for _, d := range []map[string]string{kate, chris} {
		s.expectStatus(http.StatusOK, "PUT", "/api/v1/nowplaying/", map[string]any{
			"itemId": "a", "title": "A", "isPlaying": true, "basedOnRevision": 0}, d)
	}
	clip := testClip(16000)
	up, err := s.sendRaw("POST", "/api/v1/nowplaying/announcements", "audio/wav", clip, kate)
	if err != nil || up.status != http.StatusOK {
		t.Fatalf("Kate's clip: %v %d %v", err, up.status, up)
	}
	ann, _ := up.body["announcement"].(map[string]any)
	id, _ := ann["id"].(string)
	if ann["byLabel"] != "Kate's iPhone" || len(id) != 32 {
		t.Fatalf("announcement: %v", ann)
	}

	got := s.expectStatus(http.StatusOK, "GET", "/api/v1/nowplaying/announcements/"+id, nil, kate)
	if string(got.raw) != clip || got.header.Get("Cache-Control") != "no-store" || got.header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("Kate's own fetch: %d bytes, headers %v", len(got.raw), got.header)
	}
	real := s.do("GET", "/api/v1/nowplaying/announcements/"+id, nil, chris)
	madeUp := s.do("GET", "/api/v1/nowplaying/announcements/"+strings.Repeat("0", 32), nil, chris)
	if real.status != http.StatusNotFound || real.str("code") != "not-found" || string(real.raw) != string(madeUp.raw) {
		t.Fatalf("Chris fetching Kate's clip: %d %v (a made-up id: %v)", real.status, real, madeUp)
	}
	if r := s.do("POST", "/api/v1/nowplaying/announcements/"+id+"/stop", nil, chris); r.status != http.StatusNotFound {
		t.Fatalf("Chris stopping Kate's clip: %d %v", r.status, r)
	}
	if st := s.expectStatus(http.StatusOK, "GET", "/api/v1/nowplaying/", nil, chris); st.body["announcement"] != nil {
		t.Fatalf("Chris's party lists Kate's announcement: %v", st)
	}
}

// Live voice is its own party's alone too: another person's token can
// neither read, feed nor end it, and is told exactly what a made-up id
// gets.
func TestLiveAnnouncementsAreThePartysOwn(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	chris := s.linkedDevice("Chris", "Chris's iPhone")
	for _, d := range []map[string]string{kate, chris} {
		s.expectStatus(http.StatusOK, "PUT", "/api/v1/nowplaying/", map[string]any{
			"itemId": "a", "title": "A", "isPlaying": true, "basedOnRevision": 0}, d)
	}
	start := s.expectStatus(http.StatusOK, "POST", "/api/v1/nowplaying/live", nil, kate)
	ann, _ := start.body["announcement"].(map[string]any)
	id, _ := ann["id"].(string)
	if ann["kind"] != "live" || len(id) != 32 {
		t.Fatalf("Kate's live announcement: %v", start)
	}
	zeros := strings.Repeat("0", 32)
	real := s.do("GET", "/api/v1/nowplaying/live/"+id+"/audio?from=0", nil, chris)
	madeUp := s.do("GET", "/api/v1/nowplaying/live/"+zeros+"/audio?from=0", nil, chris)
	if real.status != http.StatusNotFound || real.str("code") != "not-found" || string(real.raw) != string(madeUp.raw) {
		t.Fatalf("Chris reading Kate's live voice: %d %v (a made-up id: %v)", real.status, real, madeUp)
	}
	if r, _ := s.sendRaw("POST", "/api/v1/nowplaying/live/"+id+"/audio?from=0", "application/octet-stream",
		"\x01\x02", chris); r.status != http.StatusNotFound {
		t.Fatalf("Chris feeding Kate's live voice: %d %v", r.status, r)
	}
	if r := s.do("POST", "/api/v1/nowplaying/live/"+id+"/end", nil, chris); r.status != http.StatusNotFound {
		t.Fatalf("Chris ending Kate's live voice: %d %v", r.status, r)
	}
	// Kate ends it; her own read answers at once, ended.
	s.expectStatus(http.StatusOK, "POST", "/api/v1/nowplaying/live/"+id+"/end", map[string]any{"endSample": 0}, kate)
	frame := s.expectStatus(http.StatusOK, "GET", "/api/v1/nowplaying/live/"+id+"/audio?from=0", nil, kate)
	if len(frame.raw) != 24 || string(frame.raw[:4]) != "PAL1" || frame.raw[20] != 1 {
		t.Fatalf("Kate's read of her ended live voice: % x", frame.raw)
	}
}

// The access log never names an announcement: every path that carries an
// id is skipped, in any case; the clip upload and the live start, which
// carry none, are still logged.
func TestTheAccessLogSkipsAnnouncementIDs(t *testing.T) {
	for path, quiet := range map[string]bool{
		"/api/v1/nowplaying/announcements/0123456789abcdef0123456789abcdef":      true,
		"/api/v1/nowplaying/announcements/0123456789abcdef0123456789abcdef/stop": true,
		"/API/V1/NowPlaying/Announcements/0123456789abcdef0123456789abcdef":      true,
		"/api/v1/nowplaying/live/0123456789abcdef0123456789abcdef/audio":         true,
		"/api/v1/nowplaying/live/0123456789abcdef0123456789abcdef/end":           true,
		"/api/v1/nowplaying/LIVE/0123456789abcdef0123456789abcdef/audio":         true,
		"/api/v1/nowplaying/live":            false,
		"/api/v1/nowplaying/announcements":   false,
		"/api/v1/nowplaying/announce-policy": false,
		"/api/v1/nowplaying/volume":          false,
		"/api/v1/nowplaying/":                false,
		"/api/v1/nowplaying":                 false,
	} {
		if got := quietPath(path); got != quiet {
			t.Errorf("quietPath(%q) = %v, want %v", path, got, quiet)
		}
	}
}
