package provision

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// fake is one service: handlers by "METHOD /path" (the query stripped), and
// what each call carried.
type fake struct {
	t     *testing.T
	mu    sync.Mutex
	calls []call
	route map[string]func(w http.ResponseWriter, r *http.Request, body []byte)
}

type call struct {
	method, path, query, auth string
	body                      []byte
}

func newFake(t *testing.T) (*fake, *httptest.Server) {
	f := &fake{t: t, route: map[string]func(http.ResponseWriter, *http.Request, []byte){}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		auth := r.Header.Get("Authorization")
		if auth == "" {
			auth = r.Header.Get("X-ND-Authorization")
		}
		f.mu.Lock()
		f.calls = append(f.calls, call{r.Method, r.URL.Path, r.URL.RawQuery, auth, body})
		h := f.route[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			http.Error(w, `{"message":"no route"}`, http.StatusNotFound)
			return
		}
		h(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fake) on(route string, h func(w http.ResponseWriter, r *http.Request, body []byte)) {
	f.route[route] = h
}

func (f *fake) json(route string, status int, v any) {
	f.on(route, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(v)
	})
}

// called is every call to a route.
func (f *fake) called(route string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.method+" "+c.path == route {
			out = append(out, c)
		}
	}
	return out
}

func bodyOf(t *testing.T, c call) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.body, &m); err != nil {
		t.Fatalf("body of %s %s is not JSON: %s", c.method, c.path, c.body)
	}
	return m
}

var emily = Person{Username: "emily", Password: "a-new-secret-9", Name: "Emily", Email: "emily@example.com"}

func run(t *testing.T, typ string, srv *httptest.Server, p Person, allowReset bool) (Result, error) {
	t.Helper()
	return Ensure(context.Background(), srv.Client(), typ,
		Admin{BaseURL: srv.URL + "/", Username: "admin", Password: "admin-pass"}, p, allowReset)
}

// ---- the shared rules -----------------------------------------------------

func TestTheAdminsOwnAccountIsNeverTheTarget(t *testing.T) {
	f, srv := newFake(t)
	_, err := run(t, "abs", srv, Person{Username: "Admin", Password: "x"}, true)
	if !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("got %v, want ErrAdminAccount", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("the service was called: %+v", f.calls)
	}
}

func TestUnknownServiceAndMissingFields(t *testing.T) {
	_, srv := newFake(t)
	if _, err := run(t, "plex", srv, emily, false); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("plex: %v", err)
	}
	if _, err := run(t, "abs", srv, Person{Username: "emily"}, false); err == nil {
		t.Fatal("no password was accepted")
	}
}

func TestEveryAdminServiceTypeHasAProvider(t *testing.T) {
	// repository.ServiceAdminTypes, by the app's names.
	for _, typ := range []string{"abs", "jellyfin", "kavita", "komga", "navidrome", "booklore", "storyteller", "stump", "calibreweb"} {
		p, ok := For(typ)
		if !ok {
			t.Errorf("%s has no provider", typ)
			continue
		}
		if c := p.Capability(); c.Type != typ || !c.Create {
			t.Errorf("%s capability: %+v", typ, c)
		}
	}
	if len(Capabilities()) != 9 {
		t.Fatalf("capabilities: %d", len(Capabilities()))
	}
}

func TestAServiceThatDoesNotAnswerIsUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err := Ensure(context.Background(), http.DefaultClient, "abs",
		Admin{BaseURL: srv.URL, Username: "admin", Password: "p"}, emily, false)
	var u *UnreachableError
	if !errors.As(err, &u) {
		t.Fatalf("got %v, want UnreachableError", err)
	}
}

// ---- Audiobookshelf -------------------------------------------------------

func absFake(t *testing.T, users []map[string]any) (*fake, *httptest.Server) {
	f, srv := newFake(t)
	f.on("POST /login", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		if !strings.Contains(string(body), `"admin-pass"`) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"accessToken": "jwt-1", "token": "legacy"}})
	})
	f.json("GET /api/users", 200, map[string]any{"users": users})
	f.json("POST /api/users", 200, map[string]any{"user": map[string]any{"id": "new"}})
	f.json("PATCH /api/users/u7", 200, map[string]any{"success": true})
	return f, srv
}

