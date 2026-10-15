package provision

// Change access: an existing account's libraries and downloads, changed
// and read back. Every fake below changes its account the way that
// service's own update does (each read from its source, 2026-10-14), so a
// read-back proves the change, and a call that leaves a field out shows up
// as the field lost.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
)

func adminOf(srv *httptest.Server) Admin {
	return Admin{BaseURL: srv.URL + "/", Username: "admin", Password: "admin-pass"}
}

func readAccess(t *testing.T, typ string, srv *httptest.Server, username string) AccessState {
	t.Helper()
	st, err := GetAccess(context.Background(), srv.Client(), typ, adminOf(srv), username)
	if err != nil {
		t.Fatalf("reading %s on %s: %v", username, typ, err)
	}
	return st
}

func changeAccess(srv *httptest.Server, typ, username string, want Access) (AccessState, error) {
	return ChangeAccess(context.Background(), srv.Client(), typ, adminOf(srv), username, want)
}

func wantAccess(t *testing.T, got AccessState, all bool, libraries []string, download bool) {
	t.Helper()
	if got.AllLibraries != all || !reflect.DeepEqual(got.Libraries, libraries) || got.Download != download {
		t.Fatalf("access: all %v, libraries %v, download %v; want all %v, libraries %v, download %v",
			got.AllLibraries, got.Libraries, got.Download, all, libraries, download)
	}
}

func decoded(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("not JSON: %s", body)
	}
	return m
}

func noChanges(t *testing.T, f *fake) {
	t.Helper()
	for _, c := range f.calls {
		if c.method != http.MethodGet && !strings.Contains(c.path, "login") && !strings.Contains(c.path, "Logout") &&
			!strings.Contains(c.path, "Authenticate") && !strings.Contains(c.path, "/token") && c.path != "/api/graphql" {
			t.Fatalf("something was changed: %s %s %s", c.method, c.path, c.body)
		}
	}
}

// ---- the shared rules -------------------------------------------------------

func TestChangeAccessNeverTouchesTheAdminsOwnSignIn(t *testing.T) {
	f, srv := newFake(t)
	if _, err := changeAccess(srv, "abs", " ADMIN ", Access{AllLibraries: true}); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("change: %v", err)
	}
	if _, err := GetAccess(context.Background(), srv.Client(), "abs", adminOf(srv), "admin"); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("read: %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("the service was called: %+v", f.calls)
	}
}

func TestChangeAccessNeedsALibraryUnlessItIsEveryOne(t *testing.T) {
	f, srv := newFake(t)
	if _, err := changeAccess(srv, "abs", "kate", Access{Libraries: []string{" ", ""}, Download: true}); !errors.Is(err, ErrNoLibraries) {
		t.Fatalf("got %v", err)
	}
	if len(f.calls) != 0 {
		t.Fatalf("the service was called: %+v", f.calls)
	}
}

func TestEveryServiceButCalibreWebCanChangeAccess(t *testing.T) {
	for _, c := range Capabilities() {
		if (c.Access == nil) != (c.Type == "calibreweb") {
			t.Errorf("%s: access %+v", c.Type, c.Access)
		}
	}
	_, srv := newFake(t)
	if _, err := changeAccess(srv, "calibreweb", "kate", Access{AllLibraries: true}); !errors.Is(err, ErrAccessUnsupported) {
		t.Fatalf("calibreweb: %v", err)
	}
	if _, err := changeAccess(srv, "plex", "kate", Access{AllLibraries: true}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("plex: %v", err)
	}
	// What the page reads carries no internals.
	b, _ := json.Marshal(Capabilities())
	if strings.Contains(string(b), "allFlag") {
		t.Fatalf("capabilities: %s", b)
	}
}

// ---- Audiobookshelf ---------------------------------------------------------

var absLibs = []string{"lib-a", "lib-b", "lib-c"}

