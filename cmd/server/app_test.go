package main

// HTTP tests against the real route table: buildApp is the same wiring
// main serves, so a route registered in the wrong group, a limiter on
// the wrong path or a middleware in the wrong order fails here first.
//
// No t.Parallel anywhere in this file: database.DB is a package global,
// and every test points it at its own temporary database.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// testAPIToken is the environment token, 16+ characters as config
// demands.
const testAPIToken = "test-env-token-0123456789abcdef"

type testServer struct {
	t   *testing.T
	app *fiber.App
	cfg *config.Config
}

// newTestServer connects a fresh temporary database and builds a fresh
// app — a fresh app also means a fresh rate limiter.
func newTestServer(t *testing.T, mutate ...func(*config.Config)) *testServer {
	t.Helper()
	cfg := &config.Config{
		Port:       "0",
		DBPath:     filepath.Join(t.TempDir(), "app_test.db"),
		APIToken:   testAPIToken,
		ServerName: "test-companion",
	}
	for _, m := range mutate {
		m(cfg)
	}
	if err := database.Connect(cfg.DBPath); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.RunMigrations(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return &testServer{t: t, app: buildApp(cfg), cfg: cfg}
}

// response is one decoded reply.
type response struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
	resp   *http.Response
}

func (r response) str(key string) string {
	s, _ := r.body[key].(string)
	return s
}

// String is what a failure message shows of a reply: quoted and cut
// short, so a binary backup never floods (or leaks into) the test log.
func (r response) String() string {
	const max = 200
	if len(r.raw) > max {
		return fmt.Sprintf("%q… (%d bytes)", r.raw[:max], len(r.raw))
	}
	return fmt.Sprintf("%q", r.raw)
}

// send issues one request and decodes the reply. A non-nil body is
// sent as JSON; hdr adds headers ("Authorization", "Cookie", proxy
// headers…). Safe from any goroutine: it reports errors instead of
// failing the test.
func (s *testServer) send(method, path string, body any, hdr map[string]string) (response, error) {
	var raw string
	ctype := ""
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return response{}, err
		}
		raw, ctype = string(buf), "application/json"
	}
	return s.sendRaw(method, path, ctype, raw, hdr)
}

func (s *testServer) sendRaw(method, path, ctype, raw string, hdr map[string]string) (response, error) {
	var rd io.Reader
	if raw != "" {
		rd = strings.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, rd)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.app.Test(req, -1)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}
	out := response{status: resp.StatusCode, header: resp.Header, raw: data, resp: resp}
	_ = json.Unmarshal(data, &out.body)
	return out, nil
}

// do is send for the test goroutine: any transport error fails the test.
func (s *testServer) do(method, path string, body any, hdr map[string]string) response {
	s.t.Helper()
	r, err := s.send(method, path, body, hdr)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// routeTable is every registered route as "METHOD /path", sorted. Fiber
// adds a HEAD for every GET.
func routeTable(app *fiber.App) []string {
	var out []string
	for _, r := range app.GetRoutes(true) {
		out = append(out, r.Method+" "+r.Path)
	}
	sort.Strings(out)
	return out
}

// wantRoutes is the whole route table, sorted.
var wantRoutes = []string{
	"DELETE /api/v1/admin/admin-devices",
	"DELETE /api/v1/admin/admin-devices/:id",
	"DELETE /api/v1/admin/devices/:tokenId",
	"DELETE /api/v1/admin/profiles/:id",
	"DELETE /api/v1/admin/profiles/:id/account",
	"DELETE /api/v1/admin/profiles/:id/invite",
	"DELETE /api/v1/admin/profiles/:id/services/:sid",
	"DELETE /api/v1/admin/service-admins/:id",
	"DELETE /api/v1/nowplaying/",
	"DELETE /api/v1/stats/",
	"GET /admin",
	"GET /api/v1/accounts/me",
	"GET /api/v1/accounts/services",
	"GET /api/v1/admin/admin-devices",
	"GET /api/v1/admin/auth/status",
	"GET /api/v1/admin/backup",
	"GET /api/v1/admin/catalog/duplicates",
	"GET /api/v1/admin/catalog/summary",
	"GET /api/v1/admin/links",
	"GET /api/v1/admin/profiles",
	"GET /api/v1/admin/profiles/:id/account",
	"GET /api/v1/admin/profiles/:id/services",
	"GET /api/v1/admin/service-admins",
	"GET /api/v1/catalog/duplicates",
	"GET /api/v1/catalog/items",
	"GET /api/v1/catalog/summary",
	"GET /api/v1/devices/me",
	"GET /api/v1/nowplaying/",
	"GET /api/v1/nowplaying/queue",
	"GET /api/v1/stats/summary",
	"GET /api/v1/time",
	"GET /health",
	"GET /invite",
	"HEAD /admin",
	"HEAD /api/v1/accounts/me",
	"HEAD /api/v1/accounts/services",
	"HEAD /api/v1/admin/admin-devices",
	"HEAD /api/v1/admin/auth/status",
	"HEAD /api/v1/admin/backup",
	"HEAD /api/v1/admin/catalog/duplicates",
	"HEAD /api/v1/admin/catalog/summary",
	"HEAD /api/v1/admin/links",
	"HEAD /api/v1/admin/profiles",
	"HEAD /api/v1/admin/profiles/:id/account",
	"HEAD /api/v1/admin/profiles/:id/services",
	"HEAD /api/v1/admin/service-admins",
	"HEAD /api/v1/catalog/duplicates",
	"HEAD /api/v1/catalog/items",
	"HEAD /api/v1/catalog/summary",
	"HEAD /api/v1/devices/me",
	"HEAD /api/v1/nowplaying/",
	"HEAD /api/v1/nowplaying/queue",
	"HEAD /api/v1/stats/summary",
	"HEAD /api/v1/time",
	"HEAD /health",
	"HEAD /invite",
	"PATCH /api/v1/admin/profiles/:id/services/:sid",
	"PATCH /api/v1/devices/me",
	"POST /api/v1/accounts/invite/accept",
	"POST /api/v1/accounts/invite/check",
	"POST /api/v1/accounts/login",
	"POST /api/v1/accounts/password",
	"POST /api/v1/accounts/totp/disable",
	"POST /api/v1/accounts/totp/enable",
	"POST /api/v1/accounts/totp/setup",
	"POST /api/v1/admin/auth/device-token",
	"POST /api/v1/admin/auth/enroll",
	"POST /api/v1/admin/auth/login",
	"POST /api/v1/admin/auth/logout",
	"POST /api/v1/admin/backup",
	"POST /api/v1/admin/links/approve",
	"POST /api/v1/admin/profiles",
	"POST /api/v1/admin/profiles/:id/invite",
	"POST /api/v1/admin/profiles/:id/services",
	"POST /api/v1/admin/service-admins",
	"POST /api/v1/admin/service-admins/open",
	"POST /api/v1/catalog/sync",
	"POST /api/v1/devices",
	"POST /api/v1/link/claim",
	"POST /api/v1/link/request",
	"POST /api/v1/nowplaying/follow",
	"POST /api/v1/nowplaying/handover",
	"POST /api/v1/nowplaying/intent",
	"POST /api/v1/stats/events",
	"PUT /api/v1/accounts/device-key",
	"PUT /api/v1/admin/profiles/:id/services/order",
	"PUT /api/v1/nowplaying/",
}

// TestRouteTable pins the whole route table. A route that appears,
// disappears or moves fails here, and the diff says which.
func TestRouteTable(t *testing.T) {
	s := newTestServer(t)
	got := routeTable(s.app)

	want := map[string]bool{}
	for _, r := range wantRoutes {
		want[r] = true
	}
	have := map[string]bool{}
	for _, r := range got {
		have[r] = true
		if !want[r] {
			t.Errorf("unexpected route: %s", r)
		}
	}
	for _, r := range wantRoutes {
		if !have[r] {
			t.Errorf("missing route: %s", r)
		}
	}
	if len(got) != len(wantRoutes) {
		t.Errorf("got %d routes, want %d:\n%s",
			len(got), len(wantRoutes), strings.Join(got, "\n"))
	}
}

// --- admin login helpers -------------------------------------------

const (
	testPassword = "correct horse battery staple"
	// totpPeriod is RFC 6238's 30 seconds, which every authenticator
	// app assumes.
	totpPeriod = 30
	// wrongPasswordOrCode is Login's one refusal, byte for byte.
	wrongPasswordOrCode = `{"error":"wrong password or code"}`
)

type enrolment struct {
	secret   string
	recovery []string
}

// enrollDirect sets up the admin login through the repository, exactly
// as the enrol handler does, without spending a request from the auth
// limiter's budget of 10.
func enrollDirect(t *testing.T) enrolment {
	t.Helper()
	hash, err := adminauth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := adminauth.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	codes, err := adminauth.NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, 0, len(codes))
	for _, c := range codes {
		hashes = append(hashes,
			adminauth.HashToken(adminauth.NormalizeRecoveryCode(c)))
	}
	if err := repository.NewAdminAuthRepository().Enroll(hash, secret, hashes); err != nil {
		t.Fatal(err)
	}
	return enrolment{secret: secret, recovery: codes}
}

