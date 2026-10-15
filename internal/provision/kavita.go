package provision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Kavita (Kareadita/Kavita Kavita.Server/Controllers/AccountController.cs
// and UsersController.cs, read 2026-10-14). Kavita has no "create a user
// with this password" call: an admin invites, and the invite is accepted.
// Both halves are done here.
//   - POST /api/Account/login {username, password, apiKey} → {token};
//   - GET /api/Users?includePending=true (admin) → [{id, username, email,
//     roles}];
//   - GET /api/Library/libraries → [{id, name}];
//   - POST /api/Account/invite (admin) {email, roles, libraries,
//     ageRestriction} → {emailLink, emailSent}. The link carries the
//     token. Without a usable email address (or mail set up) Kavita sends
//     nothing and still answers the link, so the username stands in for a
//     missing address, as Kavita itself allows;
//   - POST /api/Account/confirm-email {email, token, password, username};
//   - roles: "Login" is REQUIRED to sign in at all ("disabled-account"
//     otherwise); Pleb is added by Kavita itself;
//   - POST /api/Account/reset-password {userName, password, oldPassword}:
//     an admin resetting someone else needs no old password. It answers
//     200 for an unknown name, so the account is looked up first.
type kavitaProvider struct{}

func (kavitaProvider) Capability() Capability {
	return Capability{Type: "kavita", Create: true, Reset: true, Email: EmailOptional}
}

func kavitaSignIn(ctx context.Context, hc *http.Client, admin Admin) (*client, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/Account/login",
		map[string]string{"username": admin.Username, "password": admin.Password, "apiKey": ""}, &login); err != nil {
		return nil, signIn(err)
	}
	if login.Token == "" {
		return nil, &SignInError{Err: errors.New("Kavita gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+login.Token)
	return c, nil
}

func kavitaLibraries(ctx context.Context, c *client) ([]Library, error) {
	var libraries []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/Library/libraries", nil, &libraries); err != nil {
		return nil, err
	}
	out := make([]Library, 0, len(libraries))
	for _, l := range libraries {
		out = append(out, Library{ID: strconv.Itoa(l.ID), Name: l.Name})
	}
	return out, nil
}

func (kavitaProvider) Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error) {
	c, err := kavitaSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	return kavitaLibraries(ctx, c)
}

func (kavitaProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c, err := kavitaSignIn(ctx, hc, admin)
	if err != nil {
		return Result{}, err
	}

	var users []struct {
		ID       int      `json:"id"`
		Username string   `json:"username"`
		Email    string   `json:"email"`
		Roles    []string `json:"roles"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/Users?includePending=true", nil, &users); err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Username, p.Username) {
			continue
		}
		for _, r := range u.Roles {
			if r == "Admin" {
				return Result{}, ErrAdminAccount
			}
		}
		if !allowReset {
			return Result{}, ErrExists
		}
		if err := c.call(ctx, http.MethodPost, "/api/Account/reset-password", map[string]string{
			"userName": u.Username, "password": p.Password, "oldPassword": "",
		}, nil); err != nil {
			return Result{}, err
		}
		return Result{Outcome: Reset}, nil
	}

	// The picked libraries, else every one: Kavita gives an invite only
	// those it names.
	all, err := kavitaLibraries(ctx, c)
	if err != nil {
		return Result{}, err
	}
	chosen, err := picked(all, p.Libraries)
	if err != nil {
		return Result{}, err
	}
	ids := []int{}
	for _, l := range all {
		if chosen == nil || slices.Contains(chosen, l.ID) {
			id, _ := strconv.Atoi(l.ID)
			ids = append(ids, id)
		}
	}
	email := p.Email
	if email == "" {
		email = p.Username
	}
	var invite struct {
		EmailLink string `json:"emailLink"`
		EmailSent bool   `json:"emailSent"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/Account/invite", map[string]any{
		"email":     email,
		"roles":     []string{"Login", "Download", "Change Password", "Bookmark"},
		"libraries": ids,
		// NotApplicable: no age restriction.
		"ageRestriction": map[string]any{"ageRating": -1, "includeUnknowns": true},
	}, &invite); err != nil {
		return Result{}, err
	}
	link, err := url.Parse(invite.EmailLink)
	token := ""
	if err == nil {
		token = link.Query().Get("token")
	}
	if token == "" {
		return Result{}, &ServiceError{Status: http.StatusBadGateway, Message: "Kavita's invite carried no token"}
	}
	if err := c.call(ctx, http.MethodPost, "/api/Account/confirm-email", map[string]string{
		"email": email, "token": token, "password": p.Password, "username": p.Username,
	}, nil); err != nil {
		return Result{}, err
	}
	res := Result{Outcome: Created}
	if invite.EmailSent {
		res.Note = "Kavita also emailed an invite to " + email + "; the account is ready without it."
	}
	return res, nil
}