// absAccessFake is Audiobookshelf's update: PATCH merges the boolean
// permissions it names into the account's, and takes librariesAccessible
// only alongside a permissions object. mode "ignore" says yes to everything
// and changes nothing; "sneak" also gives the account the right to update
// items, which nobody asked for.
func absAccessFake(t *testing.T, mode string) (*fake, *httptest.Server, func() map[string]any) {
	f, srv := newFake(t)
	var mu sync.Mutex
	kate := map[string]any{"id": "u7", "username": "Kate", "type": "user", "permissions": map[string]any{
		"download": true, "update": false, "delete": false, "upload": false, "createEreader": false,
		"accessAllLibraries": true, "accessAllTags": true, "accessExplicitContent": true,
	}, "librariesAccessible": []any{}}
	users := []map[string]any{
		{"id": "r1", "username": "root", "type": "root", "permissions": map[string]any{"download": true}},
		{"id": "a2", "username": "boss", "type": "admin", "permissions": map[string]any{"download": true}},
		kate,
	}
	f.json("POST /login", 200, map[string]any{"user": map[string]any{"accessToken": "jwt-1"}})
	f.on("GET /api/users", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"users": users})
	})
	f.json("GET /api/libraries", 200, map[string]any{"libraries": []map[string]any{
		{"id": "lib-a", "name": "Audiobooks"}, {"id": "lib-b", "name": "Podcasts"}, {"id": "lib-c", "name": "Kids"}}})
	f.on("PATCH /api/users/u7", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		mu.Lock()
		defer mu.Unlock()
		if perms, ok := in["permissions"].(map[string]any); ok && mode != "ignore" {
			cur := kate["permissions"].(map[string]any)
			for k, v := range perms {
				if b, isBool := v.(bool); isBool && cur[k] != nil {
					cur[k] = b
				}
			}
			if mode == "sneak" {
				cur["update"] = true
			}
			if libs, ok := in["librariesAccessible"].([]any); ok {
				kate["librariesAccessible"] = libs
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})
	return f, srv, func() map[string]any { mu.Lock(); defer mu.Unlock(); return kate }
}

func TestABSChangesLibrariesAndDownloadsAndNothingElse(t *testing.T) {
	f, srv, kate := absAccessFake(t, "")
	wantAccess(t, readAccess(t, "abs", srv, "kate"), true, absLibs, true)

	got, err := changeAccess(srv, "abs", "kate", Access{Libraries: []string{"lib-b", "gone"}, Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{"lib-b"}, false)
	patches := f.called("PATCH /api/users/u7")
	if len(patches) != 1 || !reflect.DeepEqual(decoded(t, patches[0].body), map[string]any{
		"permissions":         map[string]any{"download": false, "accessAllLibraries": false},
		"librariesAccessible": []any{"lib-b"},
	}) {
		t.Fatalf("sent %s", patches[0].body)
	}
	perms := kate()["permissions"].(map[string]any)
	if perms["accessExplicitContent"] != true || perms["update"] != false || perms["accessAllTags"] != true {
		t.Fatalf("other permissions moved: %v", perms)
	}

	got, err = changeAccess(srv, "abs", "kate", Access{AllLibraries: true, Download: true})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, true, absLibs, true)
	if libs := kate()["librariesAccessible"].([]any); len(libs) != 0 {
		t.Fatalf("the old list was kept: %v", libs)
	}
}

func TestABSNeverChangesRootAnAdminOrAMissingAccount(t *testing.T) {
	f, srv, _ := absAccessFake(t, "")
	for _, name := range []string{"root", "boss"} {
		if _, err := changeAccess(srv, "abs", name, Access{AllLibraries: true}); !errors.Is(err, ErrAdminAccount) {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := GetAccess(context.Background(), srv.Client(), "abs", adminOf(srv), name); !errors.Is(err, ErrAdminAccount) {
			t.Fatalf("read %s: %v", name, err)
		}
	}
	if _, err := changeAccess(srv, "abs", "nobody", Access{AllLibraries: true}); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("nobody: %v", err)
	}
	if _, err := changeAccess(srv, "abs", "kate", Access{Libraries: []string{"gone"}}); !errors.Is(err, ErrLibrariesGone) {
		t.Fatalf("gone: %v", err)
	}
	noChanges(t, f)
}

func TestAYesThatChangedNothingIsNotReportedDone(t *testing.T) {
	_, srv, _ := absAccessFake(t, "ignore")
	got, err := changeAccess(srv, "abs", "kate", Access{Libraries: []string{"lib-a"}, Download: false})
	var nt *NotTakenError
	if !errors.As(err, &nt) || !reflect.DeepEqual(nt.Parts, []string{"downloads", "libraries"}) {
		t.Fatalf("got %v", err)
	}
	// What it has is what comes back.
	wantAccess(t, got, true, absLibs, true)
}

func TestAServiceThatChangesMoreThanWasAskedIsCaught(t *testing.T) {
	_, srv, _ := absAccessFake(t, "sneak")
	got, err := changeAccess(srv, "abs", "kate", Access{AllLibraries: true, Download: false})
	var ac *AlsoChangedError
	if !errors.As(err, &ac) || !reflect.DeepEqual(ac.Fields, []string{"permissions.update"}) {
		t.Fatalf("got %v", err)
	}
	// The change itself took, and is what comes back.
	wantAccess(t, got, true, absLibs, false)
}

// ---- Grimmory ---------------------------------------------------------------