// enrollHTTP enrols through the real route with the environment token.
// It spends one request of the auth limiter's 10.
func (s *testServer) enrollHTTP() enrolment {
	s.t.Helper()
	r := s.do("POST", "/api/v1/admin/auth/enroll",
		map[string]string{"password": testPassword}, bearer(testAPIToken))
	if r.status != http.StatusOK {
		s.t.Fatalf("enroll: %d %v", r.status, r)
	}
	e := enrolment{secret: r.str("secret")}
	list, _ := r.body["recoveryCodes"].([]any)
	for _, c := range list {
		e.recovery = append(e.recovery, c.(string))
	}
	if e.secret == "" || len(e.recovery) != adminauth.RecoveryCodeCount {
		s.t.Fatalf("enroll returned %v", r)
	}
	return e
}

func currentStep() int64 { return time.Now().Unix() / totpPeriod }

func codeFor(t *testing.T, secret string, step int64) string {
	t.Helper()
	code, err := adminauth.CodeAt(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// freshCode clears the spent-step record and returns the code for the
// current step, so a test can sign in again without waiting 30 s. If
// the server's clock ticks into the next step meanwhile, Verify's one
// step of skew still accepts this code.
func freshCode(t *testing.T, secret string) string {
	t.Helper()
	if _, err := database.DB.Exec(
		`UPDATE admin_credentials SET last_totp_step = 0`); err != nil {
		t.Fatal(err)
	}
	return codeFor(t, secret, currentStep())
}

// wrongCode is six digits that match no step Verify could accept now.
func wrongCode(t *testing.T, secret string) string {
	t.Helper()
	near := map[string]bool{}
	for d := int64(-2); d <= 2; d++ {
		near[codeFor(t, secret, currentStep()+d)] = true
	}
	for _, c := range []string{"000000", "111111", "123456", "999999"} {
		if !near[c] {
			return c
		}
	}
	t.Fatal("no wrong code found")
	return ""
}

func lastTOTPStep(t *testing.T) int64 {
	t.Helper()
	var step int64
	if err := database.DB.QueryRow(`
		SELECT COALESCE(last_totp_step, 0) FROM admin_credentials WHERE id = 1`).
		Scan(&step); err != nil {
		t.Fatal(err)
	}
	return step
}

func recoveryRemaining(t *testing.T) int {
	t.Helper()
	n, err := repository.NewAdminAuthRepository().RecoveryCodesRemaining()
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func findCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

const loginPath = "/api/v1/admin/auth/login"

// --- S2: Login, characterised before the TOTP change ----------------
//
// These pin what the browser sees today, so extracting the shared
// credential check (and the atomic step claim) cannot quietly change
// it.

func TestLoginSetsTheSessionCookieAndBody(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)

	before := time.Now()
	r := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	after := time.Now()
	if r.status != http.StatusOK {
		t.Fatalf("login: %d %v", r.status, r)
	}
	want := map[string]any{
		"ok": true, "usedRecoveryCode": false,
		"recoveryCodesRemaining": float64(adminauth.RecoveryCodeCount),
	}
	if !reflect.DeepEqual(r.body, want) {
		t.Fatalf("body %v, want exactly %v", r, want)
	}

	c := findCookie(r.resp, "alx_admin")
	if c == nil {
		t.Fatalf("no alx_admin cookie; headers %v", r.header)
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("cookie SameSite = %v, want Strict", c.SameSite)
	}
	if c.Path != "/" {
		t.Errorf("cookie path %q, want /", c.Path)
	}
	if c.Secure {
		t.Error("cookie is Secure on plain http; the LAN login would silently fail")
	}
	lo := before.Add(repository.SessionLifetime).Add(-time.Minute)
	hi := after.Add(repository.SessionLifetime).Add(time.Minute)
	if c.Expires.Before(lo) || c.Expires.After(hi) {
		t.Errorf("cookie expires %v, want about now+12h", c.Expires)
	}
	if len(c.Value) != 64 {
		t.Errorf("cookie value is %d characters, want 64 hex", len(c.Value))
	}
}

func TestLoginCookieIsSecureBehindAnHTTPSProxy(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	r := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)},
		map[string]string{"X-Forwarded-Proto": "https"})
	if r.status != http.StatusOK {
		t.Fatalf("login: %d %v", r.status, r)
	}
	if c := findCookie(r.resp, "alx_admin"); c == nil || !c.Secure {
		t.Fatalf("cookie behind an https proxy is not Secure: %+v", c)
	}
}

// A wrong password must spend neither second factor: checking them
// against an unproven password would let anyone burn the recovery
// codes, or spend the current step and lock the owner out of it.
func TestLoginWrongPasswordSpendsNoSecondFactor(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)

	code := freshCode(t, e.secret)
	r := s.do("POST", loginPath, map[string]string{
		"password": "not the password at all", "code": code}, nil)
	if r.status != http.StatusUnauthorized || string(r.raw) != wrongPasswordOrCode {
		t.Fatalf("wrong password: %d %v", r.status, r)
	}
	if step := lastTOTPStep(t); step != 0 {
		t.Fatalf("a wrong password spent TOTP step %d", step)
	}
	if r := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": code}, nil); r.status != http.StatusOK {
		t.Fatalf("the same code with the right password: %d %v", r.status, r)
	}

	r = s.do("POST", loginPath, map[string]string{
		"password": "not the password at all", "recoveryCode": e.recovery[0]}, nil)
	if r.status != http.StatusUnauthorized || string(r.raw) != wrongPasswordOrCode {
		t.Fatalf("wrong password with a recovery code: %d %v", r.status, r)
	}
	if n := recoveryRemaining(t); n != adminauth.RecoveryCodeCount {
		t.Fatalf("a wrong password burned a recovery code: %d left", n)
	}
	r = s.do("POST", loginPath, map[string]string{
		"password": testPassword, "recoveryCode": e.recovery[0]}, nil)
	if r.status != http.StatusOK || r.body["usedRecoveryCode"] != true ||
		r.body["recoveryCodesRemaining"] != float64(adminauth.RecoveryCodeCount-1) {
		t.Fatalf("recovery login: %d %v", r.status, r)
	}
}

// Every refusal reads the same, so an attacker never learns which half
// of the credential was right.
func TestLoginEveryFailureIsTheSame401(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)

	refuse := func(what string, body map[string]string) {
		t.Helper()
		r := s.do("POST", loginPath, body, nil)
		if r.status != http.StatusUnauthorized || string(r.raw) != wrongPasswordOrCode {
			t.Errorf("%s: %d %v, want 401 %s", what, r.status, r, wrongPasswordOrCode)
		}
	}
	admit := func(what string, body map[string]string) {
		t.Helper()
		if r := s.do("POST", loginPath, body, nil); r.status != http.StatusOK {
			t.Fatalf("%v: %d %s", what, r.status, r)
		}
	}

	refuse("wrong password", map[string]string{
		"password": "not the password at all", "code": freshCode(t, e.secret)})
	refuse("wrong code", map[string]string{
		"password": testPassword, "code": wrongCode(t, e.secret)})
	refuse("five digits", map[string]string{"password": testPassword, "code": "12345"})
	refuse("no second factor", map[string]string{"password": testPassword})

	code := freshCode(t, e.secret)
	admit("first use", map[string]string{"password": testPassword, "code": code})
	refuse("replayed code", map[string]string{"password": testPassword, "code": code})

	admit("recovery code", map[string]string{
		"password": testPassword, "recoveryCode": e.recovery[1]})
	refuse("recovery code reused", map[string]string{
		"password": testPassword, "recoveryCode": e.recovery[1]})
	refuse("unknown recovery code", map[string]string{
		"password": testPassword, "recoveryCode": "AAAAA-BBBBB"})
}

func TestLoginBeforeEnrolmentAndWithABadBody(t *testing.T) {
	s := newTestServer(t)
	r := s.do("POST", loginPath, map[string]string{
		"password": testPassword, "code": "123456"}, nil)
	if r.status != http.StatusUnauthorized ||
		string(r.raw) != `{"error":"no admin login is set up on this server"}` {
		t.Fatalf("not enrolled: %d %v", r.status, r)
	}

	r, err := s.sendRaw("POST", loginPath, "application/json", "{not json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.status != http.StatusBadRequest || string(r.raw) != `{"error":"invalid request body"}` {
		t.Fatalf("bad body: %d %v", r.status, r)
	}
}

// concurrently fires n identical requests at once and returns their
// replies.
func (s *testServer) concurrently(n int, method, path string, body any, hdr map[string]string) []response {
	s.t.Helper()
	out := make([]response, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			out[i], errs[i] = s.send(method, path, body, hdr)
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			s.t.Fatal(err)
		}
	}
	return out
}

// S2, the live security bug. Login read last_totp_step, ran the slow
// password hash, then wrote the step unconditionally — so every request
// carrying one code inside that window got in (8 of 8 in the security
// review's scratch run). Exactly one may win.
func TestConcurrentLoginsWithOneCodeAdmitExactlyOne(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	code := freshCode(t, e.secret)

	replies := s.concurrently(8, "POST", loginPath,
		map[string]string{"password": testPassword, "code": code}, nil)
	won := 0
	for _, r := range replies {
		switch {
		case r.status == http.StatusOK:
			won++
		case r.status == http.StatusUnauthorized && string(r.raw) == wrongPasswordOrCode:
		default:
			t.Errorf("unexpected reply: %d %v", r.status, r)
		}
	}
	if won != 1 {
		t.Fatalf("%d of 8 concurrent logins with one code succeeded; want exactly 1", won)
	}
}

// --- the phone's admin sign-in (R1) and what its token opens -------

