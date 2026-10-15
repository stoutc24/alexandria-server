package provision

// The "Test accounts" check: a throwaway account made, signed in as and
// deleted, after whatever an earlier run left — and nothing outside its
// prefix, nor any admin, ever deleted (owner, 2026-10-14: "so long as we
// also have a cleanup on it so we don't create a bunch of junk accounts").

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeService is a service the check drives, keeping its accounts.
type fakeService struct {
	accounts   map[string]string // username -> password
	admins     map[string]bool
	signedInAs []string
	signInErr  error
	// removeErrOn fails the n-th removeTestAccounts call (1-based); 0 never.
	removeErrOn int
	removes     int
}

func (s *fakeService) Capability() Capability {
	return Capability{Type: "fake-selftest", Create: true, Email: EmailOptional}
}

func (s *fakeService) Ensure(_ context.Context, _ *http.Client, _ Admin, p Person, _ bool) (Result, error) {
	if _, ok := s.accounts[p.Username]; ok {
		return Result{}, ErrExists
	}
	s.accounts[p.Username] = p.Password
	return Result{Outcome: Created}, nil
}

func (s *fakeService) signInAs(_ context.Context, _ *http.Client, _, username, password string) error {
	s.signedInAs = append(s.signedInAs, username)
	if s.signInErr != nil {
		return s.signInErr
	}
	if s.accounts[username] != password {
		return &SignInError{Err: errors.New("wrong password")}
	}
	return nil
}

func (s *fakeService) removeTestAccounts(_ context.Context, _ *http.Client, _ Admin, prefix string) ([]string, error) {
	s.removes++
	if s.removes == s.removeErrOn {
		return nil, &SignInError{Err: errors.New("401")}
	}
	var removed []string
	for name := range s.accounts {
		if isTestAccount(name, prefix) && !s.admins[name] {
			delete(s.accounts, name)
			removed = append(removed, name)
		}
	}
	return removed, nil
}

func withFakeService(t *testing.T, s *fakeService) {
	t.Helper()
	providers["fake-selftest"] = s
	t.Cleanup(func() { delete(providers, "fake-selftest") })
}

func runSelfTest(t *testing.T) SelfTestReport {
	t.Helper()
	rep, err := SelfTest(context.Background(), http.DefaultClient, "fake-selftest",
		Admin{BaseURL: "http://svc", Username: "admin", Password: "admin-pass"})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func stepNames(rep SelfTestReport) string {
	var out []string
	for _, s := range rep.Steps {
		mark := "ok"
		if !s.OK {
			mark = "FAILED"
		}
		out = append(out, s.Name+" "+mark)
	}
	return strings.Join(out, ", ")
}

func TestSelfTestCleansUpFirstThenMakesSignsInAndDeletes(t *testing.T) {
	s := &fakeService{accounts: map[string]string{
		"emily":                       "x",
		"admin":                       "admin-pass",
		SelfTestPrefix + "old123":     "y",
		SelfTestPrefix + "stillanadm": "z",
	}, admins: map[string]bool{SelfTestPrefix + "stillanadm": true}}
	withFakeService(t, s)

	rep := runSelfTest(t)
	if !rep.OK || stepNames(rep) != "cleanup ok, create ok, sign-in ok, remove ok" {
		t.Fatalf("report: %+v", rep)
	}
	if !strings.Contains(rep.Steps[0].Detail, SelfTestPrefix+"old123") {
		t.Errorf("cleanup did not name the leftover: %q", rep.Steps[0].Detail)
	}
	if len(s.signedInAs) != 1 || !strings.HasPrefix(s.signedInAs[0], SelfTestPrefix) {
		t.Errorf("signed in as %v", s.signedInAs)
	}
	// Only the person, the admin and the test-named admin are left.
	if len(s.accounts) != 3 || s.accounts["emily"] == "" || s.accounts["admin"] == "" ||
		s.accounts[SelfTestPrefix+"stillanadm"] == "" {
		t.Errorf("accounts left: %v", s.accounts)
	}
	if rep.Leftover != "" {
		t.Errorf("leftover %q", rep.Leftover)
	}
}

func TestSelfTestDeletesTheAccountEvenWhenItsSignInFails(t *testing.T) {
	s := &fakeService{accounts: map[string]string{}, signInErr: &SignInError{Err: errors.New("401 Unauthorized")}}
	withFakeService(t, s)

	rep := runSelfTest(t)
	if rep.OK || stepNames(rep) != "cleanup ok, create ok, sign-in FAILED, remove ok" {
		t.Fatalf("report: %+v", rep)
	}
	if got := rep.Steps[2].Detail; got != "the sign-in was refused: 401 Unauthorized" {
		t.Errorf("sign-in detail %q", got)
	}
	if len(s.accounts) != 0 || rep.Leftover != "" {
		t.Errorf("left behind: %v %q", s.accounts, rep.Leftover)
	}
}

func TestSelfTestNamesAnAccountItCouldNotDelete(t *testing.T) {
	s := &fakeService{accounts: map[string]string{}, removeErrOn: 2}
	withFakeService(t, s)

	rep := runSelfTest(t)
	if rep.OK || stepNames(rep) != "cleanup ok, create ok, sign-in ok, remove FAILED" {
		t.Fatalf("report: %+v", rep)
	}
	if !strings.HasPrefix(rep.Leftover, SelfTestPrefix) {
		t.Errorf("leftover %q", rep.Leftover)
	}
}

func TestSelfTestStopsWhenItCannotListTheAccounts(t *testing.T) {
	s := &fakeService{accounts: map[string]string{}, removeErrOn: 1}
	withFakeService(t, s)

	rep := runSelfTest(t)
	if rep.OK || stepNames(rep) != "cleanup FAILED" || len(s.accounts) != 0 {
		t.Fatalf("report %+v, accounts %v", rep, s.accounts)
	}
}

func TestEveryServiceThatMakesAccountsCanBeTested(t *testing.T) {
	for typ := range providers {
		if typ == "fake-selftest" {
			continue
		}
		if !SelfTestable(typ) {
			t.Errorf("%s has no self-test", typ)
		}
	}
	p := selfTestPerson(Capability{Email: EmailUsername})
	if !looksLikeEmail(p.Username) || !strings.HasPrefix(p.Username, SelfTestPrefix) {
		t.Errorf("an email-username service gets %q", p.Username)
	}
}

// ---- each service deletes only its test accounts -----------------------------

const (
	testA  = SelfTestPrefix + "aaaaaa"
	testB  = SelfTestPrefix + "bbbbbb"
	testCC = SelfTestPrefix + "cccccc"
)

func removeOn(t *testing.T, typ string, srv *httptest.Server) []string {
	t.Helper()
	prov, _ := For(typ)
	removed, err := prov.(tester).removeTestAccounts(context.Background(), srv.Client(),
		Admin{BaseURL: srv.URL + "/", Username: "admin", Password: "admin-pass"}, SelfTestPrefix)
	if err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	return removed
}

func deletes(f *fake) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if c.method == http.MethodDelete {
			p := c.path
			if c.query != "" {
				p += "?" + c.query
			}
			out = append(out, p)
		}
	}
	return out
}