func TestABSCreatesAnActiveListenerAccount(t *testing.T) {
	f, srv := absFake(t, []map[string]any{{"id": "u1", "username": "root", "type": "root"}})
	res, err := run(t, "abs", srv, emily, false)
	if err != nil || res.Outcome != Created {
		t.Fatalf("%+v %v", res, err)
	}
	made := f.called("POST /api/users")
	if len(made) != 1 {
		t.Fatalf("create calls: %d", len(made))
	}
	b := bodyOf(t, made[0])
	if b["username"] != "emily" || b["password"] != emily.Password || b["type"] != "user" || b["isActive"] != true {
		t.Fatalf("body: %v", b)
	}
	perms := b["permissions"].(map[string]any)
	if perms["accessAllLibraries"] != true || perms["delete"] != false || perms["upload"] != false {
		t.Fatalf("permissions: %v", perms)
	}
	if made[0].auth != "Bearer jwt-1" {
		t.Fatalf("auth: %q", made[0].auth)
	}
}

func TestABSExistingAccountAsksThenResets(t *testing.T) {
	f, srv := absFake(t, []map[string]any{{"id": "u7", "username": "Emily", "type": "user"}})
	if _, err := run(t, "abs", srv, emily, false); !errors.Is(err, ErrExists) {
		t.Fatalf("without allowReset: %v", err)
	}
	if len(f.called("PATCH /api/users/u7")) != 0 {
		t.Fatal("reset without being allowed")
	}
	res, err := run(t, "abs", srv, emily, true)
	if err != nil || res.Outcome != Reset {
		t.Fatalf("%+v %v", res, err)
	}
	if b := bodyOf(t, f.called("PATCH /api/users/u7")[0]); b["password"] != emily.Password || len(b) != 1 {
		t.Fatalf("reset body: %v", b)
	}
}

func TestABSNeverTouchesAnAdmin(t *testing.T) {
	for _, typ := range []string{"admin", "root"} {
		f, srv := absFake(t, []map[string]any{{"id": "u7", "username": "emily", "type": typ}})
		if _, err := run(t, "abs", srv, emily, true); !errors.Is(err, ErrAdminAccount) {
			t.Fatalf("%s: %v", typ, err)
		}
		if len(f.called("PATCH /api/users/u7")) != 0 {
			t.Fatalf("%s was changed", typ)
		}
	}
}

func TestABSWrongAdminPasswordIsASignInError(t *testing.T) {
	_, srv := absFake(t, nil)
	_, err := Ensure(context.Background(), srv.Client(), "abs",
		Admin{BaseURL: srv.URL, Username: "admin", Password: "wrong"}, emily, false)
	var s *SignInError
	if !errors.As(err, &s) {
		t.Fatalf("got %v", err)
	}
}

// ---- Grimmory -------------------------------------------------------------

func grimmoryFake(t *testing.T, users []map[string]any) (*fake, *httptest.Server) {
	f, srv := newFake(t)
	f.json("POST /api/v1/auth/login", 200, map[string]any{"accessToken": "g-1", "refreshToken": "r"})
	f.json("GET /api/v1/users", 200, users)
	f.json("GET /api/v1/libraries", 200, []map[string]any{{"id": 3}, {"id": 5}})
	f.on("POST /api/v1/auth/register", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(204) })
	f.on("PUT /api/v1/users/change-user-password", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(200) })
	return f, srv
}

func TestGrimmoryCreatesWithEveryLibraryAndNeedsAnEmail(t *testing.T) {
	f, srv := grimmoryFake(t, nil)
	noEmail := emily
	noEmail.Email = ""
	if _, err := run(t, "booklore", srv, noEmail, false); !errors.Is(err, ErrEmailRequired) {
		t.Fatalf("no email: %v", err)
	}
	res, err := run(t, "booklore", srv, emily, false)
	if err != nil || res.Outcome != Created || res.Note == "" {
		t.Fatalf("%+v %v", res, err)
	}
	b := bodyOf(t, f.called("POST /api/v1/auth/register")[0])
	if b["name"] != "Emily" || b["email"] != emily.Email || b["permissionDownload"] != true {
		t.Fatalf("body: %v", b)
	}
	if libs, _ := json.Marshal(b["selectedLibraries"]); string(libs) != "[3,5]" {
		t.Fatalf("libraries: %s", libs)
	}
	if _, admin := b["permissionAdmin"]; admin {
		t.Fatal("admin permission sent")
	}
}