const (
	deviceTokenPath = "/api/v1/admin/auth/device-token"
	// limitedReply is the auth limiter's 429, byte for byte.
	limitedReply = `{"error":"too many sign-in attempts; wait a minute"}`
	day          = 24 * time.Hour
)

var (
	deviceTokenRe = regexp.MustCompile(`^alxa_[0-9a-f]{64}$`)
	wholeSecondRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)
	backupNameRe  = regexp.MustCompile(`^attachment; filename="alexandria-\d{4}-\d{2}-\d{2}-\d{6}\.db"$`)
)

// deviceKeys is the ADMIN_DEVICE object's whole key set in R1; R3 adds
// "current".
var deviceKeys = []string{"createdAt", "createdVia", "expiresAt", "id",
	"label", "lastUsedAt", "persistent", "schemeBasis"}

// signedIn is one phone's admin sign-in, as R1 returned it.
type signedIn struct {
	token  string
	device map[string]any
	reply  response
}

func (d signedIn) id() string { return d.device["id"].(string) }

// signIn calls R1 with the password and a fresh code. It spends one
// request of the auth limiter's 10.
func (s *testServer) signIn(e enrolment, persistent bool, hdr map[string]string) signedIn {
	s.t.Helper()
	r := s.do("POST", deviceTokenPath, map[string]any{
		"password":    testPassword,
		"code":        freshCode(s.t, e.secret),
		"deviceLabel": "Chris · iPhone",
		"persistent":  persistent,
	}, hdr)
	if r.status != http.StatusCreated {
		s.t.Fatalf("device sign-in: %d %v", r.status, r)
	}
	dev, _ := r.body["device"].(map[string]any)
	return signedIn{token: r.str("token"), device: dev, reply: r}
}