func noContent(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(204) }

func expectDeletes(t *testing.T, f *fake, want ...string) {
	t.Helper()
	if got := deletes(f); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("deleted %v, want %v", got, want)
	}
}

func TestABSDeletesOnlyTestListeners(t *testing.T) {
	f, srv := absFake(t, []map[string]any{
		{"id": "t1", "username": testA, "type": "user"},
		{"id": "t2", "username": testB, "type": "admin"},
		{"id": "u1", "username": "emily", "type": "user"},
	})
	f.on("DELETE /api/users/t1", noContent)
	removed := removeOn(t, "abs", srv)
	expectDeletes(t, f, "/api/users/t1")
	if len(removed) != 1 || removed[0] != testA {
		t.Errorf("removed %v", removed)
	}
}

func TestGrimmoryDeletesOnlyTestReaders(t *testing.T) {
	f, srv := grimmoryFake(t, []map[string]any{
		{"id": 5, "username": testA, "permissions": map[string]any{}},
		{"id": 6, "username": testB, "permissions": map[string]any{"admin": true}},
		{"id": 7, "username": "emily"},
	})
	f.on("DELETE /api/v1/users/5", noContent)
	removeOn(t, "booklore", srv)
	expectDeletes(t, f, "/api/v1/users/5")
}

func TestJellyfinDeletesOnlyTestUsersAndSignsOut(t *testing.T) {
	f, srv := jellyfinFake(t, []map[string]any{
		{"Id": "j1", "Name": testA, "Policy": map[string]any{"IsAdministrator": false}},
		{"Id": "j2", "Name": testB, "Policy": map[string]any{"IsAdministrator": true}},
		{"Id": "j3", "Name": "emily", "Policy": map[string]any{}},
	})
	f.on("DELETE /Users/j1", noContent)
	removeOn(t, "jellyfin", srv)
	expectDeletes(t, f, "/Users/j1")
	if len(f.called("POST /Sessions/Logout")) != 1 {
		t.Error("the admin session was not signed out")
	}
}

