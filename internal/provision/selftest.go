package provision

// The "Test accounts" check (owner, 2026-10-14: "yes please, so long as we
// also have a cleanup on it so we don't create a bunch of junk accounts").
// With a service's kept admin sign-in it makes a throwaway account, signs
// in as it, and deletes it again; first it deletes whatever an earlier run
// left behind. Every account it makes, and the only ones it ever deletes,
// have a username starting with SelfTestPrefix; an admin, the server owner
// and the root account are never deleted.
//
// Delete calls, read from each service's source (2026-10-14):
//   - Audiobookshelf: DELETE /api/users/:id (admin; never root or self);
//   - Grimmory: DELETE /api/v1/users/{id} (admin);
//   - Jellyfin: DELETE /Users/{id} (elevated);
//   - Komga: DELETE /api/v2/users/{id} (ADMIN, never self);
//   - Kavita: DELETE /api/Users/delete-user?userId= (admin policy);
//   - Navidrome: DELETE /api/user/{id} (admin);
//   - Stump: mutation deleteUser(id, hardDelete: true) (server owner only);
//   - Storyteller: DELETE /api/v2/users/{id} (userDelete);
//   - Calibre-Web: POST /ajax/deleteuser {"userid": [id]} (admin, CSRF).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
)

// SelfTestPrefix starts every account the check makes.
const SelfTestPrefix = "alexandria-selftest-"

// SelfTestStep is one step of a run, as the page shows it.
type SelfTestStep struct {
	Name   string `json:"name"` // cleanup, create, sign-in, remove
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// SelfTestReport is a whole run on one service.
type SelfTestReport struct {
	Service string         `json:"service"`
	OK      bool           `json:"ok"`
	Steps   []SelfTestStep `json:"steps"`
	// Leftover is an account this run could not delete. The next run's
	// cleanup deletes it.
	Leftover string `json:"leftover,omitempty"`
}

// tester is a Provider the check can drive.
type tester interface {
	// signInAs signs in as an account with its own password.
	signInAs(ctx context.Context, hc *http.Client, baseURL, username, password string) error
	// removeTestAccounts deletes every account whose name starts with
	// prefix, never an admin, and says which it deleted.
	removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error)
}

// SelfTestable says whether the check can run on a service type.
func SelfTestable(serviceType string) bool {
	p, ok := For(serviceType)
	if !ok {
		return false
	}
	_, ok = p.(tester)
	return ok
}

// SelfTest runs the check on one service. It returns an error only when
// it cannot start (an unknown type); everything else is in the report.
func SelfTest(ctx context.Context, hc *http.Client, serviceType string, admin Admin) (SelfTestReport, error) {
	prov, ok := For(serviceType)
	if !ok {
		return SelfTestReport{}, ErrUnsupported
	}
	t, ok := prov.(tester)
	if !ok {
		return SelfTestReport{}, ErrUnsupported
	}
	rep := SelfTestReport{Service: serviceType, OK: true}
	step := func(name string, err error, detail string) bool {
		s := SelfTestStep{Name: name, OK: err == nil, Detail: detail}
		if err != nil {
			s.Detail = selfTestMessage(err)
			rep.OK = false
		}
		rep.Steps = append(rep.Steps, s)
		return err == nil
	}

	// 1. What an earlier run left.
	removed, err := t.removeTestAccounts(ctx, hc, admin, SelfTestPrefix)
	if !step("cleanup", err, leftoverNote(removed)) {
		// Without the list nothing below is safe to try.
		return rep, nil
	}

	// 2. A new account, through the same path as a person's.
	p := selfTestPerson(prov.Capability())
	res, err := Ensure(ctx, hc, serviceType, admin, p, false)
	created := step("create", err, res.Note)

	// 3. Its own sign-in.
	if created {
		step("sign-in", t.signInAs(ctx, hc, admin.BaseURL, p.Username, p.Password), "")
	}

	// 4. Gone again, whatever happened above (a create that failed half
	// way can still have left the account behind).
	removed, err = t.removeTestAccounts(ctx, hc, admin, SelfTestPrefix)
	gone := false
	for _, name := range removed {
		if strings.EqualFold(name, p.Username) {
			gone = true
		}
	}
	switch {
	case err != nil:
		step("remove", err, "")
	case created && !gone:
		step("remove", errors.New("the account was still there after the delete"), "")
	default:
		step("remove", nil, "")
	}
	if created && !gone {
		rep.Leftover = p.Username
	}
	return rep, nil
}