func keysOf(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseTime(t *testing.T, v any) time.Time {
	t.Helper()
	s, _ := v.(string)
	if !wholeSecondRe.MatchString(s) {
		t.Fatalf("time %q is not RFC 3339 UTC to the second", s)
	}
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func near(got, want time.Time) bool {
	d := got.Sub(want)
	return d > -2*time.Minute && d < 2*time.Minute
}

func (s *testServer) expectStatus(want int, method, path string, body any, hdr map[string]string) response {
	s.t.Helper()
	r := s.do(method, path, body, hdr)
	if r.status != want {
		s.t.Fatalf("%s %s: %d %v, want %d", method, path, r.status, r, want)
	}
	return r
}

// expectRefusal asserts a 401 with exactly this reason ("" for none).
func (s *testServer) expectRefusal(reason, method, path string, body any, hdr map[string]string) response {
	s.t.Helper()
	r := s.do(method, path, body, hdr)
	got, _ := r.body["reason"].(string)
	if r.status != http.StatusUnauthorized || got != reason {
		s.t.Fatalf("%s %s: %d %v, want 401 reason %q", method, path, r.status, r, reason)
	}
	return r
}

func deviceRowExists(t *testing.T, id string) bool {
	t.Helper()
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM admin_device_tokens WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func deviceTimes(t *testing.T, id string) (lastUsed, expires int64) {
	t.Helper()
	if err := database.DB.QueryRow(`
		SELECT last_used_at, expires_at FROM admin_device_tokens WHERE id = ?`, id).
		Scan(&lastUsed, &expires); err != nil {
		t.Fatal(err)
	}
	return lastUsed, expires
}

func setDeviceTimes(t *testing.T, id string, lastUsed, expires int64) {
	t.Helper()
	if _, err := database.DB.Exec(`
		UPDATE admin_device_tokens SET last_used_at = ?, expires_at = ? WHERE id = ?`,
		lastUsed, expires, id); err != nil {
		t.Fatal(err)
	}
}

// 1. Before enrolment nothing changes: the environment token opens
// /admin, and an alxa_ token is just a wrong token.
func TestBeforeEnrolmentTheEnvironmentTokenOpensAdmin(t *testing.T) {
	s := newTestServer(t)
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, bearer(testAPIToken))
	s.expectRefusal("", "GET", "/api/v1/admin/profiles", nil, bearer("alxa_garbage"))
	s.expectRefusal("", "GET", "/api/v1/admin/profiles", nil, nil)
}

// 2. Regression guard: after enrolment the environment token is demoted
// to enrolment only.
func TestAfterEnrolmentTheEnvironmentTokenIsRefused(t *testing.T) {
	s := newTestServer(t)
	s.enrollHTTP()
	r := s.expectRefusal("", "GET", "/api/v1/admin/profiles", nil, bearer(testAPIToken))
	if r.str("error") != "admin session required — sign in at /admin" {
		t.Fatalf("refusal changed: %v", r)
	}
	s.expectRefusal("", "GET", "/api/v1/admin/admin-devices", nil, bearer(testAPIToken))
}

// 3. R1 issues a token that opens every admin route, and sets no cookie.
func TestDeviceSignInIssuesATokenThatOpensAdmin(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	before := time.Now()
	d := s.signIn(e, true, nil)

	if c := d.reply.header.Values("Set-Cookie"); len(c) != 0 {
		t.Fatalf("R1 set a cookie: %v", c)
	}
	if !deviceTokenRe.MatchString(d.token) {
		t.Fatalf("token %q is not alxa_ + 64 hex", d.token)
	}
	if got := keysOf(d.device); !reflect.DeepEqual(got, deviceKeys) {
		t.Fatalf("device keys %v, want exactly %v", got, deviceKeys)
	}
	if d.device["label"] != "Chris · iPhone" || d.device["createdVia"] != "totp" ||
		d.device["persistent"] != true {
		t.Fatalf("device %v", d.device)
	}
	if created := parseTime(t, d.device["createdAt"]); !near(created, before) {
		t.Fatalf("createdAt %v", created)
	}
	parseTime(t, d.device["lastUsedAt"])
	if d.reply.body["usedRecoveryCode"] != false ||
		d.reply.body["recoveryCodesRemaining"] != float64(adminauth.RecoveryCodeCount) {
		t.Fatalf("reply %v", d.reply)
	}

	auth := bearer(d.token)
	for _, path := range []string{
		"/api/v1/admin/links",
		"/api/v1/admin/profiles",
		"/api/v1/admin/catalog/summary",
		"/api/v1/admin/catalog/duplicates?limit=5",
		"/api/v1/admin/admin-devices",
	} {
		s.expectStatus(http.StatusOK, "GET", path, nil, auth)
	}
	sum := s.do("GET", "/api/v1/admin/catalog/summary", nil, auth)
	for _, k := range []string{"rawItems", "distinctWorks", "duplicateCopies", "ungroupable", "bySource", "byMediaType"} {
		if _, ok := sum.body[k]; !ok {
			t.Fatalf("summary mirror lacks %q: %v", k, sum)
		}
	}

	// The household routes (R12) take it too.
	created := s.expectStatus(http.StatusCreated, "POST", "/api/v1/admin/profiles",
		map[string]string{"name": "Sarah"}, auth)
	pid := created.body["profile"].(map[string]any)["id"].(string)
	s.expectStatus(http.StatusConflict, "POST", "/api/v1/admin/profiles",
		map[string]string{"name": "Sarah"}, auth)
	s.expectStatus(http.StatusNotFound, "DELETE", "/api/v1/admin/devices/no-such-device", nil, auth)
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/profiles/"+pid, nil, auth)
}

// 4. The token is an ADMIN credential only: stats, the party line, the
// clock, device provisioning, the catalog's own routes and enrolment
// all refuse it. A profile token, for its part, opens no admin route.
func TestDeviceTokenOpensNothingButAdmin(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	d := s.signIn(e, true, nil)
	auth := bearer(d.token)

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/stats/summary"},
		{"POST", "/api/v1/stats/events"},
		{"DELETE", "/api/v1/stats/"},
		{"GET", "/api/v1/nowplaying/"},
		{"PUT", "/api/v1/nowplaying/"},
		{"GET", "/api/v1/time"},
		{"POST", "/api/v1/devices"},
		{"PATCH", "/api/v1/devices/me"},
		{"GET", "/api/v1/catalog/summary"},
		{"GET", "/api/v1/catalog/items"},
		{"GET", "/api/v1/catalog/duplicates"},
		{"POST", "/api/v1/catalog/sync"},
		{"POST", "/api/v1/admin/auth/enroll"},
	} {
		r := s.do(c.method, c.path, map[string]any{"password": testPassword}, auth)
		if r.status != http.StatusUnauthorized {
			t.Errorf("%v %s with an admin device token: %d %s", c.method, c.path, r.status, r)
		}
	}

	profiles := repository.NewProfileRepository()
	p, err := profiles.Create("Sarah")
	if err != nil {
		t.Fatal(err)
	}
	ptok, err := profiles.MintToken(p.ID, "Sarah's phone")
	if err != nil {
		t.Fatal(err)
	}
	s.expectRefusal("", "GET", "/api/v1/admin/profiles", nil, bearer(ptok))
	s.expectRefusal("", "GET", "/api/v1/admin/admin-devices", nil, bearer(ptok))
	// …while its own routes still work, so the refusals above are about
	// the token kind, not a broken server.
	s.expectStatus(http.StatusOK, "GET", "/api/v1/stats/summary", nil, bearer(ptok))
}

// 5. A wrong password spends nothing, and R1's refusal carries a reason
// — proof requireAdmin is not in front of it.
func TestDeviceSignInWrongPasswordSpendsNothing(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	code := freshCode(t, e.secret)
	r := s.expectRefusal("bad-credentials", "POST", deviceTokenPath, map[string]any{
		"password": "not the password at all", "code": code}, nil)
	if r.str("error") != "wrong password or code" {
		t.Fatalf("refusal %v", r)
	}
	if strings.Contains(string(r.raw), "admin session required") {
		t.Fatal("requireAdmin answered R1")
	}
	if step := lastTOTPStep(t); step != 0 {
		t.Fatalf("a wrong password spent TOTP step %d", step)
	}
	s.expectStatus(http.StatusCreated, "POST", deviceTokenPath, map[string]any{
		"password": testPassword, "code": code}, nil)
}

// 6a. A code works once.
func TestDeviceSignInCodeCannotBeReplayed(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	code := freshCode(t, e.secret)
	body := map[string]any{"password": testPassword, "code": code}
	s.expectStatus(http.StatusCreated, "POST", deviceTokenPath, body, nil)
	s.expectRefusal("bad-credentials", "POST", deviceTokenPath, body, nil)
	// …nor at the web sign-in.
	r := s.do("POST", loginPath, body, nil)
	if r.status != http.StatusUnauthorized || string(r.raw) != wrongPasswordOrCode {
		t.Fatalf("the R1 code then worked at /login: %d %v", r.status, r)
	}
}

// 6b. Eight phones racing one code: exactly one token.
func TestConcurrentDeviceSignInsWithOneCodeIssueExactlyOne(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	code := freshCode(t, e.secret)
	replies := s.concurrently(8, "POST", deviceTokenPath,
		map[string]any{"password": testPassword, "code": code, "persistent": true}, nil)
	won := 0
	for _, r := range replies {
		switch {
		case r.status == http.StatusCreated:
			won++
		case r.status == http.StatusUnauthorized && r.str("reason") == "bad-credentials":
		default:
			t.Errorf("unexpected reply: %d %v", r.status, r)
		}
	}
	if won != 1 {
		t.Fatalf("%d of 8 concurrent sign-ins with one code got a token; want exactly 1", won)
	}
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM admin_device_tokens`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("%d admin device tokens stored, want 1", n)
	}
}

// 6c. The web sign-in and the phone's racing one code: one winner
// across both routes.
func TestOneCodeRacedAcrossLoginAndDeviceSignInWinsOnce(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	code := freshCode(t, e.secret)
	body := map[string]any{"password": testPassword, "code": code}

	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		path := loginPath
		if i%2 == 1 {
			path = deviceTokenPath
		}
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			<-start
			r, err := s.send("POST", path, body, nil)
			if err != nil {
				return
			}
			if r.status == http.StatusOK || r.status == http.StatusCreated {
				mu.Lock()
				won++
				mu.Unlock()
			}
		}(path)
	}
	close(start)
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d of 8 requests across /login and R1 won with one code; want 1", won)
	}
}

// 7. A recovery code signs a phone in once, and says so.
func TestDeviceSignInWithARecoveryCode(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	body := map[string]any{"password": testPassword, "recoveryCode": strings.ToLower(e.recovery[3])}
	r := s.expectStatus(http.StatusCreated, "POST", deviceTokenPath, body, nil)
	if r.body["usedRecoveryCode"] != true ||
		r.body["recoveryCodesRemaining"] != float64(adminauth.RecoveryCodeCount-1) {
		t.Fatalf("reply %v", r)
	}
	if dev := r.body["device"].(map[string]any); dev["createdVia"] != "recovery" {
		t.Fatalf("createdVia %v", dev["createdVia"])
	}
	s.expectRefusal("bad-credentials", "POST", deviceTokenPath, body, nil)

	// A non-empty recovery code wins over a code, as in Login: a valid
	// code does not rescue a spent recovery code.
	s.expectRefusal("bad-credentials", "POST", deviceTokenPath, map[string]any{
		"password": testPassword, "code": freshCode(t, e.secret),
		"recoveryCode": e.recovery[3]}, nil)
}

// 8. Not enrolled, and a bad body.
func TestDeviceSignInNotEnrolledAndBadBody(t *testing.T) {
	s := newTestServer(t)
	r := s.expectRefusal("not-enrolled", "POST", deviceTokenPath,
		map[string]any{"password": testPassword, "code": "123456"}, nil)
	if r.str("error") != "no admin login is set up on this server" {
		t.Fatalf("refusal %v", r)
	}
	// The environment token changes nothing: it can never become a
	// phone's credential.
	s.expectRefusal("not-enrolled", "POST", deviceTokenPath,
		map[string]any{"password": testPassword, "code": "123456"}, bearer(testAPIToken))

	enrollDirect(t)
	for _, raw := range []string{"{not json", `{"persistent":"yes"}`} {
		r, err := s.sendRaw("POST", deviceTokenPath, "application/json", raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.status != http.StatusBadRequest || r.str("reason") != "bad-request" ||
			r.str("error") != "invalid request body" {
			t.Fatalf("body %q: %d %v", raw, r.status, r)
		}
	}
}

// 9. A token cannot mint a token: R1 never reads Authorization.
func TestATokenCannotMintAnotherToken(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	d := s.signIn(e, true, nil)
	s.expectRefusal("bad-credentials", "POST", deviceTokenPath,
		map[string]any{"deviceLabel": "minted", "persistent": true}, bearer(d.token))
	s.expectRefusal("bad-credentials", "POST", deviceTokenPath,
		map[string]any{"code": freshCode(t, e.secret)}, bearer(d.token))
}

// 10. A sign-in that does not ask to stay is a 12-hour session, and
// records no HTTPS basis when nothing showed one.
func TestSessionOnlySignInLastsTwelveHours(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	d := s.signIn(e, false, nil)
	if d.device["persistent"] != false || d.device["schemeBasis"] != "" {
		t.Fatalf("device %v", d.device)
	}
	if exp := parseTime(t, d.device["expiresAt"]); !near(exp, time.Now().Add(12*time.Hour)) {
		t.Fatalf("expiresAt %v, want about now + 12 h", exp)
	}
	// Evidence of HTTPS is still recorded when the phone did not ask.
	d = s.signIn(e, false, map[string]string{"X-Forwarded-Proto": "https"})
	if d.device["persistent"] != false || d.device["schemeBasis"] != "proxy" {
		t.Fatalf("device behind a proxy %v", d.device)
	}
}

// A1: the phone decides persistence, and the server records how HTTPS
// was established — one sign-in per basis.
func TestPersistentSignInIsGrantedForEachSchemeBasis(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	for _, c := range []struct {
		hdr  map[string]string
		want string
	}{
		{nil, "client"},
		{map[string]string{"X-Forwarded-Proto": "https"}, "proxy"},
		{map[string]string{"X-Forwarded-Protocol": "https"}, "proxy"},
		{map[string]string{"X-Forwarded-Ssl": "on"}, "proxy"},
		{map[string]string{"X-Url-Scheme": "https"}, "proxy"},
		{map[string]string{"Forwarded": `for=203.0.113.9;proto=https;host="alexandria.example.com"`}, "forwarded"},
		{map[string]string{"CF-Visitor": `{"scheme":"https"}`}, "cloudflare"},
		// A proxy that says http does not stop the phone's request, but
		// it is what gets recorded: plain HTTP, not the phone's word.
		{map[string]string{"X-Forwarded-Proto": "http"}, ""},
	} {
		d := s.signIn(e, true, c.hdr)
		if d.device["persistent"] != true || d.device["schemeBasis"] != c.want {
			t.Errorf("headers %v: device %v, want persistent with basis %q", c.hdr, d.device, c.want)
			continue
		}
		if exp := parseTime(t, d.device["expiresAt"]); !near(exp, time.Now().Add(180*day)) {
			t.Errorf("headers %v: expiresAt %v, want about now + 180 days", c.hdr, exp)
		}
	}
}

func TestPersistentSignInRecordsTheOperatorsHTTPSDeclaration(t *testing.T) {
	s := newTestServer(t, func(c *config.Config) {
		c.PublicURL = "https://alexandria.example.com"
	})
	e := enrollDirect(t)
	if d := s.signIn(e, true, nil); d.device["schemeBasis"] != "config" {
		t.Fatalf("device %v, want basis config", d.device)
	}
	// Direct evidence still outranks the declaration.
	if d := s.signIn(e, true, map[string]string{"X-Forwarded-Proto": "https"}); d.device["schemeBasis"] != "proxy" {
		t.Fatalf("device %v, want basis proxy", d.device)
	}
	// The declaration vouches only for a sign-in that asked to stay —
	// which the phone does only over https. One that did not is the
	// phone on a plain http:// address: no basis, whatever the operator
	// declared.
	if d := s.signIn(e, false, nil); d.device["schemeBasis"] != "" {
		t.Fatalf("session-only sign-in %v, want no basis", d.device)
	}
	// A header that says http outranks the declaration, whether or not
	// the sign-in asked to stay.
	for _, persistent := range []bool{true, false} {
		d := s.signIn(e, persistent, map[string]string{
			"X-Forwarded-Proto": "http", "CF-Visitor": `{"scheme":"http"}`})
		if d.device["schemeBasis"] != "" || d.device["persistent"] != persistent {
			t.Fatalf("persistent=%v behind headers saying http: %v, want no basis",
				persistent, d.device)
		}
	}
	// An http:// public URL declares nothing. Config no longer keeps one
	// (decision D-B); handed one anyway, the handler does not take it
	// for a declaration.
	s2 := newTestServer(t, func(c *config.Config) { c.PublicURL = "http://192.168.1.50:4000" })
	e2 := enrollDirect(t)
	if d := s2.signIn(e2, true, nil); d.device["schemeBasis"] != "client" {
		t.Fatalf("device %v, want basis client", d.device)
	}
}

// What the headers say about the client's own connection, with the
// operator's declaration set, so a declaration that wrongly won would
// show as "config".
func TestSchemeBasisTakesTheHeadersWordOverEveryDeclaration(t *testing.T) {
	s := newTestServer(t, func(c *config.Config) {
		c.PublicURL = "https://alexandria.example.com"
	})
	e := enrollDirect(t)
	for _, c := range []struct {
		name string
		hdr  map[string]string
		want string
	}{
		{"RFC 7239 saying http",
			map[string]string{"Forwarded": "for=203.0.113.9;proto=http"}, ""},
		{"X-Forwarded-Ssl: off",
			map[string]string{"X-Forwarded-Ssl": "off"}, ""},
		{"Cloudflare saying http",
			map[string]string{"CF-Visitor": `{"scheme":"http"}`}, ""},
		// A Cloudflare Tunnel into Nginx Proxy Manager: cloudflared's hop
		// to the proxy is plain HTTP, and the proxy says so, while the
		// visitor came in over HTTPS.
		{"Cloudflare https, an inner hop http",
			map[string]string{"CF-Visitor": `{"scheme":"https"}`, "X-Forwarded-Proto": "http"},
			"cloudflare"},
		// Cloudflare reaching an HTTPS origin for a visitor who came in
		// over plain HTTP: the edge saw the visitor, the proxy did not.
		{"Cloudflare http, an inner hop https",
			map[string]string{"CF-Visitor": `{"scheme":"http"}`, "X-Forwarded-Proto": "https"},
			""},
		// Unreadable headers say nothing, so the declaration stands.
		{"garbage headers",
			map[string]string{"Forwarded": ";;=,", "CF-Visitor": "{not json"}, "config"},
	} {
		d := s.signIn(e, true, c.hdr)
		if d.device["schemeBasis"] != c.want || d.device["persistent"] != true {
			t.Errorf("%s: device %v, want persistent with basis %q", c.name, d.device, c.want)
		}
	}
}

// selfSignedTLS is a certificate for 127.0.0.1 and a pool that trusts
// it, so a test can speak real TLS to the app.
func selfSignedTLS(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// A1, basis "tls": the app serving TLS itself, over a real listener —
// an in-memory test request cannot carry a TLS connection.
func TestPersistentSignInOverDirectTLS(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	cert, pool := selfSignedTLS(t)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.app.Listener(ln) }()
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs: pool, MinVersion: tls.VersionTLS12}},
	}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		_ = s.app.Shutdown()
		<-served
	})

	buf, _ := json.Marshal(map[string]any{
		"password": testPassword, "code": freshCode(t, e.secret), "persistent": true})
	resp, err := client.Post("https://"+ln.Addr().String()+deviceTokenPath,
		"application/json", bytes.NewReader(buf))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Token  string         `json:"token"`
		Device map[string]any `json:"device"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || body.Device["schemeBasis"] != "tls" ||
		body.Device["persistent"] != true {
		t.Fatalf("over TLS: %d %v", resp.StatusCode, body.Device)
	}
}

// 11. Revoking one signs that phone out at once and leaves the others;
// revoke-all ends them all; a missing id never means "all".
func TestRevokeOneAndAllAdminDevices(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	a := s.signIn(e, true, nil)
	b := s.signIn(e, true, nil)
	c := s.signIn(e, false, nil)

	// A phone signing itself out.
	r := s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/admin-devices/"+a.id(), nil, bearer(a.token))
	if string(r.raw) != `{"revoked":true}` {
		t.Fatalf("revoke: %v", r)
	}
	if deviceRowExists(t, a.id()) {
		t.Fatal("the revoked row is still there")
	}
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/links", nil, bearer(a.token))
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, bearer(b.token))

	r = s.expectStatus(http.StatusNotFound, "DELETE", "/api/v1/admin/admin-devices/"+a.id(), nil, bearer(b.token))
	if string(r.raw) != `{"error":"admin device not found"}` {
		t.Fatalf("revoke unknown: %v", r)
	}
	// Revoke with its id missing is "not found", never "all".
	s.expectStatus(http.StatusNotFound, "DELETE", "/api/v1/admin/admin-devices/", nil, bearer(b.token))
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, bearer(c.token))

	r = s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/admin-devices", nil, bearer(b.token))
	if string(r.raw) != `{"revoked":2}` {
		t.Fatalf("revoke all: %v", r)
	}
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/links", nil, bearer(b.token))
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/links", nil, bearer(c.token))
}

