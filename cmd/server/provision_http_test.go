package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// absStub is a small Audiobookshelf: the calls internal/provision makes,
// with a user list that remembers what was made.
type absStub struct {
	mu        sync.Mutex
	users     []map[string]any
	passwords map[string]string
	// made is each new account's whole body, by username.
	made map[string]map[string]any
}

func newABSStub(t *testing.T) (*absStub, *httptest.Server) {
	a := &absStub{
		users:     []map[string]any{{"id": "r1", "username": "root", "type": "root"}},
		passwords: map[string]string{},
		made:      map[string]map[string]any{},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		a.mu.Lock()
		defer a.mu.Unlock()
		switch {
		case r.Method == "POST" && r.URL.Path == "/login":
			name, _ := in["username"].(string)
			if pw, ok := a.passwords[name]; ok && in["password"] == pw {
				// An account made here signs in with its own password.
				_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"accessToken": "user-jwt"}})
				return
			}
			if in["username"] != "admin" || in["password"] != "admin-pass" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"accessToken": "abs-jwt"}})
		case r.Header.Get("Authorization") != "Bearer abs-jwt":
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
		case r.Method == "GET" && r.URL.Path == "/api/users":
			_ = json.NewEncoder(w).Encode(map[string]any{"users": a.users})
		case r.Method == "GET" && r.URL.Path == "/api/libraries":
			_ = json.NewEncoder(w).Encode(map[string]any{"libraries": []map[string]any{
				{"id": "lib-books", "name": "Audiobooks", "mediaType": "book"},
				{"id": "lib-kids", "name": "Kids", "mediaType": "book"},
			}})
		case r.Method == "POST" && r.URL.Path == "/api/users":
			id := "u" + string(rune('0'+len(a.users)))
			a.users = append(a.users, map[string]any{"id": id, "username": in["username"], "type": in["type"]})
			a.passwords[in["username"].(string)] = in["password"].(string)
			a.made[in["username"].(string)] = in
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": id}})
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/users/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/users/")
			for _, u := range a.users {
				if u["id"] == id {
					a.passwords[u["username"].(string)] = in["password"].(string)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/users/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/users/")
			kept := a.users[:0]
			for _, u := range a.users {
				if u["id"] == id {
					delete(a.passwords, u["username"].(string))
					continue
				}
				kept = append(kept, u)
			}
			a.users = kept
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return a, srv
}

func (a *absStub) usernames() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, u := range a.users {
		out = append(out, u["username"].(string))
	}
	return out
}

func (a *absStub) password(user string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.passwords[user]
}

// libraries is what a new account was given: "all", or the ids.
func (a *absStub) libraries(user string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	m := a.made[user]
	if m == nil {
		return ""
	}
	if m["permissions"].(map[string]any)["accessAllLibraries"] == true {
		return "all"
	}
	raw, _ := json.Marshal(m["librariesAccessible"])
	return string(raw)
}

func newPerson(t *testing.T, s *testServer, name string) string {
	t.Helper()
	r := s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": name}, bearer(testAPIToken))
	if r.status != 201 {
		t.Fatalf("create profile: %d %v", r.status, r)
	}
	return r.body["profile"].(map[string]any)["id"].(string)
}