func TestGrimmoryResetsByIdAndSparesAdmins(t *testing.T) {
	f, srv := grimmoryFake(t, []map[string]any{
		{"id": 9, "username": "emily", "permissions": map[string]any{"admin": false}},
		{"id": 1, "username": "boss", "permissions": map[string]any{"admin": true}},
	})
	if _, err := run(t, "booklore", srv, emily, true); err != nil {
		t.Fatal(err)
	}
	b := bodyOf(t, f.called("PUT /api/v1/users/change-user-password")[0])
	if b["userId"] != float64(9) || b["newPassword"] != emily.Password {
		t.Fatalf("body: %v", b)
	}
	boss := Person{Username: "boss", Password: "x12345678"}
	if _, err := run(t, "booklore", srv, boss, true); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v", err)
	}
}

// ---- Jellyfin -------------------------------------------------------------

func jellyfinFake(t *testing.T, users []map[string]any) (*fake, *httptest.Server) {
	f, srv := newFake(t)
	f.json("POST /Users/AuthenticateByName", 200, map[string]any{"AccessToken": "jf-1"})
	f.json("GET /Users", 200, users)
	f.json("POST /Users/New", 200, map[string]any{"Id": "n1"})
	f.on("POST /Users/j7/Password", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(204) })
	f.on("POST /Sessions/Logout", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(204) })
	return f, srv
}

func TestJellyfinCreatesResetsAndSignsOut(t *testing.T) {
	f, srv := jellyfinFake(t, []map[string]any{{"Id": "a1", "Name": "admin", "Policy": map[string]any{"IsAdministrator": true}}})
	if _, err := run(t, "jellyfin", srv, emily, false); err != nil {
		t.Fatal(err)
	}
	made := f.called("POST /Users/New")
	if b := bodyOf(t, made[0]); b["Name"] != "emily" || b["Password"] != emily.Password {
		t.Fatalf("body: %v", b)
	}
	if !strings.Contains(made[0].auth, `Token="jf-1"`) || !strings.HasPrefix(made[0].auth, "MediaBrowser ") {
		t.Fatalf("auth: %q", made[0].auth)
	}
	if len(f.called("POST /Sessions/Logout")) != 1 {
		t.Fatal("the admin session was left open")
	}

	f2, srv2 := jellyfinFake(t, []map[string]any{{"Id": "j7", "Name": "Emily", "Policy": map[string]any{"IsAdministrator": false}}})
	if _, err := run(t, "jellyfin", srv2, emily, true); err != nil {
		t.Fatal(err)
	}
	if b := bodyOf(t, f2.called("POST /Users/j7/Password")[0]); b["NewPw"] != emily.Password || b["ResetPassword"] != false {
		t.Fatalf("body: %v", b)
	}
	_, srv3 := jellyfinFake(t, []map[string]any{{"Id": "j7", "Name": "emily", "Policy": map[string]any{"IsAdministrator": true}}})
	if _, err := run(t, "jellyfin", srv3, emily, true); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v", err)
	}
}

// ---- Navidrome ------------------------------------------------------------

func navidromeFake(t *testing.T, users []map[string]any, libraries bool) (*fake, *httptest.Server) {
	f, srv := newFake(t)
	f.json("POST /auth/login", 200, map[string]any{"token": "nd-1", "isAdmin": true})
	f.json("GET /api/user", 200, users)
	f.json("POST /api/user", 200, map[string]any{"id": "new-1"})
	f.json("GET /api/user/n7", 200, map[string]any{"id": "n7", "userName": "emily", "name": "Emily N", "email": "e@x.io", "isAdmin": false})
	f.json("PUT /api/user/n7", 200, map[string]any{"id": "n7"})
	if libraries {
		f.json("GET /api/library", 200, []map[string]any{{"id": 1}, {"id": 2}})
		f.on("PUT /api/user/new-1/library", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(200) })
	}
	return f, srv
}