// 12. Re-enrolment, the break-glass reset, signs out every phone.
func TestReEnrolmentSignsOutEveryPhone(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	a := s.signIn(e, true, nil)
	b := s.signIn(e, false, nil)
	s.enrollHTTP()
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/profiles", nil, bearer(a.token))
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/profiles", nil, bearer(b.token))
}

// 13. An expired sign-in is refused with its own reason, and its row
// deleted: past expires_at, or (A1) 180 days unused.
func TestExpiredSignInsAreRefusedAndDeleted(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	now := time.Now().Unix()

	past := s.signIn(e, true, nil)
	setDeviceTimes(t, past.id(), now, 0)
	r := s.expectRefusal("device-token-expired", "GET", "/api/v1/admin/links", nil, bearer(past.token))
	if r.str("error") != "this phone's sign-in expired" {
		t.Fatalf("refusal %v", r)
	}
	if deviceRowExists(t, past.id()) {
		t.Fatal("the expired row was not deleted")
	}
	// Gone, so the next attempt is "not signed in", not "expired".
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/links", nil, bearer(past.token))

	idle := s.signIn(e, true, nil)
	setDeviceTimes(t, idle.id(), now-int64((181*day).Seconds()), now+int64(day.Seconds()))
	s.expectRefusal("device-token-expired", "GET", "/api/v1/admin/links", nil, bearer(idle.token))
	if deviceRowExists(t, idle.id()) {
		t.Fatal("the idle row was not deleted")
	}

	session := s.signIn(e, false, nil)
	setDeviceTimes(t, session.id(), now-60, now-1)
	s.expectRefusal("device-token-expired", "GET", "/api/v1/admin/links", nil, bearer(session.token))
}

// A1: every use renews a persistent sign-in; a session-only one is
// never renewed.
func TestUseSlidesAPersistentSignInsExpiry(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	now := time.Now().Unix()

	d := s.signIn(e, true, nil)
	setDeviceTimes(t, d.id(), now-int64((179*day).Seconds()), now+int64(day.Seconds()))
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, bearer(d.token))
	last, exp := deviceTimes(t, d.id())
	if !near(time.Unix(last, 0), time.Now()) || !near(time.Unix(exp, 0), time.Now().Add(180*day)) {
		t.Fatalf("after a use: last %v, expires %v", time.Unix(last, 0), time.Unix(exp, 0))
	}

	sess := s.signIn(e, false, nil)
	_, fixed := deviceTimes(t, sess.id())
	setDeviceTimes(t, sess.id(), now-2*3600, fixed)
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, bearer(sess.token))
	last, exp = deviceTimes(t, sess.id())
	if exp != fixed || !near(time.Unix(last, 0), time.Now()) {
		t.Fatalf("session-only after a use: last %v, expires %d (was %d)",
			time.Unix(last, 0), exp, fixed)
	}
}

// 14. The list marks the caller's own sign-in, skips expired ones and
// never carries a secret.
func TestAdminDeviceListMarksCurrentAndCarriesNoSecret(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	a := s.signIn(e, true, nil)
	b := s.signIn(e, false, nil)
	gone := s.signIn(e, false, nil)
	setDeviceTimes(t, gone.id(), time.Now().Unix()-60, 1)

	r := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/admin-devices", nil, bearer(a.token))
	devices, _ := r.body["devices"].([]any)
	if r.body["count"] != float64(2) || len(devices) != 2 {
		t.Fatalf("list %v, want a and b only", r)
	}
	wantKeys := append(append([]string{}, deviceKeys...), "current")
	sort.Strings(wantKeys)
	seen := map[string]bool{}
	for _, raw := range devices {
		dev := raw.(map[string]any)
		if got := keysOf(dev); !reflect.DeepEqual(got, wantKeys) {
			t.Fatalf("listed keys %v, want exactly %v", got, wantKeys)
		}
		id := dev["id"].(string)
		seen[id] = true
		if want := id == a.id(); dev["current"] != want {
			t.Fatalf("device %s current=%v, want %v", id, dev["current"], want)
		}
	}
	if !seen[a.id()] || !seen[b.id()] {
		t.Fatalf("list %v", r)
	}
	for _, secret := range []string{a.token, b.token,
		adminauth.HashToken(a.token), adminauth.HashToken(b.token)} {
		if bytes.Contains(r.raw, []byte(secret)) {
			t.Fatal("the list carries a token or its hash")
		}
	}

	// The browser is never "current".
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	cookie := findCookie(login.resp, "alx_admin")
	r = s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/admin-devices", nil,
		map[string]string{"Cookie": "alx_admin=" + cookie.Value})
	for _, raw := range r.body["devices"].([]any) {
		if raw.(map[string]any)["current"] != false {
			t.Fatalf("a cookie caller saw current=true: %v", r)
		}
	}
}