// Owner, 2026-10-14: make Emily's account with the kept admin sign-in, or
// set its password if it is there — the server makes the calls, for the
// admin page and the phone alike.
func TestProvisionMakesThenResetsWithTheKeptServerAdminSignIn(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	abs, srv := newABSStub(t)
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": srv.URL + "/", "username": "admin", "password": "admin-pass"}, admin)
	emily := newPerson(t, s, "Emily")
	path := "/api/v1/admin/profiles/" + emily + "/provision"

	// What can be done where: ABS has a kept sign-in, Jellyfin has none.
	r := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/provision", nil, admin)
	raw, _ := json.Marshal(r.body)
	if strings.Contains(string(raw), "admin-pass") {
		t.Fatalf("the capabilities carry the kept password: %s", raw)
	}
	byType := map[string]map[string]any{}
	for _, v := range r.body["services"].([]any) {
		m := v.(map[string]any)
		byType[m["type"].(string)] = m
	}
	if byType["abs"]["serverAdmin"] != true || byType["abs"]["host"] != strings.TrimPrefix(srv.URL, "http://") ||
		byType["jellyfin"]["serverAdmin"] != false || byType["storyteller"]["reset"] != false || byType["booklore"]["email"] != "required" {
		t.Fatalf("capabilities: %v", byType)
	}

	// New: made with the given login.
	r = s.expectStatus(http.StatusOK, "POST", path, map[string]any{
		"type": "abs", "username": "emily", "password": "first-pw"}, admin)
	if r.str("outcome") != "created" || r.str("service") != "Audiobookshelf" || abs.password("emily") != "first-pw" {
		t.Fatalf("create: %v (stub has %q)", r, abs.password("emily"))
	}
	if _, has := r.body["password"]; has {
		t.Fatal("the answer carries a password")
	}

	// There already: the admin is asked first, and nothing changes...
	s.refused(http.StatusConflict, "account-exists", "POST", path, map[string]any{
		"type": "abs", "username": "Emily", "password": "second-pw"}, admin)
	if abs.password("emily") != "first-pw" {
		t.Fatal("the password changed without the admin saying so")
	}
	// ...then, told to, the password is set.
	r = s.expectStatus(http.StatusOK, "POST", path, map[string]any{
		"type": "abs", "username": "Emily", "password": "second-pw", "allowReset": true}, admin)
	if r.str("outcome") != "reset" || abs.password("emily") != "second-pw" {
		t.Fatalf("reset: %v (stub has %q)", r, abs.password("emily"))
	}

	// Never an admin account, and never the kept sign-in's own.
	s.refused(http.StatusConflict, "admin-account", "POST", path, map[string]any{
		"type": "abs", "username": "root", "password": "x", "allowReset": true}, admin)
	s.refused(http.StatusConflict, "admin-account", "POST", path, map[string]any{
		"type": "abs", "username": "ADMIN", "password": "x", "allowReset": true}, admin)

	// No kept sign-in for the service; a service that has no accounts here.
	s.refused(http.StatusConflict, "no-service-admin", "POST", path, map[string]any{
		"type": "jellyfin", "username": "emily", "password": "x"}, admin)
	s.refused(http.StatusBadRequest, "not-supported", "POST", path, map[string]any{
		"type": "plex", "username": "emily", "password": "x"}, admin)
	s.refused(http.StatusBadRequest, "bad-request", "POST", path, map[string]any{
		"type": "abs", "username": "emily"}, admin)
	s.refused(http.StatusNotFound, "profile-not-found", "POST", "/api/v1/admin/profiles/nobody/provision",
		map[string]any{"type": "abs", "username": "emily", "password": "x"}, admin)
}

func TestProvisionSaysWhenTheKeptSignInFailsOrTheServiceIsGone(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	_, srv := newABSStub(t)
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": srv.URL, "username": "admin", "password": "stale-pass"}, admin)
	path := "/api/v1/admin/profiles/" + newPerson(t, s, "Emily") + "/provision"
	s.refused(http.StatusBadGateway, "admin-sign-in-failed", "POST", path, map[string]any{
		"type": "abs", "username": "emily", "password": "x"}, admin)

	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": gone.URL, "username": "admin", "password": "admin-pass"}, admin)
	s.refused(http.StatusBadGateway, "service-unreachable", "POST", path, map[string]any{
		"type": "abs", "username": "emily", "password": "x"}, admin)
}

// Owner, 2026-10-14: "need to also pick libraries available". The forms
// list the service's libraries with the kept sign-in, and a new account
// sees the picked ones; nothing picked is every library.
func TestProvisionListsLibrariesAndMakesTheAccountWithThePickedOnes(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	abs, srv := newABSStub(t)
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": srv.URL, "username": "admin", "password": "admin-pass"}, admin)
	path := "/api/v1/admin/profiles/" + newPerson(t, s, "Emily") + "/provision"

	r := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/provision", nil, admin)
	for _, v := range r.body["services"].([]any) {
		m := v.(map[string]any)
		if want := m["type"] != "storyteller" && m["type"] != "calibreweb"; m["libraries"] != want {
			t.Fatalf("%v libraries = %v", m["type"], m["libraries"])
		}
	}

	r = s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/provision/abs/libraries", nil, admin)
	raw, _ := json.Marshal(r.body)
	if strings.Contains(string(raw), "admin-pass") {
		t.Fatalf("the list carries the kept password: %s", raw)
	}
	if string(raw) != `{"host":"`+strings.TrimPrefix(srv.URL, "http://")+`","libraries":[{"id":"lib-books","name":"Audiobooks"},{"id":"lib-kids","name":"Kids"}],"service":"Audiobookshelf"}` {
		t.Fatalf("libraries: %s", raw)
	}

	s.expectStatus(http.StatusOK, "POST", path, map[string]any{
		"type": "abs", "username": "kate", "password": "pw-1", "libraries": []string{"lib-kids"}}, admin)
	if got := abs.libraries("kate"); got != `["lib-kids"]` {
		t.Fatalf("picked: %s", got)
	}
	s.expectStatus(http.StatusOK, "POST", path, map[string]any{
		"type": "abs", "username": "emily", "password": "pw-2"}, admin)
	if got := abs.libraries("emily"); got != "all" {
		t.Fatalf("nothing picked: %s", got)
	}
	s.expectStatus(http.StatusOK, "POST", path, map[string]any{
		"type": "abs", "username": "sam", "password": "pw-3", "libraries": nil}, admin)
	if got := abs.libraries("sam"); got != "all" {
		t.Fatalf("null: %s", got)
	}

	s.refused(http.StatusBadRequest, "no-libraries", "POST", path, map[string]any{
		"type": "abs", "username": "noah", "password": "pw", "libraries": []string{}}, admin)
	s.refused(http.StatusConflict, "libraries-gone", "POST", path, map[string]any{
		"type": "abs", "username": "noah", "password": "pw", "libraries": []string{"lib-gone"}}, admin)
	if abs.libraries("noah") != "" {
		t.Fatal("made with no library")
	}

	s.refused(http.StatusConflict, "no-service-admin", "GET", "/api/v1/admin/provision/jellyfin/libraries", nil, admin)
	s.refused(http.StatusBadRequest, "not-supported", "GET", "/api/v1/admin/provision/plex/libraries", nil, admin)
}