func leftoverNote(removed []string) string {
	switch len(removed) {
	case 0:
		return ""
	case 1:
		return "deleted 1 account an earlier test left: " + removed[0]
	default:
		return fmt.Sprintf("deleted %d accounts earlier tests left", len(removed))
	}
}

// selfTestMessage is a step's failure in a line: a refused sign-in names
// the service's answer, not "the saved admin sign-in" (it may be the test
// account's own).
func selfTestMessage(err error) string {
	var si *SignInError
	if errors.As(err, &si) {
		return "the sign-in was refused: " + si.Err.Error()
	}
	return err.Error()
}

// selfTestPerson is a fresh account the services all accept: a random
// name under the prefix, a password with every kind of character a
// password rule asks for, and an address on a domain that cannot exist.
func selfTestPerson(cap Capability) Person {
	name := SelfTestPrefix + randomHex(3)
	email := name + "@selftest.invalid"
	if cap.Email == EmailUsername {
		name = email
	}
	return Person{Username: name, Password: "St1-" + randomAlnum(20), Name: "Alexandria test", Email: email}
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randomAlnum(n int) string {
	const set = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	out := make([]byte, n)
	for i := range out {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		out[i] = set[k.Int64()]
	}
	return string(out)
}

func isTestAccount(name, prefix string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), prefix)
}

// ---- per service -----------------------------------------------------------

func as(base, username, password string) Admin {
	return Admin{BaseURL: base, Username: username, Password: password}
}

func (absProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, err := absSignIn(ctx, hc, as(base, username, password))
	return err
}

func (absProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c, err := absSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	var list struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Type     string `json:"type"`
		} `json:"users"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/users", nil, &list); err != nil {
		return nil, err
	}
	var removed []string
	for _, u := range list.Users {
		if !isTestAccount(u.Username, prefix) || u.Type == "root" || u.Type == "admin" {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, "/api/users/"+url.PathEscape(u.ID), nil, nil); err != nil {
			return removed, err
		}
		removed = append(removed, u.Username)
	}
	return removed, nil
}

func (grimmoryProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, err := grimmorySignIn(ctx, hc, as(base, username, password))
	return err
}

func (grimmoryProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c, err := grimmorySignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	var users []struct {
		ID          int64          `json:"id"`
		Username    string         `json:"username"`
		Permissions map[string]any `json:"permissions"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/users", nil, &users); err != nil {
		return nil, err
	}
	var removed []string
	for _, u := range users {
		if !isTestAccount(u.Username, prefix) ||
			truthy(u.Permissions["admin"]) || truthy(u.Permissions["isAdmin"]) {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", u.ID), nil, nil); err != nil {
			return removed, err
		}
		removed = append(removed, u.Username)
	}
	return removed, nil
}

func (jellyfinProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, done, err := jellyfinSignIn(ctx, hc, as(base, username, password))
	if err != nil {
		return err
	}
	done()
	return nil
}

func (jellyfinProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c, done, err := jellyfinSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	defer done()
	var users []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.call(ctx, http.MethodGet, "/Users", nil, &users); err != nil {
		return nil, err
	}
	var removed []string
	for _, u := range users {
		if !isTestAccount(u.Name, prefix) || u.Policy.IsAdministrator {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, "/Users/"+url.PathEscape(u.ID), nil, nil); err != nil {
			return removed, err
		}
		removed = append(removed, u.Name)
	}
	return removed, nil
}

func (komgaProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	c := komgaClient(hc, as(base, username, password))
	if err := c.call(ctx, http.MethodGet, "/api/v2/users/me", nil, nil); err != nil {
		return signIn(err)
	}
	return nil
}

