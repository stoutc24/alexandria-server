package main

// Alexandria Accounts against the real route table: the admin sets a
// person up, the person accepts an invite on one device and signs in on
// another, and each device opens its own sealed services. No t.Parallel:
// database.DB is a package global (see app_test.go).

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

func withSecretsKey(cfg *config.Config) {
	key := make([]byte, vault.KeySize)
	_, _ = rand.Read(key)
	cfg.SecretsKey = key
}

// testDevice is a device's key pair, as the app makes one at sign-in.
type testDevice struct {
	priv *ecdh.PrivateKey
}

func newTestDevice(t *testing.T) testDevice {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testDevice{priv: k}
}

func (d testDevice) body(label string) map[string]any {
	return map[string]any{
		"label":     label,
		"publicKey": base64.StdEncoding.EncodeToString(d.priv.PublicKey().Bytes()),
	}
}

type openedServices struct {
	Revision string `json:"revision"`
	Services []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		BaseURL   string `json:"baseUrl"`
		LoginKind string `json:"loginKind"`
		Priority  int    `json:"priority"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		Token     string `json:"token"`
		Plex      *struct {
			ServerID    string `json:"serverId"`
			ServerName  string `json:"serverName"`
			UserID      int64  `json:"userId"`
			UserTitle   string `json:"userTitle"`
			Connections []struct {
				URI   string `json:"uri"`
				Local bool   `json:"local"`
				Relay bool   `json:"relay"`
			} `json:"connections"`
		} `json:"plex"`
	} `json:"services"`
}

// openServices opens a GET /accounts/services answer with the device's key.
func openServices(t *testing.T, d testDevice, r response) openedServices {
	t.Helper()
	raw, _ := json.Marshal(r.body["box"])
	var box vault.SealedBox
	if err := json.Unmarshal(raw, &box); err != nil {
		t.Fatalf("no box: %s", r)
	}
	plain, err := vault.OpenBox(d.priv, &box)
	if err != nil {
		t.Fatalf("could not open the box: %v", err)
	}
	var out openedServices
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// setUpSam: a person with two services and an invite, made the way the
// admin page makes them. Returns the profile id and the invite.
func setUpSam(t *testing.T, s *testServer) (profileID string, invite map[string]any) {
	t.Helper()
	admin := bearer(testAPIToken)
	r := s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": "Sam Rivera"}, admin)
	if r.status != 201 {
		t.Fatalf("create profile: %d %s", r.status, r)
	}
	profileID = r.body["profile"].(map[string]any)["id"].(string)

	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/services", map[string]any{
		"type": "abs", "baseUrl": "http://abs.local:13378/", "loginKind": "password",
		"username": "sam", "password": "correct horse",
	}, admin)
	if r.status != 201 {
		t.Fatalf("add abs: %d %s", r.status, r)
	}
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/services", map[string]any{
		"type": "plex", "baseUrl": "https://plex.example", "loginKind": "none",
	}, admin)
	if r.status != 201 {
		t.Fatalf("add plex: %d %s", r.status, r)
	}
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/invite", nil, admin)
	if r.status != 201 {
		t.Fatalf("invite: %d %s", r.status, r)
	}
	if !strings.HasPrefix(r.str("link"), "alexandria://accounts/invite?") {
		t.Fatalf("link %q", r.str("link"))
	}
	return profileID, r.body["invite"].(map[string]any)
}

func TestAccountsInviteSignInAndSealedServices(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	profileID, invite := setUpSam(t, s)
	token := invite["token"].(string)
	code := invite["code"].(string)

	// The app greets the person before they choose a password.
	r := s.do("POST", "/api/v1/accounts/invite/check", map[string]any{"invite": code}, nil)
	if r.status != 200 || r.str("profileName") != "Sam Rivera" || r.str("purpose") != "invite" ||
		r.str("suggestedUsername") != "sam.rivera" {
		t.Fatalf("check: %d %s", r.status, r)
	}

	phone := newTestDevice(t)
	r = s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": token, "username": "Sam", "password": "short", "device": phone.body("Sam's iPhone"),
	}, nil)
	if r.status != 400 || r.str("reason") != "password-too-short" {
		t.Fatalf("short password: %d %s", r.status, r)
	}
	r = s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": token, "username": "Sam", "password": "a long enough one", "device": phone.body("Sam's iPhone"),
	}, nil)
	if r.status != 201 || r.str("username") != "sam" || !strings.HasPrefix(r.str("deviceToken"), "alxp_") {
		t.Fatalf("accept: %d %s", r.status, r)
	}
	phoneToken := r.str("deviceToken")

	// The invite is spent.
	r = s.do("POST", "/api/v1/accounts/invite/check", map[string]any{"invite": token}, nil)
	if r.status != 404 || r.str("reason") != "invite-invalid" {
		t.Fatalf("spent invite: %d %s", r.status, r)
	}

	// The device is linked like any other: Party Play and stats routes take it.
	r = s.do("GET", "/api/v1/devices/me", nil, bearer(phoneToken))
	if r.status != 200 {
		t.Fatalf("devices/me: %d %s", r.status, r)
	}
	r = s.do("GET", "/api/v1/accounts/me", nil, bearer(phoneToken))
	if r.status != 200 || r.body["hasLogin"] != true || r.str("username") != "sam" || r.body["deviceKey"] != true {
		t.Fatalf("me: %d %s", r.status, r)
	}

	// Its services, sealed to its own key, in priority order.
	r = s.do("GET", "/api/v1/accounts/services", nil, bearer(phoneToken))
	if r.status != 200 {
		t.Fatalf("services: %d %s", r.status, r)
	}
	if strings.Contains(string(r.raw), "correct horse") {
		t.Fatal("the password crossed the wire unsealed")
	}
	mine := openServices(t, phone, r)
	if len(mine.Services) != 2 || mine.Services[0].Type != "abs" ||
		mine.Services[0].Password != "correct horse" || mine.Services[0].BaseURL != "http://abs.local:13378" ||
		mine.Services[1].LoginKind != "none" {
		t.Fatalf("opened services %+v", mine)
	}
	r = s.do("GET", "/api/v1/accounts/services?since="+mine.Revision, nil, bearer(phoneToken))
	if r.status != 200 || r.body["unchanged"] != true || r.body["box"] != nil {
		t.Fatalf("unchanged check: %d %s", r.status, r)
	}

	// A second device signs in with the password: no admin, no code.
	tv := newTestDevice(t)
	r = s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "SAM", "password": "wrong password", "device": tv.body("Living Room TV"),
	}, nil)
	if r.status != 401 || r.str("reason") != "bad-credentials" {
		t.Fatalf("wrong password: %d %s", r.status, r)
	}
	r = s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "nobody", "password": "a long enough one", "device": tv.body("Living Room TV"),
	}, nil)
	if r.status != 401 || r.str("reason") != "bad-credentials" {
		t.Fatalf("unknown user: %d %s", r.status, r)
	}
	r = s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "SAM", "password": "a long enough one", "device": tv.body("Living Room TV"),
	}, nil)
	if r.status != 200 || r.str("deviceToken") == phoneToken {
		t.Fatalf("login: %d %s", r.status, r)
	}
	tvToken := r.str("deviceToken")
	r = s.do("GET", "/api/v1/accounts/services", nil, bearer(tvToken))
	tvServices := openServices(t, tv, r)
	if len(tvServices.Services) != 2 {
		t.Fatalf("tv services %+v", tvServices)
	}
	// The phone's key cannot open the TV's box.
	raw, _ := json.Marshal(r.body["box"])
	var box vault.SealedBox
	_ = json.Unmarshal(raw, &box)
	if _, err := vault.OpenBox(phone.priv, &box); err == nil {
		t.Fatal("one device opened another's box")
	}

	// The admin's view: who, which services, never a secret.
	r = s.do("GET", "/api/v1/admin/profiles/"+profileID+"/account", nil, bearer(testAPIToken))
	if r.status != 200 || strings.Contains(string(r.raw), "correct horse") {
		t.Fatalf("admin view: %d %s", r.status, r)
	}
	login := r.body["login"].(map[string]any)
	if login["username"] != "sam" || r.body["secretsKeyConfigured"] != true {
		t.Fatalf("admin view %s", r)
	}

	// Removing a service changes what the devices are given.
	services := r.body["services"].([]any)
	absID := services[0].(map[string]any)["id"].(string)
	r = s.do("DELETE", "/api/v1/admin/profiles/"+profileID+"/services/"+absID, nil, bearer(testAPIToken))
	if r.status != 200 {
		t.Fatalf("delete service: %d %s", r.status, r)
	}
	r = s.do("GET", "/api/v1/accounts/services?since="+mine.Revision, nil, bearer(phoneToken))
	after := openServices(t, phone, r)
	if len(after.Services) != 1 || after.Services[0].Type != "plex" || after.Revision == mine.Revision {
		t.Fatalf("after removal %+v", after)
	}
}

func TestAccountsTwoStepCode(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	_, invite := setUpSam(t, s)
	phone := newTestDevice(t)
	r := s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": invite["token"], "username": "sam", "password": "a long enough one", "device": phone.body("phone"),
	}, nil)
	token := r.str("deviceToken")

	r = s.do("POST", "/api/v1/accounts/totp/setup", nil, bearer(token))
	if r.status != 200 || !strings.HasPrefix(r.str("uri"), "otpauth://totp/") {
		t.Fatalf("setup: %d %s", r.status, r)
	}
	secret := r.str("secret")
	step := time.Now().Unix() / 30
	code, _ := adminauth.CodeAt(secret, step)
	r = s.do("POST", "/api/v1/accounts/totp/enable", map[string]any{"code": code}, bearer(token))
	if r.status != 200 || r.body["totpEnabled"] != true {
		t.Fatalf("enable: %d %s", r.status, r)
	}

	tv := newTestDevice(t)
	r = s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "sam", "password": "a long enough one", "device": tv.body("tv"),
	}, nil)
	if r.status != 401 || r.str("reason") != "code-required" {
		t.Fatalf("no code: %d %s", r.status, r)
	}
	// The code that enabled it is spent.
	r = s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "sam", "password": "a long enough one", "code": code, "device": tv.body("tv"),
	}, nil)
	if r.status != 401 || r.str("reason") != "bad-code" {
		t.Fatalf("replayed code: %d %s", r.status, r)
	}
	next, _ := adminauth.CodeAt(secret, step+1)
	r = s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "sam", "password": "a long enough one", "code": next, "device": tv.body("tv"),
	}, nil)
	if r.status != 200 {
		t.Fatalf("with code: %d %s", r.status, r)
	}
}

func TestAccountsLockedSignInSaysSo(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	profileID, invite := setUpSam(t, s)
	phone := newTestDevice(t)
	s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": invite["token"], "username": "sam", "password": "a long enough one", "device": phone.body("phone"),
	}, nil)
	if _, err := database.DB.Exec(`UPDATE account_logins SET locked_until = ? WHERE profile_id = ?`,
		time.Now().Add(10*time.Minute).Unix(), profileID); err != nil {
		t.Fatal(err)
	}
	r := s.do("POST", "/api/v1/accounts/login", map[string]any{
		"username": "sam", "password": "a long enough one", "device": newTestDevice(t).body("tv"),
	}, nil)
	if r.status != 429 || r.str("reason") != "locked" {
		t.Fatalf("locked: %d %s", r.status, r)
	}
}

func TestAccountsWithoutASecretsKey(t *testing.T) {
	s := newTestServer(t)
	admin := bearer(testAPIToken)
	r := s.do("GET", "/health", nil, nil)
	caps, _ := json.Marshal(r.body["capabilities"])
	if !strings.Contains(string(caps), `"accounts"`) || strings.Contains(string(caps), "accounts.services") {
		t.Fatalf("capabilities %s", caps)
	}
	r = s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": "Sam"}, admin)
	profileID := r.body["profile"].(map[string]any)["id"].(string)
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/services", map[string]any{
		"type": "abs", "baseUrl": "http://abs.local", "loginKind": "password", "password": "x",
	}, admin)
	if r.status != 503 || r.str("reason") != "secrets-key-missing" {
		t.Fatalf("stored a login without a key: %d %s", r.status, r)
	}
	// Signing in still works: the device is linked, Party Play-ready.
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/invite", nil, admin)
	invite := r.body["invite"].(map[string]any)
	phone := newTestDevice(t)
	r = s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": invite["code"], "username": "sam", "password": "a long enough one", "device": phone.body("phone"),
	}, nil)
	if r.status != 201 || r.body["servicesAvailable"] != false {
		t.Fatalf("accept without a key: %d %s", r.status, r)
	}
	r = s.do("GET", "/api/v1/accounts/services", nil, bearer(r.str("deviceToken")))
	if r.status != 503 || r.str("reason") != "secrets-key-missing" {
		t.Fatalf("services without a key: %d %s", r.status, r)
	}
}

func TestAccountsHealthAdvertisesServicesWithAKey(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	r := s.do("GET", "/health", nil, nil)
	caps, _ := json.Marshal(r.body["capabilities"])
	if !strings.Contains(string(caps), "accounts.services") {
		t.Fatalf("capabilities %s", caps)
	}
}

func TestAccountsALinkedDeviceGetsOneKey(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	profileID, _ := setUpSam(t, s)
	// A device linked the old way, by code.
	token, err := repository.NewProfileRepository().MintToken(profileID, "old phone")
	if err != nil {
		t.Fatal(err)
	}
	r := s.do("GET", "/api/v1/accounts/services", nil, bearer(token))
	if r.status != 409 || r.str("reason") != "device-key-required" {
		t.Fatalf("no key yet: %d %s", r.status, r)
	}
	d := newTestDevice(t)
	r = s.do("PUT", "/api/v1/accounts/device-key", map[string]any{"publicKey": d.body("")["publicKey"]}, bearer(token))
	if r.status != 200 {
		t.Fatalf("set key: %d %s", r.status, r)
	}
	other := newTestDevice(t)
	r = s.do("PUT", "/api/v1/accounts/device-key", map[string]any{"publicKey": other.body("")["publicKey"]}, bearer(token))
	if r.status != 409 || r.str("reason") != "device-key-exists" {
		t.Fatalf("swapped key: %d %s", r.status, r)
	}
	r = s.do("GET", "/api/v1/accounts/services", nil, bearer(token))
	if got := openServices(t, d, r); len(got.Services) != 2 {
		t.Fatalf("services %+v", got)
	}
}

func TestAccountsAdminRoutesNeedTheAdmin(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	profileID, invite := setUpSam(t, s)
	phone := newTestDevice(t)
	r := s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": invite["token"], "username": "sam", "password": "a long enough one", "device": phone.body("phone"),
	}, nil)
	personToken := r.str("deviceToken")
	for _, path := range []string{
		"/api/v1/admin/profiles/" + profileID + "/account",
		"/api/v1/admin/profiles/" + profileID + "/services",
	} {
		if r := s.do("GET", path, nil, bearer(personToken)); r.status != 401 {
			t.Errorf("%s with a person's token: %d", path, r.status)
		}
	}
	// And a person's routes refuse the admin token.
	if r := s.do("GET", "/api/v1/accounts/services", nil, bearer(testAPIToken)); r.status != 401 {
		t.Errorf("services with the admin token: %d", r.status)
	}
}

func TestAccountsInviteWarnsWhenItsLinkPointsAtLoopback(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	r := s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": "Sam"}, admin)
	profileID := r.body["profile"].(map[string]any)["id"].(string)

	// The test transport asks as example.com: a reachable address, no warning.
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/invite", nil, admin)
	if r.status != 201 || r.body["serverUrlWarning"] != nil {
		t.Fatalf("plain invite: %d %s", r.status, r)
	}
	// The page opened on the server itself (an absolute URL sets the Host
	// the server sees).
	for _, host := range []string{"localhost:4000", "127.0.0.1:4000", "[::1]:4000"} {
		req := httptest.NewRequest("POST", "/api/v1/admin/profiles/"+profileID+"/invite", nil)
		req.Host = host
		req.Header.Set("Authorization", "Bearer "+testAPIToken)
		resp, err := s.app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		warning, _ := body["serverUrlWarning"].(string)
		if resp.StatusCode != 201 || !strings.Contains(warning, "can't reach") {
			t.Fatalf("invite made at %s: %d %v", host, resp.StatusCode, body["serverUrl"])
		}
	}
}

func TestAccountsInviteLinkToShareOpensTheInvitePage(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	r := s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": "Sam"}, admin)
	profileID := r.body["profile"].(map[string]any)["id"].(string)
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/invite", nil, admin)
	token := r.body["invite"].(map[string]any)["token"].(string)
	web := r.str("webLink")
	if !strings.HasPrefix(web, "http://example.com/invite#invite=") || !strings.HasSuffix(web, token) {
		t.Fatalf("webLink %q", web)
	}

	page := s.do("GET", "/invite", nil, nil)
	if page.status != 200 || !strings.Contains(string(page.raw), "Open Alexandria") {
		t.Fatalf("invite page: %d", page.status)
	}
	csp := page.header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || page.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("invite page headers: %q", csp)
	}
	// The page only hands over well-formed invites.
	if !strings.Contains(string(page.raw), `alxi_[0-9a-f]{16,128}`) {
		t.Fatal("the page no longer checks the invite's shape")
	}
}

// A Plex person the admin picked (in their browser or on their phone)
// reaches the person's device whole: the key, sealed, and where the
// server is — with no address typed by anyone.
func TestAccountsAPickedPlexPersonReachesTheirDevice(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	r := s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": "Grandma"}, admin)
	if r.status != 201 {
		t.Fatalf("create profile: %d %s", r.status, r)
	}
	profileID := r.body["profile"].(map[string]any)["id"].(string)
	pick := map[string]any{
		"serverId": "5d776825e9c9b9b6e8f1a2b3c4d5e6f708192a3b", "serverName": "StoutOffice",
		"userId": 42, "userTitle": "Grandma",
		"connections": []map[string]any{
			{"uri": "http://192.168.1.9:32400", "local": true},
			{"uri": "https://203-0-113-7.abc123.plex.direct:32400"},
			{"uri": "https://relay-1.abc123.plex.direct:8443", "relay": true},
		},
	}
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/services", map[string]any{
		"type": "plex", "loginKind": "plexuser", "plex": pick,
	}, admin)
	if r.status != 400 || r.str("reason") != "bad-service" {
		t.Fatalf("a pick without its key: %d %s", r.status, r)
	}
	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/services", map[string]any{
		"type": "plex", "loginKind": "plexuser", "token": "key-grandma", "plex": pick,
	}, admin)
	if r.status != 201 {
		t.Fatalf("add plex person: %d %s", r.status, r)
	}
	// The admin sees who and where, never the key.
	r = s.do("GET", "/api/v1/admin/profiles/"+profileID+"/services", nil, admin)
	if r.status != 200 || strings.Contains(string(r.raw), "key-grandma") ||
		!strings.Contains(string(r.raw), `"userTitle":"Grandma"`) {
		t.Fatalf("admin list: %d %s", r.status, r)
	}

	r = s.do("POST", "/api/v1/admin/profiles/"+profileID+"/invite", nil, admin)
	if r.status != 201 {
		t.Fatalf("invite: %d %s", r.status, r)
	}
	token := r.body["invite"].(map[string]any)["token"].(string)
	phone := newTestDevice(t)
	r = s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": token, "username": "grandma", "password": "a long enough one", "device": phone.body("Grandma's iPad"),
	}, nil)
	if r.status != 201 {
		t.Fatalf("accept: %d %s", r.status, r)
	}
	r = s.do("GET", "/api/v1/accounts/services", nil, bearer(r.str("deviceToken")))
	if r.status != 200 || strings.Contains(string(r.raw), "key-grandma") {
		t.Fatalf("services: %d %s", r.status, r)
	}
	mine := openServices(t, phone, r)
	if len(mine.Services) != 1 {
		t.Fatalf("services %+v", mine)
	}
	got := mine.Services[0]
	if got.LoginKind != "plexuser" || got.Token != "key-grandma" || got.Plex == nil ||
		got.Plex.ServerID != "5d776825e9c9b9b6e8f1a2b3c4d5e6f708192a3b" || got.Plex.UserTitle != "Grandma" ||
		got.Plex.UserID != 42 || len(got.Plex.Connections) != 3 || !got.Plex.Connections[0].Local ||
		!got.Plex.Connections[2].Relay || got.BaseURL != "https://203-0-113-7.abc123.plex.direct:32400" {
		t.Fatalf("the device's Plex %+v plex %+v", got, got.Plex)
	}
}