func TestNavidromeCreatesWithEveryLibrary(t *testing.T) {
	f, srv := navidromeFake(t, nil, true)
	if _, err := run(t, "navidrome", srv, emily, false); err != nil {
		t.Fatal(err)
	}
	b := bodyOf(t, f.called("POST /api/user")[0])
	if b["userName"] != "emily" || b["isAdmin"] != false || b["password"] != emily.Password {
		t.Fatalf("body: %v", b)
	}
	if f.called("POST /api/user")[0].auth != "Bearer nd-1" {
		t.Fatal("no X-ND-Authorization")
	}
	libs := f.called("PUT /api/user/new-1/library")
	if len(libs) != 1 || string(libs[0].body) != `{"libraryIds":[1,2]}` {
		t.Fatalf("libraries: %+v", libs)
	}
}

func TestNavidromeBeforeMultipleLibrariesNeedsNone(t *testing.T) {
	_, srv := navidromeFake(t, nil, false)
	res, err := run(t, "navidrome", srv, emily, false)
	if err != nil || res.Outcome != Created || res.Note != "" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNavidromeResetSendsTheWholeUserBack(t *testing.T) {
	f, srv := navidromeFake(t, []map[string]any{{"id": "n7", "userName": "emily", "isAdmin": false}}, true)
	if _, err := run(t, "navidrome", srv, emily, true); err != nil {
		t.Fatal(err)
	}
	b := bodyOf(t, f.called("PUT /api/user/n7")[0])
	if b["password"] != emily.Password || b["name"] != "Emily N" || b["email"] != "e@x.io" || b["userName"] != "emily" {
		t.Fatalf("the edit lost fields: %v", b)
	}
}

// ---- Komga ----------------------------------------------------------------

func TestKomgaUsesEmailsAndBasicAuth(t *testing.T) {
	f, srv := newFake(t)
	f.json("GET /api/v2/users", 200, []map[string]any{{"id": "k1", "email": "boss@x.io", "roles": []string{"ADMIN", "USER"}}})
	f.json("POST /api/v2/users", 201, map[string]any{"id": "k2"})
	if _, err := run(t, "komga", srv, emily, false); !errors.Is(err, ErrEmailUsername) {
		t.Fatalf("plain username: %v", err)
	}
	p := emily
	p.Username = "emily@example.com"
	if _, err := run(t, "komga", srv, p, false); err != nil {
		t.Fatal(err)
	}
	made := f.called("POST /api/v2/users")[0]
	b := bodyOf(t, made)
	if b["email"] != "emily@example.com" {
		t.Fatalf("body: %v", b)
	}
	if roles, _ := json.Marshal(b["roles"]); string(roles) != `["FILE_DOWNLOAD","PAGE_STREAMING"]` {
		t.Fatalf("roles: %s", roles)
	}
	if !strings.HasPrefix(made.auth, "Basic ") {
		t.Fatalf("auth: %q", made.auth)
	}
	if _, err := run(t, "komga", srv, Person{Username: "BOSS@x.io", Password: "x"}, true); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v", err)
	}
}

func TestKomgaResetsThePassword(t *testing.T) {
	f, srv := newFake(t)
	f.json("GET /api/v2/users", 200, []map[string]any{{"id": "k9", "email": "emily@example.com", "roles": []string{"USER"}}})
	f.on("PATCH /api/v2/users/k9/password", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(204) })
	p := emily
	p.Username = "emily@example.com"
	if _, err := run(t, "komga", srv, p, true); err != nil {
		t.Fatal(err)
	}
	if b := bodyOf(t, f.called("PATCH /api/v2/users/k9/password")[0]); b["password"] != emily.Password {
		t.Fatalf("body: %v", b)
	}
}

// ---- Kavita ---------------------------------------------------------------