// 15a. R1, the web sign-in, status and the phone's backup step-up share
// one budget of 10 a minute.
func TestSignInRoutesShareOneRateLimit(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	d := s.signIn(e, true, nil) // 1
	wrong := map[string]any{"password": "not the password at all", "code": "123456"}
	for i, c := range []struct{ method, path string }{
		{"POST", loginPath},                  // 2
		{"GET", "/api/v1/admin/auth/status"}, // 3
		{"POST", deviceTokenPath},            // 4
		{"POST", loginPath},                  // 5
		{"GET", "/api/v1/admin/auth/status"}, // 6
		{"POST", deviceTokenPath},            // 7
		{"GET", "/api/v1/admin/auth/status"}, // 8
		{"POST", loginPath},                  // 9
		{"GET", "/api/v1/admin/auth/status"}, // 10
	} {
		var body any
		if c.method == "POST" {
			body = wrong
		}
		if r := s.do(c.method, c.path, body, nil); r.status == http.StatusTooManyRequests {
			t.Fatalf("request %d (%s %s) was already limited", i+2, c.method, c.path)
		}
	}
	for _, c := range []struct {
		method, path string
		hdr          map[string]string
	}{
		{"POST", deviceTokenPath, nil},
		{"POST", loginPath, nil},
		{"GET", "/api/v1/admin/auth/status", nil},
		{"POST", "/api/v1/admin/backup", bearer(d.token)},
	} {
		r := s.do(c.method, c.path, map[string]any{"code": "123456"}, c.hdr)
		if r.status != http.StatusTooManyRequests || string(r.raw) != limitedReply {
			t.Errorf("11th+ %v %s: %d %s, want the limiter's 429", c.method, c.path, r.status, r)
		}
	}
	// The rest of /admin is not behind it.
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/admin-devices", nil, bearer(d.token))
}

// 15c. Every approval from a phone carries a fresh code (test 17c), so
// a phone's approvals share the sign-in budget: a stolen phone token
// cannot grind codes there either. The browser's approvals carry no
// code and stay unlimited.
func TestPhoneApprovalsShareTheSignInRateLimit(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	phone := bearer(s.signIn(e, true, nil).token) // 1
	guess := map[string]string{"code": "ZZZZZZ", "name": "Sarah", "totpCode": "123456"}
	for i := 2; i <= 10; i++ {
		s.expectStatus(http.StatusNotFound, "POST", approvePath, guess, phone)
	}
	for _, c := range []struct {
		path string
		hdr  map[string]string
	}{
		{approvePath, phone},
		{loginPath, nil},
	} {
		r := s.do("POST", c.path, guess, c.hdr)
		if r.status != http.StatusTooManyRequests || string(r.raw) != limitedReply {
			t.Errorf("11th+ POST %s: %d %s, want the limiter's 429", c.path, r.status, r)
		}
	}

	// The browser, on a fresh app and limiter: one sign-in, then as many
	// approvals as it likes.
	s = newTestServer(t)
	e = enrollDirect(t)
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	cookie := map[string]string{"Cookie": "alx_admin=" + findCookie(login.resp, "alx_admin").Value}
	for i := 0; i < 15; i++ {
		s.expectStatus(http.StatusNotFound, "POST", approvePath,
			map[string]string{"code": "ZZZZZZ", "name": "Sarah"}, cookie)
	}
}

// 15b. The admin routes a phone polls are never rate limited.
func TestAdminRoutesThePhonePollsAreNotRateLimited(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	d := s.signIn(e, true, nil)
	for i := 0; i < 25; i++ {
		s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/admin-devices", nil, bearer(d.token))
		s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, bearer(d.token))
	}
}

// openBackup writes a backup to disk and opens it.
func openBackup(t *testing.T, data []byte) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup.db")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// 16. Backups: the browser's GET is unchanged; a phone must take the
// POST, with a fresh code, and gets a copy with no way back in.
func TestBackupFromAPhoneNeedsAFreshCodeAndLeavesSecretsOut(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	if _, err := repository.NewProfileRepository().Create("Sarah"); err != nil {
		t.Fatal(err)
	}

	// The browser: today's whole snapshot, admin login included.
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	cookie := map[string]string{"Cookie": "alx_admin=" + findCookie(login.resp, "alx_admin").Value}
	full := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/backup", nil, cookie)
	if db := openBackup(t, full.raw); countRows(t, db, "admin_credentials") != 1 {
		t.Fatal("the browser's backup lost the admin login")
	}

	d := s.signIn(e, true, nil)
	phone := bearer(d.token)

	r := s.expectStatus(http.StatusForbidden, "GET", "/api/v1/admin/backup", nil, phone)
	if r.str("reason") != "step-up-required" ||
		r.str("error") != "backing up from a phone needs a fresh code" {
		t.Fatalf("GET backup with a phone token: %v", r)
	}

	// The code the phone signed in with is spent: the step-up refuses
	// it, and the phone stays signed in.
	signInCode := codeFor(t, e.secret, lastTOTPStep(t))
	r = s.expectRefusal("step-up-failed", "POST", "/api/v1/admin/backup",
		map[string]string{"code": signInCode}, phone)
	if r.str("error") != "wrong code" {
		t.Fatalf("step-up refusal %v", r)
	}
	s.expectRefusal("step-up-failed", "POST", "/api/v1/admin/backup",
		map[string]string{"code": wrongCode(t, e.secret)}, phone)
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, phone)

	r = s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/backup",
		map[string]string{"code": freshCode(t, e.secret)}, phone)
	if ct := r.header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Fatalf("Content-Type %q", ct)
	}
	if cd := r.header.Get("Content-Disposition"); !backupNameRe.MatchString(cd) {
		t.Fatalf("Content-Disposition %q", cd)
	}
	if r.resp.ContentLength != int64(len(r.raw)) {
		t.Fatalf("Content-Length %d, body %d bytes", r.resp.ContentLength, len(r.raw))
	}
	if !bytes.HasPrefix(r.raw, []byte("SQLite format 3\x00")) {
		t.Fatal("the backup is not a SQLite file")
	}
	db := openBackup(t, r.raw)
	for _, table := range []string{"admin_credentials", "admin_recovery_codes",
		"admin_sessions", "admin_device_tokens", "service_admin_logins"} {
		if n := countRows(t, db, table); n != 0 {
			t.Errorf("the phone's backup kept %d rows of %s", n, table)
		}
	}
	if countRows(t, db, "profiles") != 1 {
		t.Fatal("the phone's backup lost the household's data")
	}

	// Nothing of the login survives anywhere in the bytes.
	var pwHash string
	if err := database.DB.QueryRow(
		`SELECT password_hash FROM admin_credentials WHERE id = 1`).Scan(&pwHash); err != nil {
		t.Fatal(err)
	}
	secrets := []string{e.secret, pwHash, adminauth.HashToken(d.token),
		adminauth.HashToken(findCookie(login.resp, "alx_admin").Value)}
	for _, rc := range e.recovery {
		secrets = append(secrets, adminauth.HashToken(adminauth.NormalizeRecoveryCode(rc)))
	}
	for _, secret := range secrets {
		if bytes.Contains(r.raw, []byte(secret)) {
			t.Fatalf("the phone's backup still contains %.12s…", secret)
		}
	}
	if !bytes.Contains(full.raw, []byte(e.secret)) {
		t.Fatal("check is blind: the full backup does not contain the secret either")
	}

	// A recovery code does for a step-up too, once.
	s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/backup",
		map[string]string{"recoveryCode": e.recovery[0]}, phone)
	s.expectRefusal("step-up-failed", "POST", "/api/v1/admin/backup",
		map[string]string{"recoveryCode": e.recovery[0]}, phone)
}

// 17. Approving into a person removed meanwhile is a 404 with a reason
// (was a 500); everything else about approval is unchanged.
func TestApproveIntoAnUnknownProfileIsNotFound(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	d := s.signIn(e, true, nil)
	auth := bearer(d.token)

	req := s.expectStatus(http.StatusOK, "POST", "/api/v1/link/request",
		map[string]string{"deviceLabel": "Living Room TV"}, nil)
	code := req.str("code")

	r := s.expectStatus(http.StatusNotFound, "POST", "/api/v1/admin/links/approve",
		map[string]string{"code": code, "profileId": "no-such-person"}, auth)
	if string(r.raw) != `{"error":"profile not found","reason":"profile-not-found"}` {
		t.Fatalf("unknown profile: %v", r)
	}
	// The code is checked first, as before: an unknown code is the
	// unchanged 404 without a reason.
	r = s.expectStatus(http.StatusNotFound, "POST", "/api/v1/admin/links/approve",
		map[string]string{"code": "ZZZZZZ", "profileId": "no-such-person"}, auth)
	if string(r.raw) != `{"error":"no pending link with that code"}` {
		t.Fatalf("unknown code: %v", r)
	}

	// Every approval from a phone takes a fresh code (test 17c).
	r = s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/links/approve",
		map[string]string{"code": code, "name": "Sarah",
			"totpCode": freshCode(t, e.secret)}, auth)
	if r.body["approved"] != true || r.str("profileId") == "" {
		t.Fatalf("approve by name: %v", r)
	}
	s.expectStatus(http.StatusConflict, "POST", "/api/v1/admin/links/approve",
		map[string]string{"code": code, "name": "Sarah"}, auth)

	// …and by an id that exists.
	req = s.expectStatus(http.StatusOK, "POST", "/api/v1/link/request", nil, nil)
	r = s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/links/approve",
		map[string]string{"code": req.str("code"), "profileId": r.str("profileId"),
			"totpCode": freshCode(t, e.secret)}, auth)
	if r.body["approved"] != true {
		t.Fatalf("approve by id: %v", r)
	}
}