// grimmoryAccessFake is Grimmory's update: PUT sets the name and the email
// as sent, every permission the request knows (a missing one false) and
// the libraries (at least one). demoUser is in the answer only; sent back,
// it is refused here, as a strict reader would.
func grimmoryAccessFake(t *testing.T) (*fake, *httptest.Server, func() map[string]any) {
	f, srv := newFake(t)
	var mu sync.Mutex
	known := []string{"admin", "canUpload", "canDownload", "canSyncKobo", "canAccessOpds"}
	kate := map[string]any{"id": 5, "username": "kate", "name": "Kate", "email": "kate@example.com",
		"permissions": map[string]any{"admin": false, "canUpload": true, "canDownload": true, "canSyncKobo": true,
			"canAccessOpds": false, "demoUser": false},
		"assignedLibraries": []any{map[string]any{"id": 1, "name": "Books"}, map[string]any{"id": 2, "name": "Comics"}}}
	boss := map[string]any{"id": 1, "username": "boss", "permissions": map[string]any{"admin": true}}
	f.json("POST /api/v1/auth/login", 200, map[string]any{"accessToken": "g-1"})
	f.on("GET /api/v1/users", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode([]any{boss, kate})
	})
	f.json("GET /api/v1/libraries", 200, []map[string]any{{"id": 1, "name": "Books"}, {"id": 2, "name": "Comics"}, {"id": 3, "name": "Papers"}})
	f.on("PUT /api/v1/users/5", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var in struct {
			Name              any            `json:"name"`
			Email             any            `json:"email"`
			Permissions       map[string]any `json:"permissions"`
			AssignedLibraries *[]float64     `json:"assignedLibraries"`
		}
		_ = json.Unmarshal(body, &in)
		if _, bad := in.Permissions["demoUser"]; bad {
			http.Error(w, `{"message":"Unrecognized field \"demoUser\""}`, http.StatusBadRequest)
			return
		}
		if in.AssignedLibraries == nil || len(*in.AssignedLibraries) == 0 {
			http.Error(w, `{"message":"At least one library must be assigned."}`, http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		kate["name"], kate["email"] = in.Name, in.Email
		perms := kate["permissions"].(map[string]any)
		for _, k := range known {
			v, _ := in.Permissions[k].(bool)
			perms[k] = v
		}
		libs := []any{}
		for _, id := range *in.AssignedLibraries {
			libs = append(libs, map[string]any{"id": id})
		}
		kate["assignedLibraries"] = libs
		_ = json.NewEncoder(w).Encode(kate)
	})
	return f, srv, func() map[string]any { mu.Lock(); defer mu.Unlock(); return kate }
}

func TestGrimmorySendsTheWholeAccountBackWithTheTwoChanged(t *testing.T) {
	_, srv, kate := grimmoryAccessFake(t)
	wantAccess(t, readAccess(t, "booklore", srv, "kate"), false, []string{"1", "2"}, true)

	got, err := changeAccess(srv, "booklore", "kate", Access{Libraries: []string{"3"}, Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{"3"}, false)
	k := kate()
	if k["name"] != "Kate" || k["email"] != "kate@example.com" {
		t.Fatalf("name or email lost: %v", k)
	}
	if perms := k["permissions"].(map[string]any); perms["canUpload"] != true || perms["canSyncKobo"] != true ||
		perms["canAccessOpds"] != false || perms["admin"] != false {
		t.Fatalf("other permissions moved: %v", perms)
	}

	// Every library there is now: Grimmory has no setting for "every one".
	got, err = changeAccess(srv, "booklore", "kate", Access{AllLibraries: true, Download: true})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, true, []string{"1", "2", "3"}, true)

	if _, err := changeAccess(srv, "booklore", "boss", Access{AllLibraries: true}); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("boss: %v", err)
	}
}

// ---- Jellyfin ---------------------------------------------------------------

const (
	jfMovies = "aaaa1111bbbb2222cccc3333dddd4444"
	jfMusic  = "eeee5555ffff6666aaaa7777bbbb8888"
)

