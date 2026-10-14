package main

// Admin access through a person's own account (owner, 2026-10-13: "a
// switch in the server profile section that allows admins to select what
// all user gets. The full management access, just server admin page, or
// just management access"). The admin login gives it, only to a sign-in
// with two-step on; the account's phone signs in to Manage server with the
// account's own password and code, reaches no further than its access, and
// steps up with the account's own authenticator. No t.Parallel: the
// database is a package global (see app_test.go).
//
// The auth limiter allows 10 requests a minute per client, and each test
// here stays under it: the comments count the ones that spend it.

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

const (
	adminSignInPath = "/api/v1/accounts/admin-sign-in"
	samPassword     = "a long enough one"
)

// household is three people made BEFORE the admin login is enrolled, while
// the environment token still reaches /admin: Sam (signed in on a phone,
// two-step on), Dana (signed in, no two-step) and Lee (no sign-in).
type household struct {
	sam, dana, lee string // profile ids
	samPhone       string // the account token on Sam's phone
	samSecret      string // Sam's authenticator
}

func setUpHousehold(t *testing.T, s *testServer) household {
	t.Helper()
	var h household
	var invite map[string]any
	h.sam, invite = setUpSam(t, s)
	r := s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": invite["token"], "username": "sam", "password": samPassword,
		"device": newTestDevice(t).body("Sam's phone"),
	}, nil)
	h.samPhone = r.str("deviceToken")
	if h.samPhone == "" {
		t.Fatalf("accept: %v", r)
	}
	r = s.do("POST", "/api/v1/accounts/totp/setup", nil, bearer(h.samPhone))
	h.samSecret = r.str("secret")
	if r = s.do("POST", "/api/v1/accounts/totp/enable", map[string]any{
		"code": codeFor(t, h.samSecret, currentStep())}, bearer(h.samPhone)); r.status != http.StatusOK {
		t.Fatalf("two-step on: %v", r)
	}

	env := bearer(testAPIToken)
	r = s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": "Dana"}, env)
	h.dana = r.body["profile"].(map[string]any)["id"].(string)
	r = s.do("POST", "/api/v1/admin/profiles/"+h.dana+"/invite", nil, env)
	inv := r.body["invite"].(map[string]any)
	if r = s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": inv["token"], "username": "dana", "password": samPassword,
		"device": newTestDevice(t).body("Dana's phone"),
	}, nil); r.status != http.StatusCreated {
		t.Fatalf("Dana accepts: %v", r)
	}
	r = s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": "Lee"}, env)
	h.lee = r.body["profile"].(map[string]any)["id"].(string)
	return h
}

// samCode is a fresh code from Sam's authenticator: the account's spent
// step is cleared first, as freshCode does for the admin login, so a test
// can use one code after another without waiting 30 s.
func samCode(t *testing.T, h household) string {
	t.Helper()
	if _, err := database.DB.Exec(
		`UPDATE account_logins SET last_totp_step = 0 WHERE profile_id = ?`, h.sam); err != nil {
		t.Fatal(err)
	}
	return codeFor(t, h.samSecret, currentStep())
}

func accessPath(profileID string) string {
	return "/api/v1/admin/profiles/" + profileID + "/admin-access"
}

func access(a string) map[string]string { return map[string]string{"access": a} }

// samSignsIn signs Sam's phone in to Manage server with Sam's account
// (one request of the auth limiter's 10) and returns its admin token.
func (s *testServer) samSignsIn(h household, password string) response {
	s.t.Helper()
	return s.do("POST", adminSignInPath, map[string]any{
		"password": password, "code": samCode(s.t, h),
		"deviceLabel": "Sam · Pixel", "persistent": true,
	}, bearer(h.samPhone))
}

func samsLogin(t *testing.T, s *testServer, admin map[string]string, h household) map[string]any {
	t.Helper()
	r := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/accounts/summary", nil, admin)
	for _, p := range r.body["people"].([]any) {
		m := p.(map[string]any)
		if m["profileId"] == h.sam {
			l, _ := m["login"].(map[string]any)
			return l
		}
	}
	t.Fatalf("Sam is not in the summary: %v", r)
	return nil
}