func TestKavitaInvitesThenAcceptsWithTheLoginRole(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/Account/login", 200, map[string]any{"token": "kv-1"})
	f.json("GET /api/Users", 200, []map[string]any{})
	f.json("GET /api/Library/libraries", 200, []map[string]any{{"id": 4}})
	f.json("POST /api/Account/invite", 200, map[string]any{
		"emailLink": "http://kavita/registration/confirm-email?token=abc%2Bdef%3D%3D&email=emily%40example.com",
		"emailSent": false,
	})
	f.json("POST /api/Account/confirm-email", 200, map[string]any{"username": "emily"})
	res, err := run(t, "kavita", srv, emily, false)
	if err != nil || res.Outcome != Created {
		t.Fatalf("%+v %v", res, err)
	}
	inv := bodyOf(t, f.called("POST /api/Account/invite")[0])
	roles, _ := json.Marshal(inv["roles"])
	if !strings.Contains(string(roles), `"Login"`) || strings.Contains(string(roles), "Admin") {
		t.Fatalf("roles: %s", roles)
	}
	if libs, _ := json.Marshal(inv["libraries"]); string(libs) != "[4]" {
		t.Fatalf("libraries: %s", libs)
	}
	conf := bodyOf(t, f.called("POST /api/Account/confirm-email")[0])
	if conf["token"] != "abc+def==" || conf["username"] != "emily" || conf["password"] != emily.Password {
		t.Fatalf("confirm: %v", conf)
	}
	if f.called("GET /api/Users")[0].query != "includePending=true" {
		t.Fatal("pending invites are not looked at")
	}
}

func TestKavitaResetLooksTheAccountUpFirst(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/Account/login", 200, map[string]any{"token": "kv-1"})
	f.json("GET /api/Users", 200, []map[string]any{
		{"id": 2, "username": "Emily", "roles": []string{"Pleb", "Login"}},
		{"id": 1, "username": "boss", "roles": []string{"Admin"}},
	})
	f.on("POST /api/Account/reset-password", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(200) })
	if _, err := run(t, "kavita", srv, emily, true); err != nil {
		t.Fatal(err)
	}
	b := bodyOf(t, f.called("POST /api/Account/reset-password")[0])
	if b["userName"] != "Emily" || b["password"] != emily.Password || b["oldPassword"] != "" {
		t.Fatalf("body: %v", b)
	}
	if _, err := run(t, "kavita", srv, Person{Username: "boss", Password: "x"}, true); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v", err)
	}
}

// ---- Stump ----------------------------------------------------------------

func TestStumpOverGraphQL(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/v2/auth/login", 200, map[string]any{"accessToken": "st-1", "forUser": map[string]any{}})
	var mu sync.Mutex
	var mutations []map[string]any
	f.on("POST /api/graphql", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		q := req["query"].(string)
		switch {
		case strings.Contains(q, "users("):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"users": map[string]any{"nodes": []map[string]any{
				{"id": "s1", "username": "owner", "isServerOwner": true, "permissions": []string{}},
				{"id": "s7", "username": "emily", "isServerOwner": false, "permissions": []string{"DOWNLOAD_FILE", "UPLOAD_FILE"},
					"maxSessionsAllowed": 3, "ageRestriction": map[string]any{"age": 13, "restrictOnUnset": true}},
			}}}})
		default:
			mu.Lock()
			mutations = append(mutations, req)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"ok": map[string]any{"id": "x"}}})
		}
	})
	if c := f.called("POST /api/v2/auth/login"); len(c) != 0 {
		t.Fatal("early call")
	}
	if _, err := run(t, "stump", srv, emily, true); err != nil {
		t.Fatal(err)
	}
	if q := f.called("POST /api/v2/auth/login")[0].query; !strings.Contains(q, "generate_token=true") {
		t.Fatalf("login query: %q", q)
	}
	upd := mutations[0]
	in := upd["variables"].(map[string]any)["input"].(map[string]any)
	if upd["variables"].(map[string]any)["id"] != "s7" || in["password"] != emily.Password {
		t.Fatalf("update: %v", upd)
	}
	if perms, _ := json.Marshal(in["permissions"]); string(perms) != `["DOWNLOAD_FILE","UPLOAD_FILE"]` {
		t.Fatalf("permissions not kept: %s", perms)
	}
	if ar := in["ageRestriction"].(map[string]any); ar["age"] != float64(13) || in["maxSessionsAllowed"] != float64(3) {
		t.Fatalf("restrictions not kept: %v", in)
	}
	if _, err := run(t, "stump", srv, Person{Username: "owner", Password: "x"}, true); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("owner: %v", err)
	}
	if _, err := run(t, "stump", srv, Person{Username: "newbie", Password: "x"}, false); err != nil {
		t.Fatal(err)
	}
	create := mutations[len(mutations)-1]
	if !strings.Contains(create["query"].(string), "createUser") {
		t.Fatalf("not a create: %v", create)
	}
}