func dashed(id string) string {
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

// jellyfinAccessFake is Jellyfin's policy update: the whole policy,
// replaced by what is sent (refused without its provider ids). It writes
// the account's folders with dashes, as it writes them in that answer.
func jellyfinAccessFake(t *testing.T) (*fake, *httptest.Server, func() map[string]any) {
	f, srv := newFake(t)
	var mu sync.Mutex
	policy := map[string]any{"IsAdministrator": false, "EnableContentDownloading": true, "EnableAllFolders": true,
		"EnabledFolders": []any{}, "MaxActiveSessions": 3, "EnableRemoteAccess": true,
		"AuthenticationProviderId": "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider",
		"PasswordResetProviderId":  "Jellyfin.Server.Implementations.Users.DefaultPasswordResetProvider"}
	f.json("POST /Users/AuthenticateByName", 200, map[string]any{"AccessToken": "jf-1"})
	f.on("POST /Sessions/Logout", noContent)
	f.json("GET /Users", 200, []map[string]any{
		{"Id": "a1", "Name": "admin", "Policy": map[string]any{"IsAdministrator": true}},
		{"Id": "j7", "Name": "kate", "Policy": map[string]any{"IsAdministrator": false}},
	})
	f.on("GET /Users/j7", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]any{}
		for k, v := range policy {
			out[k] = v
		}
		folders := []any{}
		for _, id := range policy["EnabledFolders"].([]any) {
			folders = append(folders, dashed(jellyfinID(id.(string))))
		}
		out["EnabledFolders"] = folders
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "j7", "Name": "kate", "Policy": out})
	})
	f.json("GET /Library/MediaFolders", 200, map[string]any{"Items": []map[string]any{
		{"Id": jfMovies, "Name": "Movies"}, {"Id": jfMusic, "Name": "Music"}}})
	f.on("POST /Users/j7/Policy", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		if s, _ := in["AuthenticationProviderId"].(string); s == "" {
			http.Error(w, `{"title":"Auth provider is required"}`, http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		policy = in
		w.WriteHeader(http.StatusNoContent)
	})
	return f, srv, func() map[string]any { mu.Lock(); defer mu.Unlock(); return policy }
}

func TestJellyfinSendsThePolicyBackWholeWithTheTwoChanged(t *testing.T) {
	f, srv, policy := jellyfinAccessFake(t)
	wantAccess(t, readAccess(t, "jellyfin", srv, "kate"), true, []string{jfMovies, jfMusic}, true)

	got, err := changeAccess(srv, "jellyfin", "kate", Access{Libraries: []string{jfMusic}, Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{jfMusic}, false)
	p := policy()
	if p["MaxActiveSessions"] != float64(3) || p["EnableRemoteAccess"] != true || p["PasswordResetProviderId"] == nil {
		t.Fatalf("the rest of the policy moved: %v", p)
	}
	if p["EnableAllFolders"] != false || !reflect.DeepEqual(p["EnabledFolders"], []any{jfMusic}) {
		t.Fatalf("folders: %v %v", p["EnableAllFolders"], p["EnabledFolders"])
	}
	// An id written with dashes is the same library.
	if got, err = changeAccess(srv, "jellyfin", "kate", Access{Libraries: []string{dashed(jfMovies)}, Download: true}); err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{jfMovies}, true)
	// Every sign-in signed out again.
	if signIns, outs := len(f.called("POST /Users/AuthenticateByName")), len(f.called("POST /Sessions/Logout")); signIns != outs {
		t.Fatalf("%d sign-ins, %d sign-outs", signIns, outs)
	}
	if _, err := changeAccess(srv, "jellyfin", "ADMIN", Access{AllLibraries: true}); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("admin: %v", err)
	}
}

// ---- Komga ------------------------------------------------------------------