// ---- Change access ----------------------------------------------------------
//
// GET /api/Users answers each member's roles ("Download" is downloading),
// libraries [{id}], ageRestriction and, on newer Kavita, identityProvider.
// POST /api/Account/update {userId, username, email, roles, libraries,
// ageRestriction, identityProvider} (admin) sets them ALL, so the member's
// own go back as read with those two changed — identityProvider above all:
// a different one there is the only thing Kavita applies. Kavita has no
// "every library" setting.

func (kavitaProvider) AccessCapability() AccessCapability {
	return AccessCapability{Libraries: true, Download: true}
}

type kavitaMember struct {
	ID       int      `json:"id"`
	Username string   `json:"username"`
	Email    *string  `json:"email"`
	Roles    []string `json:"roles"`
	// Libraries lists what the member can see.
	Libraries []struct {
		ID int `json:"id"`
	} `json:"libraries"`
	// As Kavita has them: they go back unchanged.
	AgeRestriction   json.RawMessage `json:"ageRestriction"`
	IdentityProvider json.RawMessage `json:"identityProvider"`
}

// kavitaAccount is the member to read or change: never an admin.
func kavitaAccount(ctx context.Context, c *client, username string) (kavitaMember, error) {
	var users []kavitaMember
	if err := c.call(ctx, http.MethodGet, "/api/Users?includePending=true", nil, &users); err != nil {
		return kavitaMember{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Username, username) {
			continue
		}
		if hasRole(u.Roles, "Admin") {
			return kavitaMember{}, ErrAdminAccount
		}
		return u, nil
	}
	return kavitaMember{}, ErrNoAccount
}

func kavitaAccess(ctx context.Context, c *client, username string) (AccessState, error) {
	u, err := kavitaAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	all, err := kavitaLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	libs := []string{}
	for _, l := range u.Libraries {
		libs = append(libs, strconv.Itoa(l.ID))
	}
	// Pleb is Kavita's own, added to every non-admin on each update.
	rest := map[string]any{
		"username":         u.Username,
		"email":            u.Email,
		"roles":            sortedWithout(u.Roles, "Download", "Pleb"),
		"ageRestriction":   parsed(u.AgeRestriction),
		"identityProvider": parsed(u.IdentityProvider),
	}
	return AccessState{Available: all, rest: rest,
		Access: Access{Libraries: libs, Download: hasRole(u.Roles, "Download")}}, nil
}

func (kavitaProvider) GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error) {
	c, err := kavitaSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	return kavitaAccess(ctx, c, username)
}

func (kavitaProvider) SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error) {
	c, err := kavitaSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	u, err := kavitaAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	all, err := kavitaLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	chosen, err := chosenIDs(all, want)
	if err != nil {
		return AccessState{}, err
	}
	ids := []int{}
	for _, id := range chosen {
		n, _ := strconv.Atoi(id)
		ids = append(ids, n)
	}
	roles := []string{}
	for _, r := range u.Roles {
		if r != "Download" {
			roles = append(roles, r)
		}
	}
	if want.Download {
		roles = append(roles, "Download")
	}
	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	body := map[string]any{
		"userId":    u.ID,
		"username":  u.Username,
		"email":     email,
		"roles":     roles,
		"libraries": ids,
		// NotApplicable, as a new account gets, if Kavita gave none.
		"ageRestriction": map[string]any{"ageRating": -1, "includeUnknowns": true},
	}
	if raw := strings.TrimSpace(string(u.AgeRestriction)); raw != "" && raw != "null" {
		body["ageRestriction"] = u.AgeRestriction
	}
	if raw := strings.TrimSpace(string(u.IdentityProvider)); raw != "" && raw != "null" {
		body["identityProvider"] = u.IdentityProvider
	}
	if err := c.call(ctx, http.MethodPost, "/api/Account/update", body, nil); err != nil {
		return AccessState{}, err
	}
	return kavitaAccess(ctx, c, username)
}
