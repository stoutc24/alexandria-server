package main

import (
	"encoding/json"
	"fmt"
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
// with a user list that remembers what was made. An account's permissions
// and libraries change as Audiobookshelf's own PATCH changes them: the
// permissions named are merged in, and the library list is taken only
// alongside them. With ignore it says yes and changes neither.
type absStub struct {
	mu        sync.Mutex
	users     []map[string]any
	passwords map[string]string
	// made is each new account's whole body, by username.
	made   map[string]map[string]any
	ignore bool
	// sneak: a PATCH also lets the account update items, unasked.
	sneak bool
}

func absUserPermissions() map[string]any {
	return map[string]any{"download": true, "update": false, "delete": false, "upload": false,
		"createEreader": false, "accessAllLibraries": true, "accessAllTags": true, "accessExplicitContent": false}
}

func newABSStub(t *testing.T) (*absStub, *httptest.Server) {
	a := &absStub{
		users: []map[string]any{{"id": "r1", "username": "root", "type": "root",
			"permissions": absUserPermissions(), "librariesAccessible": []any{}}},
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
			perms := absUserPermissions()
			if sent, ok := in["permissions"].(map[string]any); ok {
				for k, v := range sent {
					perms[k] = v
				}
			}
			libs, _ := in["librariesAccessible"].([]any)
			if libs == nil {
				libs = []any{}
			}
			a.users = append(a.users, map[string]any{"id": id, "username": in["username"], "type": in["type"],
				"permissions": perms, "librariesAccessible": libs})
			a.passwords[in["username"].(string)] = in["password"].(string)
			a.made[in["username"].(string)] = in
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"id": id}})
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/users/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/users/")
			for _, u := range a.users {
				if u["id"] != id {
					continue
				}
				if pw, ok := in["password"].(string); ok && pw != "" {
					a.passwords[u["username"].(string)] = pw
				}
				if sent, ok := in["permissions"].(map[string]any); ok && !a.ignore {
					perms := u["permissions"].(map[string]any)
					for k, v := range sent {
						if b, isBool := v.(bool); isBool && perms[k] != nil {
							perms[k] = b
						}
					}
					if libs, ok := in["librariesAccessible"].([]any); ok {
						u["librariesAccessible"] = libs
					}
					if a.sneak {
						perms["update"] = true
					}
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

// access is what an account has now, in a line.
func (a *absStub) access(user string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, u := range a.users {
		if u["username"] == user {
			p := u["permissions"].(map[string]any)
			raw, _ := json.Marshal(u["librariesAccessible"])
			return fmt.Sprintf("download=%v all=%v libraries=%s update=%v", p["download"], p["accessAllLibraries"], raw, p["update"])
		}
	}
	return ""
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
	// The kept address, as it was saved (trailing slash trimmed), for the
	// forms to offer; none where nothing is kept.
	if byType["abs"]["baseUrl"] != srv.URL {
		t.Fatalf("abs baseUrl = %v, want %s", byType["abs"]["baseUrl"], srv.URL)
	}
	if _, has := byType["jellyfin"]["baseUrl"]; has {
		t.Fatalf("jellyfin has no kept sign-in but carries a baseUrl: %v", byType["jellyfin"])
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
	emily := newPerson(t, s, "Emily")
	path := "/api/v1/admin/profiles/" + emily + "/provision"
	// Change access too: people and media servers both.
	access := "/api/v1/admin/profiles/" + emily + "/services/nope/provision"
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
			s.refused(http.StatusNotFound, "service-not-found", "GET", access, nil, bearer(raw))
			s.refused(http.StatusNotFound, "service-not-found", "PUT", access, map[string]any{"allLibraries": true}, bearer(raw))
			continue
		}
		s.refused(http.StatusForbidden, "access-denied", "GET", access, nil, bearer(raw))
		s.refused(http.StatusForbidden, "access-denied", "PUT", access, map[string]any{"allLibraries": true}, bearer(raw))
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
	// cleanup, create, sign-in, access (changed and read back), remove.
	if r.body["service"] != "Audiobookshelf" || report["ok"] != true || len(steps) != 5 ||
		steps[3].(map[string]any)["name"] != "access" {
		t.Fatalf("%v", r.body)
	}
	if got := abs.usernames(); len(got) != 1 || got[0] != "root" {
		t.Fatalf("accounts after the test: %v, want only root", got)
	}
}

// addLogin saves one of a person's logins and says its id.
func addLogin(t *testing.T, s *testServer, profile string, body map[string]any) string {
	t.Helper()
	r := s.do("POST", "/api/v1/admin/profiles/"+profile+"/services", body, bearer(testAPIToken))
	if r.status != 201 {
		t.Fatalf("add login: %d %v", r.status, r)
	}
	return r.body["service"].(map[string]any)["id"].(string)
}

// Owner, 2026-10-14: "allow the admins to adjust permissions and library
// access from the server". The account a saved login names is read and
// changed with the kept sign-in, then read back.
func TestChangeAccessReadsAndChangesTheAccountASavedLoginNames(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	abs, srv := newABSStub(t)
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": srv.URL + "/", "username": "admin", "password": "admin-pass"}, admin)
	kate := newPerson(t, s, "Kate")
	s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/profiles/"+kate+"/provision", map[string]any{
		"type": "abs", "username": "kate", "password": "pw-1"}, admin)
	accessOf := func(login string) string {
		return "/api/v1/admin/profiles/" + kate + "/services/" + login + "/provision"
	}
	path := accessOf(addLogin(t, s, kate, map[string]any{"type": "abs", "baseUrl": "http://abs.lan:13378",
		"loginKind": "password", "username": "Kate", "password": "pw-1"}))

	// Read: what it has, where the change would go, and no secret.
	r := s.expectStatus(http.StatusOK, "GET", path, nil, admin)
	raw, _ := json.Marshal(r.body)
	host := strings.TrimPrefix(srv.URL, "http://")
	if string(raw) != `{"access":{"allLibraries":true,"download":true,"libraries":["lib-books","lib-kids"]},`+
		`"can":{"download":true,"laterLibraries":true,"libraries":true},"host":"`+host+`",`+
		`"libraries":[{"id":"lib-books","name":"Audiobooks"},{"id":"lib-kids","name":"Kids"}],`+
		`"loginHost":"abs.lan:13378","service":"Audiobookshelf","username":"Kate"}` {
		t.Fatalf("read: %s", raw)
	}

	// Changed, and the answer is the account read back.
	r = s.expectStatus(http.StatusOK, "PUT", path, map[string]any{
		"allLibraries": false, "libraries": []string{"lib-kids"}, "download": false}, admin)
	if got, _ := json.Marshal(r.body["access"]); string(got) != `{"allLibraries":false,"download":false,"libraries":["lib-kids"]}` {
		t.Fatalf("answer: %s", got)
	}
	if got := abs.access("kate"); got != `download=false all=false libraries=["lib-kids"] update=false` {
		t.Fatalf("on the service: %s", got)
	}

	// Refused before anything changes.
	s.refused(http.StatusBadRequest, "no-libraries", "PUT", path, map[string]any{"libraries": []string{}}, admin)
	s.refused(http.StatusConflict, "libraries-gone", "PUT", path, map[string]any{"libraries": []string{"lib-gone"}}, admin)
	root := accessOf(addLogin(t, s, kate, map[string]any{"type": "abs", "baseUrl": srv.URL,
		"loginKind": "password", "username": "root", "password": "x"}))
	s.refused(http.StatusConflict, "admin-account", "GET", root, nil, admin)
	s.refused(http.StatusConflict, "admin-account", "PUT", root, map[string]any{"allLibraries": true}, admin)
	nobody := accessOf(addLogin(t, s, kate, map[string]any{"type": "abs", "baseUrl": srv.URL,
		"loginKind": "password", "username": "nobody", "password": "x"}))
	s.refused(http.StatusNotFound, "no-account", "GET", nobody, nil, admin)
	keyed := accessOf(addLogin(t, s, kate, map[string]any{"type": "abs", "baseUrl": srv.URL,
		"loginKind": "apikey", "apiKey": "k-1"}))
	s.refused(http.StatusConflict, "no-username", "GET", keyed, nil, admin)
	cw := accessOf(addLogin(t, s, kate, map[string]any{"type": "calibreweb", "baseUrl": "http://cw.lan",
		"loginKind": "password", "username": "kate", "password": "x"}))
	s.refused(http.StatusBadRequest, "not-supported", "GET", cw, nil, admin)
	jf := accessOf(addLogin(t, s, kate, map[string]any{"type": "jellyfin", "baseUrl": "http://jf.lan",
		"loginKind": "password", "username": "kate", "password": "x"}))
	s.refused(http.StatusConflict, "no-service-admin", "GET", jf, nil, admin)
	s.refused(http.StatusNotFound, "service-not-found", "GET", accessOf("nope"), nil, admin)
	s.refused(http.StatusNotFound, "profile-not-found", "GET", "/api/v1/admin/profiles/nobody/services/x/provision", nil, admin)
	if got := abs.access("kate"); got != `download=false all=false libraries=["lib-kids"] update=false` {
		t.Fatalf("a refusal changed the account: %s", got)
	}

	// A service that says yes and keeps things as they were is no success:
	// the answer says so, with what the account has.
	abs.mu.Lock()
	abs.ignore = true
	abs.mu.Unlock()
	r = s.do("PUT", path, map[string]any{"allLibraries": true, "download": true}, admin)
	if got, _ := json.Marshal(r.body["access"]); r.status != http.StatusBadGateway || r.str("reason") != "not-taken" ||
		string(got) != `{"allLibraries":false,"download":false,"libraries":["lib-kids"]}` ||
		!strings.Contains(r.str("error"), "downloads and libraries unchanged") {
		t.Fatalf("not taken: %d %v", r.status, r.body)
	}

	// Nor is one that changes a right nobody asked about: the change took,
	// and the answer names what else moved.
	abs.mu.Lock()
	abs.ignore, abs.sneak = false, true
	abs.mu.Unlock()
	r = s.do("PUT", path, map[string]any{"allLibraries": true, "download": true}, admin)
	if got, _ := json.Marshal(r.body["access"]); r.status != http.StatusBadGateway || r.str("reason") != "also-changed" ||
		string(got) != `{"allLibraries":true,"download":true,"libraries":["lib-books","lib-kids"]}` ||
		!strings.Contains(r.str("error"), "also changed permissions.update") {
		t.Fatalf("also changed: %d %v", r.status, r.body)
	}
}