// komgaAccessFake is Komga's PATCH: only the fields sent change; roles it
// does not know are dropped; "every library" empties the list. Each answer
// adds the pseudo-role USER, as Komga's does.
func komgaAccessFake(t *testing.T) (*fake, *httptest.Server, func() map[string]any) {
	f, srv := newFake(t)
	var mu sync.Mutex
	kate := map[string]any{"id": "k7", "email": "kate@example.com", "roles": []any{"FILE_DOWNLOAD", "PAGE_STREAMING", "KOREADER_SYNC"},
		"sharedAllLibraries": true, "sharedLibrariesIds": []any{}}
	boss := map[string]any{"id": "k1", "email": "boss@example.com", "roles": []any{"ADMIN"}, "sharedAllLibraries": true, "sharedLibrariesIds": []any{}}
	withUser := func(u map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range u {
			out[k] = v
		}
		out["roles"] = append(append([]any{}, u["roles"].([]any)...), "USER")
		return out
	}
	f.on("GET /api/v2/users", func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if u, p, _ := r.BasicAuth(); u != "admin" || p != "admin-pass" {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode([]any{withUser(boss), withUser(kate)})
	})
	f.json("GET /api/v1/libraries", 200, []map[string]any{{"id": "L1", "name": "Manga"}, {"id": "L2", "name": "Comics"}})
	f.on("PATCH /api/v2/users/k7", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		mu.Lock()
		defer mu.Unlock()
		if roles, ok := in["roles"].([]any); ok {
			keep := []any{}
			for _, r := range roles {
				if slices.Contains([]string{"ADMIN", "FILE_DOWNLOAD", "PAGE_STREAMING", "KOBO_SYNC", "KOREADER_SYNC"}, r.(string)) {
					keep = append(keep, r)
				}
			}
			kate["roles"] = keep
		}
		if shared, ok := in["sharedLibraries"].(map[string]any); ok {
			kate["sharedAllLibraries"] = shared["all"]
			kate["sharedLibrariesIds"] = []any{}
			if shared["all"] == false {
				kate["sharedLibrariesIds"] = shared["libraryIds"]
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return f, srv, func() map[string]any { mu.Lock(); defer mu.Unlock(); return kate }
}

func TestKomgaChangesRolesAndSharingOnly(t *testing.T) {
	f, srv, kate := komgaAccessFake(t)
	wantAccess(t, readAccess(t, "komga", srv, "Kate@Example.com"), true, []string{"L1", "L2"}, true)

	got, err := changeAccess(srv, "komga", "kate@example.com", Access{Libraries: []string{"L2"}, Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{"L2"}, false)
	if roles := kate()["roles"]; !reflect.DeepEqual(roles, []any{"PAGE_STREAMING", "KOREADER_SYNC"}) {
		t.Fatalf("roles: %v", roles)
	}
	sent := decoded(t, f.called("PATCH /api/v2/users/k7")[0].body)
	if len(sent) != 2 || slices.Contains(sent["roles"].([]any), any("USER")) {
		t.Fatalf("sent %v", sent)
	}
	if _, err := changeAccess(srv, "komga", "boss@example.com", Access{AllLibraries: true}); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("boss: %v", err)
	}
}

// ---- Kavita -----------------------------------------------------------------

// kavitaAccessFake is Kavita's account update: a different identity
// provider is the ONLY thing it applies; otherwise roles (Pleb added) and
// libraries are replaced and the age limit, required, set as sent.
func kavitaAccessFake(t *testing.T) (*fake, *httptest.Server, func() map[string]any) {
	f, srv := newFake(t)
	var mu sync.Mutex
	names := map[float64]string{1: "Manga", 2: "Books", 3: "Comics"}
	kate := map[string]any{"id": 11, "username": "kate", "email": "kate@example.com",
		"roles":          []any{"Login", "Download", "Change Password", "Pleb"},
		"libraries":      []any{map[string]any{"id": 1, "name": "Manga"}, map[string]any{"id": 2, "name": "Books"}},
		"ageRestriction": map[string]any{"ageRating": 3, "includeUnknowns": false}, "identityProvider": 1}
	boss := map[string]any{"id": 1, "username": "boss", "roles": []any{"Admin", "Login"}}
	f.json("POST /api/Account/login", 200, map[string]any{"token": "kv-1"})
	f.on("GET /api/Users", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode([]any{boss, kate})
	})
	f.json("GET /api/Library/libraries", 200, []map[string]any{{"id": 1, "name": "Manga"}, {"id": 2, "name": "Books"}, {"id": 3, "name": "Comics"}})
	f.on("POST /api/Account/update", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		mu.Lock()
		defer mu.Unlock()
		provider, _ := in["identityProvider"].(float64)
		if provider != float64(kate["identityProvider"].(int)) {
			kate["identityProvider"] = int(provider)
			return
		}
		age, ok := in["ageRestriction"].(map[string]any)
		if !ok {
			http.Error(w, "ageRestriction is required", http.StatusBadRequest)
			return
		}
		roles := in["roles"].([]any)
		if !slices.Contains(roles, any("Pleb")) {
			roles = append(roles, "Pleb")
		}
		libs := []any{}
		for _, id := range in["libraries"].([]any) {
			libs = append(libs, map[string]any{"id": id, "name": names[id.(float64)]})
		}
		kate["roles"], kate["libraries"], kate["ageRestriction"] = roles, libs, age
		kate["username"], kate["email"] = in["username"], in["email"]
	})
	return f, srv, func() map[string]any { mu.Lock(); defer mu.Unlock(); return kate }
}

func TestKavitaSendsTheMemberBackWithTheTwoChanged(t *testing.T) {
	_, srv, kate := kavitaAccessFake(t)
	wantAccess(t, readAccess(t, "kavita", srv, "kate"), false, []string{"1", "2"}, true)

	got, err := changeAccess(srv, "kavita", "kate", Access{Libraries: []string{"3"}, Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{"3"}, false)
	k := kate()
	if !reflect.DeepEqual(k["roles"], []any{"Login", "Change Password", "Pleb"}) {
		t.Fatalf("roles: %v", k["roles"])
	}
	if !reflect.DeepEqual(k["ageRestriction"], map[string]any{"ageRating": float64(3), "includeUnknowns": false}) ||
		k["identityProvider"] != 1 || k["email"] != "kate@example.com" || k["username"] != "kate" {
		t.Fatalf("the rest of the member moved: %v", k)
	}
	if _, err := changeAccess(srv, "kavita", "boss", Access{AllLibraries: true}); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("boss: %v", err)
	}
}

// ---- Navidrome --------------------------------------------------------------

func navidromeAccessFake(t *testing.T, multiple bool) (*fake, *httptest.Server) {
	f, srv := newFake(t)
	var mu sync.Mutex
	has := []any{map[string]any{"id": 1, "name": "Music"}, map[string]any{"id": 2, "name": "Audiobooks"}}
	f.json("POST /auth/login", 200, map[string]any{"token": "nd-1"})
	f.json("GET /api/user", 200, []map[string]any{{"id": "n0", "userName": "admin", "isAdmin": true}, {"id": "n7", "userName": "kate"}})
	if !multiple {
		return f, srv // one library: no /api/library (404)
	}
	f.json("GET /api/library", 200, has)
	f.on("GET /api/user/n7/library", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewEncoder(w).Encode(has)
	})
	f.on("PUT /api/user/n7/library", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var in struct {
			LibraryIDs []int `json:"libraryIds"`
		}
		_ = json.Unmarshal(body, &in)
		if len(in.LibraryIDs) == 0 {
			http.Error(w, "at least one library must be assigned to non-admin users", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		has = []any{}
		for _, id := range in.LibraryIDs {
			has = append(has, map[string]any{"id": id})
		}
		_ = json.NewEncoder(w).Encode(has)
	})
	return f, srv
}

func TestNavidromeChangesLibrariesAndNeverOffersDownloads(t *testing.T) {
	_, srv := navidromeAccessFake(t, true)
	if can, _ := AccessCapabilityOf("navidrome"); can.Download || !can.Libraries {
		t.Fatalf("capability: %+v", can)
	}
	wantAccess(t, readAccess(t, "navidrome", srv, "kate"), true, []string{"1", "2"}, false)
	got, err := changeAccess(srv, "navidrome", "kate", Access{Libraries: []string{"2"}, Download: true})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{"2"}, false)
}