func TestKomgaDeletesOnlyTestEmailsAndChecksTheNewSignIn(t *testing.T) {
	f, srv := newFake(t)
	f.json("GET /api/v2/users", 200, []map[string]any{
		{"id": "k1", "email": testA + "@selftest.invalid", "roles": []string{"USER"}},
		{"id": "k2", "email": testB + "@selftest.invalid", "roles": []string{"ADMIN"}},
		{"id": "k3", "email": "emily@example.com", "roles": []string{"USER"}},
	})
	f.on("DELETE /api/v2/users/k1", noContent)
	f.json("GET /api/v2/users/me", 200, map[string]any{"id": "k1"})
	removeOn(t, "komga", srv)
	expectDeletes(t, f, "/api/v2/users/k1")

	err := komgaProvider{}.signInAs(context.Background(), srv.Client(), srv.URL, testA+"@selftest.invalid", "pw")
	me := f.called("GET /api/v2/users/me")
	if err != nil || len(me) != 1 || !strings.HasPrefix(me[0].auth, "Basic ") {
		t.Fatalf("sign-in as the test account: %v %+v", err, me)
	}
}

func TestKavitaDeletesOnlyTestUsersById(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/Account/login", 200, map[string]any{"token": "kv-1"})
	f.json("GET /api/Users", 200, []map[string]any{
		{"id": 11, "username": testA, "roles": []string{"Pleb"}},
		{"id": 12, "username": testB, "roles": []string{"Admin"}},
		{"id": 13, "username": "emily", "roles": []string{"Pleb"}},
	})
	f.on("DELETE /api/Users/delete-user", noContent)
	removeOn(t, "kavita", srv)
	expectDeletes(t, f, "/api/Users/delete-user?userId=11")
}

func TestNavidromeDeletesOnlyTestUsers(t *testing.T) {
	f, srv := navidromeFake(t, []map[string]any{
		{"id": "n1", "userName": testA, "isAdmin": false},
		{"id": "n2", "userName": testB, "isAdmin": true},
		{"id": "n3", "userName": "emily", "isAdmin": false},
	}, false)
	f.on("DELETE /api/user/n1", noContent)
	removeOn(t, "navidrome", srv)
	expectDeletes(t, f, "/api/user/n1")
}

func TestStumpHardDeletesOnlyTestUsersAndNeverTheOwner(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/v2/auth/login", 200, map[string]any{"accessToken": "st-1"})
	var deleted []string
	f.on("POST /api/graphql", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		if strings.Contains(req.Query, "deleteUser") {
			if !strings.Contains(req.Query, "hardDelete: true") {
				t.Errorf("a soft delete leaves the account in Stump: %s", req.Query)
			}
			deleted = append(deleted, req.Variables["id"].(string))
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"deleteUser": map[string]any{"id": req.Variables["id"]}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"users": map[string]any{"nodes": []map[string]any{
			{"id": "s1", "username": testA, "isServerOwner": false},
			{"id": "s2", "username": testB, "isServerOwner": true},
			{"id": "s3", "username": "emily", "isServerOwner": false},
		}}}})
	})
	removeOn(t, "stump", srv)
	if strings.Join(deleted, " ") != "s1" {
		t.Fatalf("deleted %v", deleted)
	}
}

func TestStorytellerDeletesTestUsersAndUnacceptedTestInvites(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/v2/token", 200, map[string]any{"access_token": "tok"})
	f.json("GET /api/v2/users", 200, []map[string]any{
		{"id": "s1", "username": testA, "email": testA + "@selftest.invalid", "permissions": map[string]bool{"bookRead": true}},
		{"id": "s2", "username": "", "email": testB + "@selftest.invalid", "permissions": map[string]bool{}},
		{"id": "s3", "username": testCC, "email": "x@y.z", "permissions": map[string]bool{"userCreate": true}},
		{"id": "s4", "username": "emily", "email": "emily@example.com", "permissions": map[string]bool{}},
	})
	f.on("DELETE /api/v2/users/s1", noContent)
	f.on("DELETE /api/v2/users/s2", noContent)
	removeOn(t, "storyteller", srv)
	expectDeletes(t, f, "/api/v2/users/s1", "/api/v2/users/s2")
}

func TestCalibreWebDeletesTestUsersWithTheSessionsCSRFToken(t *testing.T) {
	f, srv := calibreWebFake(t, []map[string]any{
		{"id": 2, "name": testA, "role": 2},
		{"id": 3, "name": testB, "role": 1}, // admin bit
		{"id": 4, "name": "emily", "role": 2},
	})
	var csrf string
	var asked map[string]any
	f.on("POST /ajax/deleteuser", func(w http.ResponseWriter, r *http.Request, body []byte) {
		csrf = r.Header.Get("X-CSRFToken")
		_ = json.Unmarshal(body, &asked)
		_ = json.NewEncoder(w).Encode([]map[string]any{{"type": "success", "message": "User deleted"}})
	})
	removed := removeOn(t, "calibreweb", srv)
	if csrf != "tok-2" {
		t.Errorf("CSRF header %q", csrf)
	}
	ids, _ := asked["userid"].([]any)
	if len(ids) != 1 || ids[0] != float64(2) || len(removed) != 1 || removed[0] != testA {
		t.Fatalf("asked %v, removed %v", asked, removed)
	}
}