func TestProvisionLibrariesSayWhenTheKeptSignInFails(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	_, srv := newABSStub(t)
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": srv.URL, "username": "admin", "password": "stale-pass"}, admin)
	s.refused(http.StatusBadGateway, "admin-sign-in-failed", "GET", "/api/v1/admin/provision/abs/libraries", nil, admin)
}

// Making accounts on media servers with the kept admin sign-ins touches
// people AND media servers: an account's own admin sign-in needs "full".
func TestProvisionNeedsFullAdminAccess(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	sam := newPerson(t, s, "Sam")
	path := "/api/v1/admin/profiles/" + newPerson(t, s, "Emily") + "/provision"
	body := map[string]any{"type": "abs", "username": "emily", "password": "x"}
	// Phones' admin sign-ins exist once an admin login is enrolled.
	enrollDirect(t)
	for _, scope := range []string{"manage", "serveradmin", "full"} {
		raw := "alxa_provision_test_" + scope
		if _, err := repository.NewAdminAuthRepository().CreateDeviceToken(adminauth.HashToken(raw),
			repository.NewAdminDevice{Label: "Sam's phone (" + scope + ")", ProfileID: sam, Scope: scope,
				CreatedVia: repository.AdminDeviceViaAccount}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if scope == "full" {
			// Past the gate: refused only because nothing is kept for ABS.
			s.refused(http.StatusConflict, "no-service-admin", "POST", path, body, bearer(raw))
			s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/provision", nil, bearer(raw))
			s.refused(http.StatusConflict, "no-service-admin", "GET", "/api/v1/admin/provision/abs/libraries", nil, bearer(raw))
			s.refused(http.StatusConflict, "no-service-admin", "POST", "/api/v1/admin/provision/abs/selftest", nil, bearer(raw))
			s.refused(http.StatusBadRequest, "not-supported", "POST", "/api/v1/admin/provision/plex/selftest", nil, bearer(raw))
			continue
		}
		s.refused(http.StatusForbidden, "access-denied", "POST", path, body, bearer(raw))
		s.refused(http.StatusForbidden, "access-denied", "GET", "/api/v1/admin/provision", nil, bearer(raw))
		s.refused(http.StatusForbidden, "access-denied", "GET", "/api/v1/admin/provision/abs/libraries", nil, bearer(raw))
		s.refused(http.StatusForbidden, "access-denied", "POST", "/api/v1/admin/provision/abs/selftest", nil, bearer(raw))
	}
}

// Owner, 2026-10-14: a test button, "so long as we also have a cleanup on it
// so we don't create a bunch of junk accounts".
func TestProvisionSelfTestMakesSignsInAndDeletesWithTheKeptSignIn(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	abs, srv := newABSStub(t)
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": srv.URL + "/", "username": "admin", "password": "admin-pass"}, admin)

	r := s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/provision/abs/selftest", nil, admin)
	report, _ := r.body["report"].(map[string]any)
	steps, _ := report["steps"].([]any)
	if r.body["service"] != "Audiobookshelf" || report["ok"] != true || len(steps) != 4 {
		t.Fatalf("%v", r.body)
	}
	if got := abs.usernames(); len(got) != 1 || got[0] != "root" {
		t.Fatalf("accounts after the test: %v, want only root", got)
	}
}