func TestNavidromeWithOneLibraryHasNothingToChange(t *testing.T) {
	f, srv := navidromeAccessFake(t, false)
	st := readAccess(t, "navidrome", srv, "kate")
	if !st.AllLibraries || len(st.Available) != 0 {
		t.Fatalf("access: %+v", st)
	}
	if _, err := changeAccess(srv, "navidrome", "kate", Access{AllLibraries: true}); !errors.Is(err, ErrNothingToChange) {
		t.Fatalf("got %v", err)
	}
	noChanges(t, f)
}

// ---- Stump ------------------------------------------------------------------

// stumpAccessFake is Stump's GraphQL: updateUser replaces the permission
// list, deletes the age limit when none is sent and sets the session cap
// as sent; each library's list of who is kept out is replaced whole.
func stumpAccessFake(t *testing.T) (*fake, *httptest.Server, func() (map[string]any, map[string][]string, []map[string]any)) {
	f, srv := newFake(t)
	var mu sync.Mutex
	kate := map[string]any{"id": "s7", "username": "kate", "isServerOwner": false,
		"permissions": []any{"DOWNLOAD_FILE", "CHANGE_PASSWORD"}, "maxSessionsAllowed": 2,
		"ageRestriction": map[string]any{"age": 13, "restrictOnUnset": true}}
	owner := map[string]any{"id": "s1", "username": "owner", "isServerOwner": true, "permissions": []any{}}
	out := map[string][]string{"L1": {"s9"}, "L2": {}, "L3": {"s7"}}
	var updates []map[string]any
	f.json("POST /api/v2/auth/login", 200, map[string]any{"accessToken": "st-1"})
	f.on("POST /api/graphql", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		mu.Lock()
		defer mu.Unlock()
		data := map[string]any{}
		switch {
		case strings.Contains(req.Query, "updateUser("):
			in := req.Variables["input"].(map[string]any)
			updates = append(updates, in)
			kate["permissions"] = in["permissions"]
			kate["maxSessionsAllowed"] = in["maxSessionsAllowed"]
			kate["ageRestriction"] = in["ageRestriction"]
			data["updateUser"] = map[string]any{"id": "s7"}
		case strings.Contains(req.Query, "updateLibraryExcludedUsers("):
			ids := []string{}
			for _, id := range req.Variables["userIds"].([]any) {
				ids = append(ids, id.(string))
			}
			out[req.Variables["id"].(string)] = ids
			data["updateLibraryExcludedUsers"] = map[string]any{"id": req.Variables["id"]}
		case strings.Contains(req.Query, "users("):
			data["users"] = map[string]any{"nodes": []any{owner, kate}}
		case strings.Contains(req.Query, "libraries("):
			nodes := []any{}
			for _, id := range []string{"L1", "L2", "L3"} {
				ex := []any{}
				for _, u := range out[id] {
					ex = append(ex, map[string]any{"id": u})
				}
				nodes = append(nodes, map[string]any{"id": id, "name": "Library " + id, "excludedUsers": ex})
			}
			data["libraries"] = map[string]any{"nodes": nodes}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})
	return f, srv, func() (map[string]any, map[string][]string, []map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		return kate, out, updates
	}
}