const (
	approvePath = "/api/v1/admin/links/approve"
	// stepUpRequiredReply is a phone's approval without a code, byte for
	// byte.
	stepUpRequiredReply = `{"error":"approving a device from a phone ` +
		`needs a fresh code","reason":"step-up-required"}`
)

// linkRequest starts a link as a new device does: its code, and the
// claim secret it collects its token with.
func (s *testServer) linkRequest(label string) (code, claimSecret string) {
	s.t.Helper()
	r := s.expectStatus(http.StatusOK, "POST", "/api/v1/link/request",
		map[string]string{"deviceLabel": label}, nil)
	return r.str("code"), r.str("claimSecret")
}

// 17b. A device linked to someone already here reads their history and
// joins their party line, so a phone's admin token alone cannot link
// one: it takes a fresh code, as the phone's backup does. So does
// linking someone new (test 17c); approving from the browser is
// unchanged (test 17e).
func TestLinkingADeviceToSomeoneAlreadyHereFromAPhoneNeedsAFreshCode(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	profiles := repository.NewProfileRepository()
	sarah, err := profiles.Create("Sarah")
	if err != nil {
		t.Fatal(err)
	}
	sarahsPhone, err := profiles.MintToken(sarah.ID, "Sarah's phone")
	if err != nil {
		t.Fatal(err)
	}
	s.expectStatus(http.StatusOK, "POST", "/api/v1/stats/events", map[string]any{
		"events": []map[string]any{{
			"title": "Secret Title", "author": "Secret Author", "seconds": 60,
			"occurredAt": time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339),
		}},
	}, bearer(sarahsPhone))

	d := s.signIn(e, true, nil) // limiter: 1
	phone := bearer(d.token)
	code, claimSecret := s.linkRequest("Not Sarah's tablet")
	claim := map[string]string{"claimSecret": claimSecret}

	// The token alone, by her id or by her name: refused, and the link
	// is still waiting.
	for _, body := range []map[string]string{
		{"code": code, "profileId": sarah.ID},
		{"code": code, "name": "Sarah"},
	} { // limiter: 2, 3
		if r := s.expectStatus(http.StatusForbidden, "POST", approvePath, body, phone); string(r.raw) != stepUpRequiredReply {
			t.Fatalf("approve %v with the token alone: %v", body, r)
		}
	}
	// The code the phone signed in with is spent, and a wrong one is
	// wrong: step-up-failed, and the phone stays signed in.
	for _, bad := range []string{codeFor(t, e.secret, lastTOTPStep(t)), wrongCode(t, e.secret)} {
		r := s.expectRefusal("step-up-failed", "POST", approvePath, map[string]string{
			"code": code, "profileId": sarah.ID, "totpCode": bad}, phone) // limiter: 4, 5
		if r.str("error") != "wrong code" {
			t.Fatalf("step-up refusal %v", r)
		}
	}
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, phone)
	s.expectStatus(http.StatusAccepted, "POST", "/api/v1/link/claim", claim, nil)

	// A fresh code links it — to Sarah, history and all, which is what
	// the code is for.
	r := s.expectStatus(http.StatusOK, "POST", approvePath, map[string]string{
		"code": code, "profileId": sarah.ID, "totpCode": freshCode(t, e.secret)}, phone) // 6
	if r.str("profileId") != sarah.ID {
		t.Fatalf("approve with a fresh code: %v", r)
	}
	r = s.expectStatus(http.StatusOK, "POST", "/api/v1/link/claim", claim, nil)
	if r.str("profileName") != "Sarah" {
		t.Fatalf("claim: %v", r)
	}
	summary := s.expectStatus(http.StatusOK, "GET", "/api/v1/stats/summary", nil, bearer(r.str("token")))
	if !bytes.Contains(summary.raw, []byte("Secret Author")) {
		t.Fatalf("check is blind: the linked device does not see Sarah's history: %v", summary)
	}

	// By her name with a recovery code — once.
	code2, _ := s.linkRequest("")
	s.expectStatus(http.StatusOK, "POST", approvePath, map[string]string{
		"code": code2, "name": "Sarah", "recoveryCode": e.recovery[0]}, phone) // 7
	code3, _ := s.linkRequest("")
	s.expectRefusal("step-up-failed", "POST", approvePath, map[string]string{
		"code": code3, "name": "Sarah", "recoveryCode": e.recovery[0]}, phone) // 8

	// Someone new takes a code too (test 17c).
	if r := s.expectStatus(http.StatusForbidden, "POST", approvePath,
		map[string]string{"code": code3, "name": "Theo"}, phone); string(r.raw) != stepUpRequiredReply { // 9
		t.Fatalf("approve someone new with the token alone: %v", r)
	}

	// The browser is unchanged: its session took the password and a
	// code, and lasts 12 hours.
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil) // 10
	cookie := map[string]string{"Cookie": "alx_admin=" + findCookie(login.resp, "alx_admin").Value}
	code4, _ := s.linkRequest("")
	s.expectStatus(http.StatusOK, "POST", approvePath,
		map[string]string{"code": code4, "profileId": sarah.ID}, cookie)
}

// profileNamed is the person called name, or nil when nobody is.
func profileNamed(t *testing.T, name string) *repository.Profile {
	t.Helper()
	p, err := repository.NewProfileRepository().GetByName(name)
	if err == repository.ErrProfileNotFound {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// 17c. Every approval from a phone takes a fresh code, someone new
// included (decision D-A). A new person has no history yet, but a name
// is reused by every later approval: without a code, a phone token
// alone could create "Mike" before Mike is here, link its own device to
// him, and read his history from the day his devices are approved into
// that name.
func TestEveryApprovalFromAPhoneNeedsAFreshCode(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	sarah, err := repository.NewProfileRepository().Create("Sarah")
	if err != nil {
		t.Fatal(err)
	}

	phone := bearer(s.signIn(e, true, nil).token) // limiter: 1
	code, claimSecret := s.linkRequest("The phone's own tablet")
	claim := map[string]string{"claimSecret": claimSecret}

	// No code, for someone new and for someone already here: refused,
	// nobody created, and the link still waiting. Blank is no code.
	for _, body := range []map[string]string{
		{"code": code, "name": "Mike"},
		{"code": code, "profileId": sarah.ID},
		{"code": code, "name": "Mike", "totpCode": "  ", "recoveryCode": " "},
	} { // limiter: 2, 3, 4
		if r := s.expectStatus(http.StatusForbidden, "POST", approvePath, body, phone); string(r.raw) != stepUpRequiredReply {
			t.Fatalf("approve %v with the token alone: %v", body, r)
		}
	}
	// A wrong code, and the one the phone signed in with (spent):
	// step-up-failed, and the phone stays signed in.
	for _, bad := range []string{wrongCode(t, e.secret), codeFor(t, e.secret, lastTOTPStep(t))} {
		r := s.expectRefusal("step-up-failed", "POST", approvePath, map[string]string{
			"code": code, "name": "Mike", "totpCode": bad}, phone) // limiter: 5, 6
		if r.str("error") != "wrong code" {
			t.Fatalf("step-up refusal %v", r)
		}
	}
	if profileNamed(t, "Mike") != nil {
		t.Fatal("a refused approval created Mike")
	}
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, phone)
	s.expectStatus(http.StatusAccepted, "POST", "/api/v1/link/claim", claim, nil)

	// A fresh code: Mike is created, and the device is his.
	r := s.expectStatus(http.StatusOK, "POST", approvePath, map[string]string{
		"code": code, "name": "Mike", "totpCode": freshCode(t, e.secret)}, phone) // 7
	mike := profileNamed(t, "Mike")
	if mike == nil || r.str("profileId") != mike.ID {
		t.Fatalf("approve someone new with a fresh code: %v (Mike %v)", r, mike)
	}
	if r = s.expectStatus(http.StatusOK, "POST", "/api/v1/link/claim", claim, nil); r.str("profileName") != "Mike" {
		t.Fatalf("claim: %v", r)
	}

	// A recovery code does for someone new as well — once.
	code2, _ := s.linkRequest("")
	s.expectStatus(http.StatusOK, "POST", approvePath, map[string]string{
		"code": code2, "name": "Noor", "recoveryCode": e.recovery[1]}, phone) // 8
	code3, _ := s.linkRequest("")
	s.expectRefusal("step-up-failed", "POST", approvePath, map[string]string{
		"code": code3, "name": "Omar", "recoveryCode": e.recovery[1]}, phone) // 9
	if profileNamed(t, "Noor") == nil || profileNamed(t, "Omar") != nil {
		t.Fatal("the recovery code did not decide who was created")
	}
}

// 17d. One code raced across a phone's approvals and a web sign-in is
// spent once, through the same atomic claim as every sign-in: exactly
// one request wins, and every approval that lost is step-up-failed with
// its link still waiting and nobody created for it.
func TestOneCodeRacedAcrossPhoneApprovalsWinsOnce(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	phone := bearer(s.signIn(e, true, nil).token) // limiter: 1

	const approvals = 5 // limiter: 2-6, and the sign-in 7
	codes := make([]string, approvals)
	for i := range codes {
		codes[i], _ = s.linkRequest(fmt.Sprintf("device %d", i))
	}
	totp := freshCode(t, e.secret)

	replies := make([]response, approvals+1)
	errs := make([]error, approvals+1)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range replies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if i == approvals {
				replies[i], errs[i] = s.send("POST", loginPath, map[string]string{
					"password": testPassword, "code": totp}, nil)
				return
			}
			replies[i], errs[i] = s.send("POST", approvePath, map[string]string{
				"code": codes[i], "name": fmt.Sprintf("Person %d", i),
				"totpCode": totp}, phone)
		}(i)
	}
	close(start)
	wg.Wait()

	won, approved := 0, 0
	for i, r := range replies {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		switch {
		case r.status == http.StatusOK:
			won++
			if i < approvals {
				approved++
			}
		case i == approvals && r.status == http.StatusUnauthorized &&
			string(r.raw) == wrongPasswordOrCode:
		case i < approvals && r.status == http.StatusUnauthorized &&
			r.str("reason") == "step-up-failed":
			if profileNamed(t, fmt.Sprintf("Person %d", i)) != nil {
				t.Errorf("approval %d lost the code but created its person", i)
			}
		default:
			t.Errorf("request %d: unexpected reply %d %v", i, r.status, r)
		}
	}
	if won != 1 {
		t.Fatalf("%d requests won one code; want exactly 1", won)
	}
	pending := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/links", nil, phone)
	if want := float64(approvals - approved); pending.body["count"] != want {
		t.Fatalf("%v links still waiting, want %v: %v", pending.body["count"], want, pending)
	}
}