func TestStumpGraphQLErrorsAreRefusals(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/v2/auth/login", 200, map[string]any{"accessToken": "st-1"})
	f.json("POST /api/graphql", 200, map[string]any{"errors": []map[string]any{{"message": "Forbidden: missing MANAGE_USERS"}}})
	_, err := run(t, "stump", srv, emily, false)
	var se *ServiceError
	if !errors.As(err, &se) || !strings.Contains(se.Message, "MANAGE_USERS") {
		t.Fatalf("got %v", err)
	}
}

// ---- Storyteller ----------------------------------------------------------

func TestStorytellerInvitesAndAccepts(t *testing.T) {
	f, srv := newFake(t)
	f.on("POST /api/v2/token", func(w http.ResponseWriter, r *http.Request, body []byte) {
		form, _ := url.ParseQuery(string(body))
		if form.Get("usernameOrEmail") != "admin" || form.Get("password") != "admin-pass" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "sty-1", "token_type": "bearer"})
	})
	f.json("GET /api/v2/users", 200, []map[string]any{{"id": "u1", "username": "boss", "email": "b@x.io", "permissions": map[string]any{"userCreate": true}}})
	f.json("POST /api/v2/invites", 200, map[string]any{"email": emily.Email, "key": "k123"})
	f.on("POST /api/v2/users", func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(204) })
	if _, err := run(t, "storyteller", srv, emily, false); err != nil {
		t.Fatal(err)
	}
	inv := bodyOf(t, f.called("POST /api/v2/invites")[0])
	if inv["bookRead"] != true || inv["userCreate"] != false || inv["email"] != emily.Email {
		t.Fatalf("invite: %v", inv)
	}
	acc := bodyOf(t, f.called("POST /api/v2/users")[0])
	if acc["inviteKey"] != "k123" || acc["password"] != emily.Password || acc["username"] != "emily" {
		t.Fatalf("accept: %v", acc)
	}
	if _, err := run(t, "storyteller", srv, Person{Username: "boss", Password: "x"}, true); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v", err)
	}
}

func TestStorytellerCannotResetSomeoneElse(t *testing.T) {
	f, srv := newFake(t)
	f.json("POST /api/v2/token", 200, map[string]any{"access_token": "sty-1"})
	f.json("GET /api/v2/users", 200, []map[string]any{{"id": "u2", "username": "emily", "permissions": map[string]any{"bookRead": true}}})
	if _, err := run(t, "storyteller", srv, emily, true); !errors.Is(err, ErrResetUnsupported) {
		t.Fatalf("got %v", err)
	}
	if len(f.called("POST /api/v2/invites")) != 0 {
		t.Fatal("invited an existing account")
	}
}

// ---- Calibre-Web ----------------------------------------------------------

const cwNewUserForm = `<form method="post">
<input type="hidden" name="csrf_token" value="tok-2">
<input type="text" name="name"><input type="email" name="email">
<select name="locale" id="locale"><option value="de">Deutsch</option><option value="en" selected>English</option></select>
<select name="default_language"><option value="all" selected>Show all</option><option value="eng">English</option></select>
<input type="checkbox" name="show_2" id="show_2" checked>
<input type="checkbox" name="show_4" id="show_4">
<input type="checkbox" name="admin_role" id="admin_role" checked>
<input type="checkbox" name="download_role" id="download_role" checked>
<input type="checkbox" name="upload_role" id="upload_role">
<input type="checkbox" name="viewer_role" id="viewer_role" checked="checked">
</form>`