func TestStumpChangesDownloadsAndWhoIsKeptOutAndKeepsThePassword(t *testing.T) {
	_, srv, state := stumpAccessFake(t)
	wantAccess(t, readAccess(t, "stump", srv, "kate"), false, []string{"L1", "L2"}, true)

	got, err := changeAccess(srv, "stump", "kate", Access{Libraries: []string{"L3"}, Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, false, []string{"L3"}, false)
	kate, out, updates := state()
	if len(updates) != 1 {
		t.Fatalf("updates: %v", updates)
	}
	if _, sent := updates[0]["password"]; sent {
		t.Fatalf("a password was sent: %v", updates[0])
	}
	if !reflect.DeepEqual(kate["permissions"], []any{"CHANGE_PASSWORD"}) || kate["maxSessionsAllowed"] != float64(2) ||
		!reflect.DeepEqual(kate["ageRestriction"], map[string]any{"age": float64(13), "restrictOnUnset": true}) {
		t.Fatalf("the rest of the account moved: %v", kate)
	}
	if !reflect.DeepEqual(out, map[string][]string{"L1": {"s9", "s7"}, "L2": {"s7"}, "L3": {}}) {
		t.Fatalf("kept out: %v", out)
	}

	// Every library: kept out of none, someone else still kept out of L1.
	got, err = changeAccess(srv, "stump", "kate", Access{AllLibraries: true, Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, true, []string{"L1", "L2", "L3"}, false)
	if _, out, updates = state(); len(updates) != 1 || !reflect.DeepEqual(out, map[string][]string{"L1": {"s9"}, "L2": {}, "L3": {}}) {
		t.Fatalf("kept out %v after %d updates", out, len(updates))
	}
	if _, err := changeAccess(srv, "stump", "owner", Access{AllLibraries: true}); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("owner: %v", err)
	}
}

// ---- Storyteller ------------------------------------------------------------

// storytellerAccessFake is Storyteller's update: every flag set from what
// is sent (a missing one false), kept as SQLite's 1 and 0. drop leaves a
// flag out of the answer; reset is a flag every update turns off.
func storytellerAccessFake(t *testing.T, drop, reset string) (*fake, *httptest.Server, func() map[string]any) {
	f, srv := newFake(t)
	var mu sync.Mutex
	perms := map[string]any{}
	for _, k := range storytellerFlags {
		perms[k] = 0
	}
	for _, k := range []string{"bookRead", "bookList", "bookDownload", "collectionCreate"} {
		perms[k] = 1
	}
	f.json("POST /api/v2/token", 200, map[string]any{"access_token": "st-1"})
	f.on("GET /api/v2/users", func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		mu.Lock()
		defer mu.Unlock()
		shown := map[string]any{}
		for k, v := range perms {
			if k != drop {
				shown[k] = v
			}
		}
		_ = json.NewEncoder(w).Encode([]any{
			map[string]any{"id": "s1", "username": "boss", "email": "boss@example.com", "permissions": map[string]any{"userCreate": 1}},
			map[string]any{"id": "s7", "username": "kate", "email": "kate@example.com", "permissions": shown},
		})
	})
	f.on("PUT /api/v2/users/s7", func(w http.ResponseWriter, _ *http.Request, body []byte) {
		var in struct {
			Permissions map[string]any `json:"permissions"`
		}
		_ = json.Unmarshal(body, &in)
		mu.Lock()
		defer mu.Unlock()
		for _, k := range storytellerFlags {
			perms[k] = 0
			if truthy(in.Permissions[k]) && k != reset {
				perms[k] = 1
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return f, srv, func() map[string]any { mu.Lock(); defer mu.Unlock(); return perms }
}

func TestStorytellerSendsEveryFlagBackWithDownloadsChanged(t *testing.T) {
	_, srv, perms := storytellerAccessFake(t, "", "")
	wantAccess(t, readAccess(t, "storyteller", srv, "KATE@example.com"), true, []string{}, true)

	got, err := changeAccess(srv, "storyteller", "kate", Access{Download: false})
	if err != nil {
		t.Fatal(err)
	}
	wantAccess(t, got, true, []string{}, false)
	if p := perms(); p["bookDownload"] != 0 || p["collectionCreate"] != 1 || p["bookRead"] != 1 || p["bookList"] != 1 {
		t.Fatalf("flags: %v", p)
	}
	if _, err := changeAccess(srv, "storyteller", "boss", Access{Download: true}); !errors.Is(err, ErrAdminAccount) {
		t.Fatalf("boss: %v", err)
	}
}

func TestStorytellerChangesNothingWhenAFlagIsMissing(t *testing.T) {
	f, srv, perms := storytellerAccessFake(t, "inviteList", "")
	if _, err := changeAccess(srv, "storyteller", "kate", Access{Download: false}); err == nil || !strings.Contains(err.Error(), "inviteList") {
		t.Fatalf("got %v", err)
	}
	noChanges(t, f)
	if perms()["bookDownload"] != 1 {
		t.Fatal("downloads changed")
	}
}

func TestStorytellerFlagThatMovedTooIsNamed(t *testing.T) {
	_, srv, _ := storytellerAccessFake(t, "", "collectionCreate")
	got, err := changeAccess(srv, "storyteller", "kate", Access{Download: false})
	var ac *AlsoChangedError
	if !errors.As(err, &ac) || !reflect.DeepEqual(ac.Fields, []string{"permissions.collectionCreate"}) {
		t.Fatalf("got %v", err)
	}
	wantAccess(t, got, true, []string{}, false)
}

// ---- the "Test accounts" check ------------------------------------------

// accessFakeService is fakeService with access to change. ignore says yes
// and changes nothing; sneak also takes away a right nobody asked about.
type accessFakeService struct {
	*fakeService
	access    Access
	available []Library
	upload    bool
	ignore    bool
	sneak     bool
}

func (s *accessFakeService) AccessCapability() AccessCapability {
	return AccessCapability{Libraries: true, Download: true, LaterLibraries: true, allFlag: true}
}

func (s *accessFakeService) GetAccess(context.Context, *http.Client, Admin, string) (AccessState, error) {
	return AccessState{Access: s.access, Available: s.available, rest: map[string]any{"upload": s.upload}}, nil
}

func (s *accessFakeService) SetAccess(ctx context.Context, hc *http.Client, a Admin, u string, want Access) (AccessState, error) {
	if !s.ignore {
		s.access = want
	}
	if s.sneak {
		s.upload = !s.upload
	}
	return s.GetAccess(ctx, hc, a, u)
}

func TestSelfTestChangesTheTestAccountsAccessAndReadsItBack(t *testing.T) {
	s := &accessFakeService{fakeService: &fakeService{accounts: map[string]string{}},
		access:    Access{AllLibraries: true, Libraries: []string{"a", "b"}, Download: true},
		available: []Library{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}}
	providers["fake-selftest"] = s
	t.Cleanup(func() { delete(providers, "fake-selftest") })

	rep := runSelfTest(t)
	if !rep.OK || stepNames(rep) != "cleanup ok, create ok, sign-in ok, access ok, remove ok" {
		t.Fatalf("report: %+v", rep)
	}
	if d := rep.Steps[3].Detail; d != "changed and read back: downloads off, 1 of 2 libraries" {
		t.Fatalf("detail %q", d)
	}
	if s.access.AllLibraries || !reflect.DeepEqual(s.access.Libraries, []string{"a"}) || s.access.Download {
		t.Fatalf("access: %+v", s.access)
	}

	// A service that says yes and changes nothing fails the step, and the
	// account is still deleted.
	s.ignore, s.access = true, Access{AllLibraries: true, Download: true}
	rep = runSelfTest(t)
	if rep.OK || stepNames(rep) != "cleanup ok, create ok, sign-in ok, access FAILED, remove ok" ||
		!strings.Contains(rep.Steps[3].Detail, "unchanged") || len(s.accounts) != 0 {
		t.Fatalf("report: %+v, accounts %v", rep, s.accounts)
	}

	// So does one that changes a right nobody asked about.
	s.ignore, s.sneak, s.access = false, true, Access{AllLibraries: true, Download: true}
	rep = runSelfTest(t)
	if rep.OK || stepNames(rep) != "cleanup ok, create ok, sign-in ok, access FAILED, remove ok" ||
		!strings.Contains(rep.Steps[3].Detail, "changed too: upload") {
		t.Fatalf("report: %+v", rep)
	}
}
