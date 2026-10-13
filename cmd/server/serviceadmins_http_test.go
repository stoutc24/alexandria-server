package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

const serviceAdminsPath = "/api/v1/admin/service-admins"

type openedServiceAdmins struct {
	Admins []struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Name       string `json:"name"`
		BaseURL    string `json:"baseUrl"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		Unreadable bool   `json:"unreadable"`
	} `json:"admins"`
}

func openServiceAdmins(t *testing.T, d testDevice, r response) openedServiceAdmins {
	t.Helper()
	raw, _ := json.Marshal(r.body["box"])
	var box vault.SealedBox
	if err := json.Unmarshal(raw, &box); err != nil {
		t.Fatalf("box: %v (%v)", err, r)
	}
	plain, err := vault.OpenBox(d.priv, &box)
	if err != nil {
		t.Fatalf("open box: %v", err)
	}
	var out openedServiceAdmins
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func (d testDevice) publicKey() string {
	return base64.StdEncoding.EncodeToString(d.priv.PublicKey().Bytes())
}

// refused is expectRefusal for any status, not only 401.
func (s *testServer) refused(status int, reason, method, path string, body any, hdr map[string]string) {
	s.t.Helper()
	r := s.do(method, path, body, hdr)
	if got, _ := r.body["reason"].(string); r.status != status || got != reason {
		s.t.Fatalf("%s %s: %d %v, want %d reason %q", method, path, r.status, r, status, reason)
	}
}

// Server Admin's sign-ins: kept sealed, listed without passwords, one per
// service, and read back only by an admin phone with a fresh code, boxed
// to the key it sent.
func TestServiceAdminsKeptSealedAndOpenedOnlyByAPhoneWithAFreshCode(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	e := enrollDirect(t)
	phone := bearer(s.signIn(e, true, nil).token)

	r := s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "booklore", "baseUrl": "http://grimmory.lan:6060/",
		"username": " chris ", "password": "first-pass"}, phone)
	id := r.str("id")
	if r.str("baseUrl") != "http://grimmory.lan:6060" || r.str("name") != "Grimmory" ||
		r.str("username") != "chris" || id == "" {
		t.Fatalf("saved: %v", r)
	}
	if _, has := r.body["password"]; has {
		t.Fatalf("a save answered with the password: %v", r)
	}

	// Sealed at rest: the column never holds the password.
	var sealed string
	if err := database.DB.QueryRow(
		`SELECT secret FROM service_admin_logins WHERE id = ?`, id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "first-pass") || sealed == "" {
		t.Fatalf("the password is not sealed: %q", sealed)
	}

	// One per service: saving Grimmory again replaces it, same id.
	r = s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "booklore", "baseUrl": "http://grimmory.lan:6060",
		"username": "chris", "password": "second-pass"}, phone)
	if r.str("id") != id {
		t.Fatalf("a second save made a second row: %v", r)
	}
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "stump", "baseUrl": "https://stump.example.com",
		"username": "chris", "password": "stump-pass"}, phone)

	// Plex is never kept (its sign-in is the whole Plex account), nor
	// anything without an address, a username or a password.
	for _, bad := range []map[string]string{
		{"type": "plex", "baseUrl": "http://plex.lan:32400", "username": "x", "password": "y"},
		{"type": "calibre", "baseUrl": "http://calibre.lan", "username": "x", "password": "y"},
		{"type": "kavita", "baseUrl": "ftp://kavita.lan", "username": "x", "password": "y"},
		{"type": "kavita", "baseUrl": "http://kavita.lan", "username": " ", "password": "y"},
		{"type": "kavita", "baseUrl": "http://kavita.lan", "username": "x", "password": ""},
		{"type": "kavita", "baseUrl": "http://kavita.lan", "username": "x",
			"password": strings.Repeat("p", 1025)},
	} {
		s.refused(http.StatusBadRequest, "bad-service-admin", "POST", serviceAdminsPath, bad, phone)
	}

	// The list never carries a password.
	r = s.expectStatus(http.StatusOK, "GET", serviceAdminsPath, nil, phone)
	if r.body["available"] != true || !bytes.Contains(r.raw, []byte(`"stump"`)) {
		t.Fatalf("list: %v", r)
	}
	if bytes.Contains(r.raw, []byte("pass")) {
		t.Fatalf("the list carries a password: %s", r.raw)
	}

	// Reading them back: a fresh code first, and a key to box them to.
	d := newTestDevice(t)
	s.expectRefusal("step-up-failed", "POST", serviceAdminsPath+"/open",
		map[string]string{"publicKey": d.publicKey()}, phone)
	s.refused(http.StatusBadRequest, "bad-key", "POST", serviceAdminsPath+"/open",
		map[string]string{"code": freshCode(t, e.secret), "publicKey": "not-a-key"}, phone)
	r = s.expectStatus(http.StatusOK, "POST", serviceAdminsPath+"/open",
		map[string]string{"code": freshCode(t, e.secret), "publicKey": d.publicKey()}, phone)
	if bytes.Contains(r.raw, []byte("pass")) {
		t.Fatalf("the open answer is not boxed: %s", r.raw)
	}
	opened := openServiceAdmins(t, d, r)
	got := map[string]string{}
	for _, a := range opened.Admins {
		got[a.Type] = a.Password
	}
	if got["booklore"] != "second-pass" || got["stump"] != "stump-pass" || len(got) != 2 {
		t.Fatalf("opened: %+v", opened)
	}
	// Another key cannot open that box.
	if _, err := vault.OpenBox(newTestDevice(t).priv, func() *vault.SealedBox {
		raw, _ := json.Marshal(r.body["box"])
		var b vault.SealedBox
		_ = json.Unmarshal(raw, &b)
		return &b
	}()); err == nil {
		t.Fatal("a box opened with another key")
	}

	// The web page never reads them, code or not.
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	browser := map[string]string{"Cookie": "alx_admin=" + findCookie(login.resp, "alx_admin").Value}
	s.refused(http.StatusForbidden, "phones-only", "POST", serviceAdminsPath+"/open",
		map[string]string{"code": freshCode(t, e.secret), "publicKey": d.publicKey()}, browser)
	s.expectStatus(http.StatusOK, "GET", serviceAdminsPath, nil, browser)

	// Removed: gone from the list; a second removal is a 404.
	s.expectStatus(http.StatusNoContent, "DELETE", serviceAdminsPath+"/"+id, nil, browser)
	s.refused(http.StatusNotFound, "not-found", "DELETE", serviceAdminsPath+"/"+id, nil, phone)
	r = s.expectStatus(http.StatusOK, "GET", serviceAdminsPath, nil, phone)
	if bytes.Contains(r.raw, []byte(`"booklore"`)) {
		t.Fatalf("still listed after removal: %s", r.raw)
	}
}

// Without a secrets key nothing can be kept, and the list says so.
func TestServiceAdminsWithoutASecretsKey(t *testing.T) {
	s := newTestServer(t)
	e := enrollDirect(t)
	phone := bearer(s.signIn(e, true, nil).token)
	r := s.expectStatus(http.StatusOK, "GET", serviceAdminsPath, nil, phone)
	if r.body["available"] != false {
		t.Fatalf("list: %v", r)
	}
	s.refused(http.StatusServiceUnavailable, "no-secrets-key", "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": "http://abs.lan", "username": "x", "password": "y"}, phone)
}

// Admin only: no token, a person's account token and the environment token
// after enrolment are all refused.
func TestServiceAdminsAreForTheAdminAlone(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	_, invite := setUpSam(t, s)
	accepted := s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": invite["token"], "username": "sam", "password": "a long enough one",
		"device": newTestDevice(t).body("phone"),
	}, nil)
	personToken := accepted.str("deviceToken")
	if personToken == "" {
		t.Fatalf("accept: %v", accepted)
	}
	enrollDirect(t)
	for _, hdr := range []map[string]string{nil, bearer(personToken), bearer(testAPIToken)} {
		for _, req := range [][2]string{
			{"GET", serviceAdminsPath},
			{"POST", serviceAdminsPath},
			{"POST", serviceAdminsPath + "/open"},
			{"DELETE", serviceAdminsPath + "/x"},
		} {
			r := s.do(req[0], req[1], map[string]string{}, hdr)
			if r.status != http.StatusUnauthorized && r.status != http.StatusForbidden {
				t.Errorf("%s %s with %v: %d", req[0], req[1], hdr, r.status)
			}
		}
	}
}

// A phone's backup leaves every kept sign-in out, sealed or not; the
// browser's keeps them.
func TestPhoneBackupLeavesServiceAdminsOut(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	e := enrollDirect(t)
	phone := bearer(s.signIn(e, true, nil).token)
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "navidrome", "baseUrl": "http://navidrome.lan:4533",
		"username": "chris", "password": "navi-pass"}, phone)
	var sealed string
	if err := database.DB.QueryRow(`SELECT secret FROM service_admin_logins`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}

	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	browser := map[string]string{"Cookie": "alx_admin=" + findCookie(login.resp, "alx_admin").Value}
	full := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/backup", nil, browser)
	if countRows(t, openBackup(t, full.raw), "service_admin_logins") != 1 {
		t.Fatal("check is blind: the browser's backup has no kept sign-in")
	}

	r := s.expectStatus(http.StatusOK, "POST", "/api/v1/admin/backup",
		map[string]string{"code": freshCode(t, e.secret)}, phone)
	if n := countRows(t, openBackup(t, r.raw), "service_admin_logins"); n != 0 {
		t.Fatalf("the phone's backup kept %d sign-ins", n)
	}
	if bytes.Contains(r.raw, []byte(sealed)) {
		t.Fatal("the phone's backup still holds the sealed password")
	}
}