func TestAdminAccessIsTheAdminLoginsToGive(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	h := setUpHousehold(t, s)
	e := enrollDirect(t)
	admin := bearer(s.signIn(e, true, nil).token) // 1

	// Before: nothing to sign in with.
	s.refused(http.StatusForbidden, "no-admin-access", "POST", adminSignInPath, map[string]any{
		"password": samPassword, "code": samCode(t, h)}, bearer(h.samPhone)) // 2

	s.refused(http.StatusBadRequest, "bad-access", "PUT", accessPath(h.sam), access("root"), admin)
	s.refused(http.StatusNotFound, "no-login", "PUT", accessPath(h.lee), access("full"), admin)
	s.refused(http.StatusConflict, "two-step-required", "PUT", accessPath(h.dana), access("serveradmin"), admin)
	// Taking nothing away needs no two-step.
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.dana), access("none"), admin)

	r := s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("serveradmin"), admin)
	if r.str("adminAccess") != "serveradmin" || r.body["signedOut"] != float64(0) {
		t.Fatalf("set: %v", r)
	}
	// Shown to the admin (the People list and the account) and to Sam's phone.
	if l := samsLogin(t, s, admin, h); l["adminAccess"] != "serveradmin" {
		t.Errorf("summary: %v", l)
	}
	r = s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles/"+h.sam+"/account", nil, admin)
	if l, _ := r.body["login"].(map[string]any); l["adminAccess"] != "serveradmin" {
		t.Errorf("account: %v", r)
	}
	r = s.expectStatus(http.StatusOK, "GET", "/api/v1/accounts/me", nil, bearer(h.samPhone))
	if r.str("adminAccess") != "serveradmin" {
		t.Errorf("me: %v", r)
	}

	// No account gives access: not an account token (no admin credential
	// at all), nor an account's own Manage server sign-in, even with full.
	s.expectRefusal("", "PUT", accessPath(h.sam), access("full"), bearer(h.samPhone))
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("full"), admin)
	signed := s.samSignsIn(h, samPassword) // 3
	if signed.status != http.StatusCreated {
		t.Fatalf("sign-in: %v", signed)
	}
	sam := bearer(signed.str("token"))
	s.refused(http.StatusForbidden, "access-denied", "PUT", accessPath(h.sam), access("full"), sam)
	s.refused(http.StatusForbidden, "access-denied", "PUT", accessPath(h.dana), access("none"), sam)

	// Less access signs Sam's phone out at once: it signs in again to
	// carry the new access.
	r = s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("manage"), admin)
	if r.body["signedOut"] != float64(1) {
		t.Fatalf("lowered: %v", r)
	}
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/profiles", nil, sam)
	// And none takes it away for good.
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("none"), admin)
	if l := samsLogin(t, s, admin, h); l["adminAccess"] != "" {
		t.Errorf("after none: %v", l)
	}
}