func (komgaProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c := komgaClient(hc, admin)
	var users []struct {
		ID    string   `json:"id"`
		Email string   `json:"email"`
		Roles []string `json:"roles"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		return nil, signIn(err)
	}
	var removed []string
	for _, u := range users {
		if !isTestAccount(u.Email, prefix) || hasRole(u.Roles, "ADMIN") {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, "/api/v2/users/"+url.PathEscape(u.ID), nil, nil); err != nil {
			return removed, err
		}
		removed = append(removed, u.Email)
	}
	return removed, nil
}

func (kavitaProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, err := kavitaSignIn(ctx, hc, as(base, username, password))
	return err
}

func (kavitaProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c, err := kavitaSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	var users []struct {
		ID       int      `json:"id"`
		Username string   `json:"username"`
		Roles    []string `json:"roles"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/Users?includePending=true", nil, &users); err != nil {
		return nil, err
	}
	var removed []string
	for _, u := range users {
		if !isTestAccount(u.Username, prefix) || hasRole(u.Roles, "Admin") {
			continue
		}
		// Kavita's delete takes userId today and took username before
		// (the owner's answered 400, "One or more validation errors
		// occurred", to userId alone): both go, each version reads its own.
		if err := c.call(ctx, http.MethodDelete, fmt.Sprintf("/api/Users/delete-user?userId=%d&username=%s",
			u.ID, url.QueryEscape(u.Username)), nil, nil); err != nil {
			return removed, err
		}
		removed = append(removed, u.Username)
	}
	return removed, nil
}

func (navidromeProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, err := navidromeSignIn(ctx, hc, as(base, username, password))
	return err
}

func (navidromeProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c, err := navidromeSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	var users []struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/user", nil, &users); err != nil {
		return nil, err
	}
	var removed []string
	for _, u := range users {
		if !isTestAccount(u.UserName, prefix) || u.IsAdmin {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, "/api/user/"+url.PathEscape(u.ID), nil, nil); err != nil {
			return removed, err
		}
		removed = append(removed, u.UserName)
	}
	return removed, nil
}

func (stumpProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, err := stumpSignIn(ctx, hc, as(base, username, password))
	return err
}

func (stumpProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c, err := stumpSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	var out struct {
		Users struct {
			Nodes []struct {
				ID            string `json:"id"`
				Username      string `json:"username"`
				IsServerOwner bool   `json:"isServerOwner"`
			} `json:"nodes"`
		} `json:"users"`
	}
	if err := stumpQuery(ctx, c, `query { users(pagination: {offset: {page: 1, pageSize: 1000, zeroBased: false}}) {
		nodes { id username isServerOwner } } }`, nil, &out); err != nil {
		return nil, err
	}
	var removed []string
	for _, u := range out.Users.Nodes {
		if !isTestAccount(u.Username, prefix) || u.IsServerOwner {
			continue
		}
		if err := stumpQuery(ctx, c, `mutation ($id: ID!) { deleteUser(id: $id, hardDelete: true) { id } }`,
			map[string]any{"id": u.ID}, nil); err != nil {
			return removed, err
		}
		removed = append(removed, u.Username)
	}
	return removed, nil
}