// 17e. The browser's approvals are unchanged: no code, for someone new
// or someone already here. The session cookie decides — a phone's token
// sent alongside it does not make the approval a phone's. Before
// enrolment the environment token approves without a code, as before.
func TestBrowserApprovalsNeedNoCode(t *testing.T) {
	s := newTestServer(t)
	code, _ := s.linkRequest("")
	s.expectStatus(http.StatusOK, "POST", approvePath,
		map[string]string{"code": code, "name": "Sarah"}, bearer(testAPIToken))
	sarah := profileNamed(t, "Sarah")
	if sarah == nil {
		t.Fatal("the environment token's approval created nobody")
	}

	e := enrollDirect(t)
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	session := "alx_admin=" + findCookie(login.resp, "alx_admin").Value
	cookie := map[string]string{"Cookie": session}
	phoneToken := s.signIn(e, true, nil).token
	both := map[string]string{"Cookie": session, "Authorization": "Bearer " + phoneToken}
	step := lastTOTPStep(t)

	for _, c := range []struct {
		body map[string]string
		hdr  map[string]string
	}{
		{map[string]string{"name": "Theo"}, cookie},
		{map[string]string{"name": "Sarah"}, cookie},
		{map[string]string{"profileId": sarah.ID}, cookie},
		{map[string]string{"name": "Uma"}, both},
		{map[string]string{"profileId": sarah.ID}, both},
	} {
		code, _ := s.linkRequest("")
		c.body["code"] = code
		s.expectStatus(http.StatusOK, "POST", approvePath, c.body, c.hdr)
	}
	if profileNamed(t, "Theo") == nil || profileNamed(t, "Uma") == nil {
		t.Fatal("the browser's approvals of someone new created nobody")
	}
	if lastTOTPStep(t) != step || recoveryRemaining(t) != len(e.recovery) {
		t.Fatal("a browser approval spent a second factor")
	}
}

// 18. /health says the whole feature is live, and publishes the public
// address only when the operator set one.
func TestHealthAdvertisesPhoneAdminAndThePublicURL(t *testing.T) {
	s := newTestServer(t)
	r := s.expectStatus(http.StatusOK, "GET", "/health", nil, nil)
	caps, _ := r.body["capabilities"].([]any)
	found := false
	for _, c := range caps {
		if c == "admin.device-token" {
			found = true
		}
	}
	if !found {
		t.Fatalf("capabilities %v lack admin.device-token", caps)
	}
	if _, ok := r.body["publicUrl"]; ok {
		t.Fatalf("publicUrl present with none configured: %v", r)
	}

	// An address config refuses is not published (decision D-B).
	t.Setenv("ALEXANDRIA_API_TOKEN", testAPIToken)
	t.Setenv("ALEXANDRIA_PUBLIC_URL",
		"https://alexandria.home.example.net@evil.example/?x=1#frag")
	loaded := config.Load()
	s = newTestServer(t, func(c *config.Config) { c.PublicURL = loaded.PublicURL })
	r = s.expectStatus(http.StatusOK, "GET", "/health", nil, nil)
	if _, ok := r.body["publicUrl"]; ok {
		t.Fatalf("publicUrl published for a refused address: %v", r)
	}

	s = newTestServer(t, func(c *config.Config) {
		c.PublicURL = "https://alexandria.example.com"
	})
	r = s.expectStatus(http.StatusOK, "GET", "/health", nil, nil)
	if r.str("publicUrl") != "https://alexandria.example.com" {
		t.Fatalf("publicUrl %v", r)
	}
	if r.str("version") != "0.3.0" {
		t.Fatalf("version changed: %v", r)
	}
}

// hasCapability reports whether /health lists c.
func hasCapability(t *testing.T, s *testServer, c string) bool {
	t.Helper()
	r := s.expectStatus(http.StatusOK, "GET", "/health", nil, nil)
	caps, _ := r.body["capabilities"].([]any)
	for _, have := range caps {
		if have == c {
			return true
		}
	}
	return false
}

// 19. GET /devices/me: a linked device asks who it is (decision D-C),
// and the answer never waits on the party line, which names a device
// only while something plays. deviceKey is the token id: the id the
// person list shows for the device, and the one a revoke takes. A link
// token only — no admin credential gets an answer.
func TestDevicesMeNamesTheCallersOwnLink(t *testing.T) {
	s := newTestServer(t)
	if !hasCapability(t, s, "devices.me") {
		t.Fatal("/health does not advertise devices.me")
	}

	code, claimSecret := s.linkRequest("Chris's iPhone")
	s.expectStatus(http.StatusOK, "POST", approvePath,
		map[string]string{"code": code, "name": "Chris"}, bearer(testAPIToken))
	claimed := s.expectStatus(http.StatusOK, "POST", "/api/v1/link/claim",
		map[string]string{"claimSecret": claimSecret}, nil)
	device := bearer(claimed.str("token"))

	// Nothing is playing, and the answer is there anyway.
	s.expectStatus(http.StatusNoContent, "GET", "/api/v1/nowplaying", nil, device)
	me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, device)
	if got, want := keysOf(me.body), []string{"deviceKey", "label", "profileId", "profileName"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %v, want exactly %v: %v", got, want, me)
	}
	if me.str("label") != "Chris's iPhone" || me.str("profileName") != "Chris" ||
		me.str("profileId") != claimed.str("profileId") || me.str("deviceKey") == "" {
		t.Fatalf("devices/me: %v (claim %v)", me, claimed)
	}

	// The person list names the same key, and a second device of the
	// same person has its own.
	sibling := s.expectStatus(http.StatusCreated, "POST", "/api/v1/devices",
		map[string]string{"label": "Chris's Watch"}, device)
	other := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil,
		bearer(sibling.str("token")))
	if other.str("deviceKey") == me.str("deviceKey") || other.str("label") != "Chris's Watch" ||
		other.str("profileId") != me.str("profileId") {
		t.Fatalf("the watch: %v, the phone: %v", other, me)
	}
	list := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, bearer(testAPIToken))
	if !bytes.Contains(list.raw, []byte(`"id":"`+me.str("deviceKey")+`"`)) {
		t.Fatalf("the person list does not name %s: %v", me.str("deviceKey"), list)
	}

	// A rename shows at once.
	s.expectStatus(http.StatusOK, "PATCH", "/api/v1/devices/me",
		map[string]string{"deviceLabel": "Living Room"}, device)
	if r := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, device); r.str("label") != "Living Room" ||
		r.str("deviceKey") != me.str("deviceKey") {
		t.Fatalf("after a rename: %v", r)
	}

	// No admin credential gets an answer: not the environment token, not
	// a phone's admin sign-in, not the browser's session, not nothing.
	e := enrollDirect(t)
	phone := s.signIn(e, true, nil)
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	for name, hdr := range map[string]map[string]string{
		"environment token": bearer(testAPIToken),
		"phone admin token": bearer(phone.token),
		"browser session":   {"Cookie": "alx_admin=" + findCookie(login.resp, "alx_admin").Value},
		"nothing":           nil,
	} {
		if r := s.do("GET", "/api/v1/devices/me", nil, hdr); r.status != http.StatusUnauthorized {
			t.Errorf("%s: %d %v, want 401", name, r.status, r)
		}
	}

	// The key is the one a revoke takes: revoked, the device is no one.
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/devices/"+me.str("deviceKey"),
		nil, bearer(phone.token))
	if r := s.do("GET", "/api/v1/devices/me", nil, device); r.status != http.StatusUnauthorized {
		t.Fatalf("a revoked device: %d %v, want 401", r.status, r)
	}
	s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, bearer(sibling.str("token")))
}