func TestAnAccountsAdminSignInReachesOnlyItsAccess(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	h := setUpHousehold(t, s)
	e := enrollDirect(t)
	signedIn := s.signIn(e, true, nil) // 1
	admin := bearer(signedIn.token)
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("serveradmin"), admin)

	// The account's password first, then a code from ITS authenticator:
	// the admin login's code is no use here.
	s.refused(http.StatusUnauthorized, "bad-credentials", "POST", adminSignInPath, map[string]any{
		"password": "not the password", "code": samCode(t, h)}, bearer(h.samPhone)) // 2
	s.refused(http.StatusUnauthorized, "code-required", "POST", adminSignInPath, map[string]any{
		"password": samPassword}, bearer(h.samPhone)) // 3
	s.refused(http.StatusUnauthorized, "bad-credentials", "POST", adminSignInPath, map[string]any{
		"password": samPassword, "code": freshCode(t, e.secret)}, bearer(h.samPhone)) // 4
	r := s.samSignsIn(h, samPassword) // 5
	if r.status != http.StatusCreated || !strings.HasPrefix(r.str("token"), "alxa_") ||
		r.str("access") != "serveradmin" {
		t.Fatalf("sign-in: %v", r)
	}
	phone := bearer(r.str("token"))

	// Server Admin only: the kept sign-ins, nothing else.
	s.expectStatus(http.StatusOK, "GET", serviceAdminsPath, nil, phone)
	for _, req := range [][2]string{
		{"GET", "/api/v1/admin/profiles"},
		{"GET", "/api/v1/admin/links"},
		{"GET", "/api/v1/admin/admin-devices"},
		{"GET", "/api/v1/admin/accounts/summary"},
		{"POST", "/api/v1/admin/backup"},
		{"DELETE", "/api/v1/admin/profiles/" + h.lee},
	} {
		s.refused(http.StatusForbidden, "access-denied", req[0], req[1], map[string]string{}, phone)
	}

	// Raised to full: the same sign-in reaches the rest at once.
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("full"), admin)
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, phone)
	s.expectStatus(http.StatusOK, "GET", serviceAdminsPath, nil, phone)
	// The signed-in phones list says whose account it is.
	r = s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/admin-devices", nil, admin)
	var samsID string
	for _, d := range r.body["devices"].([]any) {
		m := d.(map[string]any)
		if m["profileId"] == h.sam {
			samsID, _ = m["id"].(string)
			if m["access"] != "full" || m["createdVia"] != "account" || m["label"] != "Sam · Pixel" {
				t.Errorf("listed as %v", m)
			}
		} else if _, has := m["access"]; has {
			t.Errorf("the admin login's phone carries an access: %v", m)
		}
	}
	if samsID == "" {
		t.Fatalf("Sam's phone is not listed: %v", r)
	}
	// Nobody else's sign-in is its to end: not the admin's phone, not all.
	s.refused(http.StatusForbidden, "access-denied", "DELETE",
		"/api/v1/admin/admin-devices/"+signedIn.id(), nil, phone)
	s.refused(http.StatusForbidden, "access-denied", "DELETE", "/api/v1/admin/admin-devices", nil, phone)

	// Changed to management only: signed out, signed in again, it manages
	// but no longer reaches the kept sign-ins.
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("manage"), admin)
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/profiles", nil, phone)
	r = s.samSignsIn(h, samPassword) // 6
	if r.status != http.StatusCreated || r.str("access") != "manage" {
		t.Fatalf("sign-in again: %v", r)
	}
	phone = bearer(r.str("token"))
	own := r.body["device"].(map[string]any)["id"].(string)
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, phone)
	s.refused(http.StatusForbidden, "access-denied", "GET", serviceAdminsPath, nil, phone)
	s.refused(http.StatusForbidden, "access-denied", "POST", serviceAdminsPath+"/open",
		map[string]string{"code": samCode(t, h), "publicKey": newTestDevice(t).publicKey()}, phone)
	// Its own sign-out is always its own.
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/admin-devices/"+own, nil, phone)
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/profiles", nil, phone)
	// The admin login's phone was never touched.
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, admin)
}