func calibreWebFake(t *testing.T, existing []map[string]any) (*fake, *httptest.Server) {
	f, srv := newFake(t)
	var mu sync.Mutex
	rows := existing
	signedIn := false
	f.on("GET /login", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "anon"})
		_, _ = io.WriteString(w, `<form><input type="hidden" name="csrf_token" value="tok-1"></form>`)
	})
	f.on("POST /login", func(w http.ResponseWriter, r *http.Request, body []byte) {
		form, _ := url.ParseQuery(string(body))
		if form.Get("csrf_token") != "tok-1" || form.Get("password") != "admin-pass" {
			w.WriteHeader(200) // the form again, with an error
			return
		}
		signedIn = true
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "admin"})
		w.Header().Set("Location", "/")
		w.WriteHeader(302)
	})
	f.on("GET /ajax/listusers", func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if c, err := r.Cookie("session"); err != nil || c.Value != "admin" || !signedIn {
			w.Header().Set("Location", "/login")
			w.WriteHeader(302)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": rows})
	})
	f.on("GET /admin/user/new", func(w http.ResponseWriter, _ *http.Request, _ []byte) { _, _ = io.WriteString(w, cwNewUserForm) })
	f.on("POST /admin/user/new", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		form, _ := url.ParseQuery(string(body))
		if form.Get("csrf_token") != "tok-2" {
			_, _ = io.WriteString(w, `<div class="alert alert-danger">The CSRF token is missing.</div>`)
			return
		}
		mu.Lock()
		rows = append(rows, map[string]any{"id": 5, "name": form.Get("name"), "role": 2})
		mu.Unlock()
		w.Header().Set("Location", "/admin/admin")
		w.WriteHeader(302)
	})
	return f, srv
}

func TestCalibreWebCreatesThroughItsFormWithTheServersDefaults(t *testing.T) {
	f, srv := calibreWebFake(t, []map[string]any{{"id": 1, "name": "admin", "role": 1}})
	res, err := run(t, "calibreweb", srv, emily, false)
	if err != nil || res.Outcome != Created {
		t.Fatalf("%+v %v", res, err)
	}
	form, _ := url.ParseQuery(string(f.called("POST /admin/user/new")[0].body))
	if form.Get("name") != "emily" || form.Get("email") != emily.Email || form.Get("password") != emily.Password {
		t.Fatalf("form: %v", form)
	}
	if form.Get("locale") != "en" || form.Get("default_language") != "all" {
		t.Fatalf("defaults: %v", form)
	}
	if form.Get("show_2") != "on" || form.Has("show_4") || form.Get("download_role") != "on" || form.Get("viewer_role") != "on" {
		t.Fatalf("ticks: %v", form)
	}
	if form.Has("admin_role") || form.Has("upload_role") {
		t.Fatalf("admin (or an unticked role) sent: %v", form)
	}
}

func TestCalibreWebLeavesExistingAccountsAlone(t *testing.T) {
	f, srv := calibreWebFake(t, []map[string]any{{"id": 1, "name": "admin", "role": 1}, {"id": 2, "name": "Emily", "role": 2}})
	if _, err := run(t, "calibreweb", srv, emily, true); !errors.Is(err, ErrResetUnsupported) {
		t.Fatalf("got %v", err)
	}
	if len(f.called("POST /admin/user/new")) != 0 {
		t.Fatal("posted the form for an existing account")
	}
	_, srv2 := calibreWebFake(t, []map[string]any{{"id": 3, "name": "emily", "role": 1 | 2}})
	if _, err := run(t, "calibreweb", srv2, emily, true); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v", err)
	}
}

func TestCalibreWebWrongAdminPassword(t *testing.T) {
	_, srv := calibreWebFake(t, nil)
	_, err := Ensure(context.Background(), srv.Client(), "calibreweb",
		Admin{BaseURL: srv.URL, Username: "admin", Password: "nope"}, emily, false)
	var s *SignInError
	if !errors.As(err, &s) {
		t.Fatalf("got %v", err)
	}
}

// ---- messages -------------------------------------------------------------

func TestServiceMessagesAreShortAndPlain(t *testing.T) {
	cases := map[string]string{
		`{"message":"Username already taken"}`:            "Username already taken",
		`[{"code":"PasswordTooShort","description":"x"}]`: "x",
		`Username already taken`:                          "Username already taken",
		`<html><body>500</body></html>`:                   "",
		`{"errors":{"password":["too short"]}}`:           "too short",
	}
	for in, want := range cases {
		if got := serviceMessage([]byte(in)); got != want {
			t.Errorf("%s → %q, want %q", in, got, want)
		}
	}
}