func storytellerSignIn(ctx context.Context, hc *http.Client, admin Admin) (*client, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/v2/token",
		url.Values{"usernameOrEmail": {admin.Username}, "password": {admin.Password}}, &login); err != nil {
		return nil, signIn(err)
	}
	if login.AccessToken == "" {
		return nil, &SignInError{Err: errors.New("Storyteller gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+login.AccessToken)
	return c, nil
}

func (storytellerProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, err := storytellerSignIn(ctx, hc, as(base, username, password))
	return err
}

func (storytellerProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	c, err := storytellerSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	var users []struct {
		ID          any            `json:"id"`
		Username    string         `json:"username"`
		Email       string         `json:"email"`
		Permissions map[string]any `json:"permissions"` // true/false or 1/0
	}
	if err := c.call(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		return nil, err
	}
	var removed []string
	for _, u := range users {
		// An invite never accepted has only its address.
		name := u.Username
		if name == "" {
			name = u.Email
		}
		if !isTestAccount(name, prefix) ||
			truthy(u.Permissions["userCreate"]) || truthy(u.Permissions["settingsUpdate"]) {
			continue
		}
		if err := c.call(ctx, http.MethodDelete, "/api/v2/users/"+url.PathEscape(fmt.Sprint(u.ID)), nil, nil); err != nil {
			return removed, err
		}
		removed = append(removed, name)
	}
	return removed, nil
}

// cwBrowser is a signed-in Calibre-Web session: its pages are forms, so a
// browser's cookies and CSRF tokens are played (as Ensure does).
type cwBrowser struct {
	ctx  context.Context
	hc   *http.Client
	base string
}

func cwSignIn(ctx context.Context, hc *http.Client, admin Admin) (*cwBrowser, error) {
	jar, _ := cookiejar.New(nil)
	b := &cwBrowser{
		ctx: ctx,
		hc: &http.Client{
			Timeout:       hc.Timeout,
			Transport:     hc.Transport,
			Jar:           jar,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		base: strings.TrimRight(strings.TrimSpace(admin.BaseURL), "/"),
	}
	status, page, err := b.do(http.MethodGet, "/login", nil, "", "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &SignInError{Err: &ServiceError{Status: status, Message: "the sign-in page did not open"}}
	}
	form := url.Values{
		"username": {admin.Username}, "password": {admin.Password},
		"remember_me": {"on"}, "csrf_token": {cwToken(page)},
	}
	status, location, err := b.doLocation(http.MethodPost, "/login", strings.NewReader(form.Encode()),
		"application/x-www-form-urlencoded", "")
	if err != nil {
		return nil, err
	}
	if (status != http.StatusFound && status != http.StatusSeeOther) || strings.Contains(location, "/login") {
		return nil, &SignInError{Err: errors.New("Calibre-Web refused the sign-in")}
	}
	return b, nil
}

func (b *cwBrowser) doLocation(method, path string, body io.Reader, ctype, csrf string) (int, string, error) {
	req, err := http.NewRequestWithContext(b.ctx, method, b.base+path, body)
	if err != nil {
		return 0, "", err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if csrf != "" {
		req.Header.Set("X-CSRFToken", csrf)
	}
	req.Header.Set("Referer", b.base+path)
	res, err := b.hc.Do(req)
	if err != nil {
		return 0, "", &UnreachableError{Err: err}
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, res.Header.Get("Location"), nil
}

func (b *cwBrowser) do(method, path string, body io.Reader, ctype, csrf string) (int, string, error) {
	req, err := http.NewRequestWithContext(b.ctx, method, b.base+path, body)
	if err != nil {
		return 0, "", err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if csrf != "" {
		req.Header.Set("X-CSRFToken", csrf)
	}
	req.Header.Set("Referer", b.base+path)
	res, err := b.hc.Do(req)
	if err != nil {
		return 0, "", &UnreachableError{Err: err}
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return res.StatusCode, string(data), nil
}

func (calibreWebProvider) signInAs(ctx context.Context, hc *http.Client, base, username, password string) error {
	_, err := cwSignIn(ctx, hc, as(base, username, password))
	return err
}

func (calibreWebProvider) removeTestAccounts(ctx context.Context, hc *http.Client, admin Admin, prefix string) ([]string, error) {
	b, err := cwSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	c := newClient(b.hc, b.base)
	var out struct {
		Rows []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Role int    `json:"role"`
		} `json:"rows"`
	}
	if err := c.call(ctx, http.MethodGet, "/ajax/listusers?offset=0&limit=1000&sort=id&order=asc", nil, &out); err != nil {
		var se *ServiceError
		if errors.As(err, &se) && se.Status == http.StatusFound {
			return nil, &SignInError{Err: errors.New("the admin sign-in has no admin rights")}
		}
		return nil, err
	}
	var ids []int
	var names []string
	for _, u := range out.Rows {
		if !isTestAccount(u.Name, prefix) || u.Role&1 == 1 {
			continue
		}
		ids = append(ids, u.ID)
		names = append(names, u.Name)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	// A page for the session's CSRF token, which a JSON POST carries in a header.
	status, page, err := b.do(http.MethodGet, "/admin/user/new", nil, "", "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, &ServiceError{Status: status, Message: "the user pages did not open"}
	}
	c.header.Set("X-CSRFToken", cwToken(page))
	c.header.Set("Referer", b.base+"/admin/usertable")
	var answer []struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := c.call(ctx, http.MethodPost, "/ajax/deleteuser", map[string]any{"userid": ids}, &answer); err != nil {
		return nil, err
	}
	for _, a := range answer {
		if a.Type == "danger" {
			return nil, &ServiceError{Status: http.StatusBadRequest, Message: a.Message}
		}
	}
	return names, nil
}

func hasRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}