func TestAnAccountsAdminPhoneStepsUpWithItsOwnCode(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	h := setUpHousehold(t, s)
	e := enrollDirect(t)
	admin := bearer(s.signIn(e, true, nil).token) // 1
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("full"), admin)
	r := s.samSignsIn(h, samPassword) // 2
	phone := bearer(r.str("token"))

	// An invite asks a phone for a fresh code: for Sam's phone, Sam's. The
	// admin login's code and recovery codes do not vouch for it.
	invite := "/api/v1/admin/profiles/" + h.lee + "/invite"
	s.expectRefusal("step-up-failed", "POST", invite,
		map[string]string{"totpCode": freshCode(t, e.secret)}, phone) // 3
	s.expectRefusal("step-up-failed", "POST", invite,
		map[string]string{"recoveryCode": e.recovery[0]}, phone) // 4
	s.expectStatus(http.StatusCreated, "POST", invite,
		map[string]string{"totpCode": samCode(t, h)}, phone) // 5

	// The kept sign-ins open the same way.
	s.expectStatus(http.StatusOK, "POST", serviceAdminsPath, map[string]string{
		"type": "abs", "baseUrl": "https://abs.example.com", "username": "chris",
		"password": "kept-for-sam"}, phone)
	d := newTestDevice(t)
	s.expectRefusal("step-up-failed", "POST", serviceAdminsPath+"/open",
		map[string]string{"code": freshCode(t, e.secret), "publicKey": d.publicKey()}, phone) // 6
	r = s.expectStatus(http.StatusOK, "POST", serviceAdminsPath+"/open",
		map[string]string{"code": samCode(t, h), "publicKey": d.publicKey()}, phone) // 7
	if opened := openServiceAdmins(t, d, r); len(opened.Admins) != 1 ||
		opened.Admins[0].Password != "kept-for-sam" {
		t.Fatalf("opened: %+v", opened)
	}

	// The admin login's own phone still steps up with the admin login's
	// code — and Sam's code is no use to it.
	danaInvite := "/api/v1/admin/profiles/" + h.dana + "/invite"
	s.expectRefusal("step-up-failed", "POST", danaInvite,
		map[string]string{"totpCode": samCode(t, h)}, admin) // 8
	s.expectStatus(http.StatusCreated, "POST", danaInvite,
		map[string]string{"totpCode": freshCode(t, e.secret)}, admin) // 9
}

func TestAccountChangesEndItsAdminSignIns(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	h := setUpHousehold(t, s)
	e := enrollDirect(t)
	admin := bearer(s.signIn(e, true, nil).token) // 1
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("full"), admin)
	phone := bearer(s.samSignsIn(h, samPassword).str("token")) // 2

	// A new password: sign-ins made with the old one end; the access stays.
	const newPassword = "another long password"
	s.expectStatus(http.StatusOK, "POST", "/api/v1/accounts/password", map[string]string{
		"currentPassword": samPassword, "newPassword": newPassword}, bearer(h.samPhone))
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/profiles", nil, phone)
	if l := samsLogin(t, s, admin, h); l["adminAccess"] != "full" {
		t.Errorf("after a new password: %v", l)
	}

	// Two-step off: the access goes, and every sign-in made with it.
	phone = bearer(s.samSignsIn(h, newPassword).str("token")) // 3
	s.expectStatus(http.StatusOK, "POST", "/api/v1/accounts/totp/disable",
		map[string]string{"password": newPassword}, bearer(h.samPhone))
	s.expectRefusal("device-token-invalid", "GET", "/api/v1/admin/profiles", nil, phone)
	if l := samsLogin(t, s, admin, h); l["adminAccess"] != "" {
		t.Errorf("after two-step off: %v", l)
	}
	s.refused(http.StatusForbidden, "no-admin-access", "POST", adminSignInPath, map[string]any{
		"password": newPassword, "code": "123456"}, bearer(h.samPhone)) // 4

	// Removing the sign-in ends them too.
	r := s.do("POST", "/api/v1/accounts/totp/setup", nil, bearer(h.samPhone))
	h.samSecret = r.str("secret")
	s.expectStatus(http.StatusOK, "POST", "/api/v1/accounts/totp/enable",
		map[string]string{"code": samCode(t, h)}, bearer(h.samPhone))
	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("serveradmin"), admin)
	phone = bearer(s.samSignsIn(h, newPassword).str("token")) // 5
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/profiles/"+h.sam+"/account", nil, admin)
	s.expectRefusal("device-token-invalid", "GET", serviceAdminsPath, nil, phone)

	// So does removing the person (their row would otherwise outlive them).
	if _, err := repository.NewAdminAuthRepository().CreateDeviceToken("hash-of-danas-phone",
		repository.NewAdminDevice{Label: "Dana's phone", ProfileID: h.dana, Scope: "manage",
			CreatedVia: repository.AdminDeviceViaAccount}, time.Now()); err != nil {
		t.Fatal(err)
	}
	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/profiles/"+h.dana, nil, admin)
	var left int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM admin_device_tokens WHERE profile_id IN (?, ?)`,
		h.sam, h.dana).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("%d sign-ins outlived their account or person", left)
	}
	// The admin login's own phone is untouched by all of it.
	s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, admin)
}
