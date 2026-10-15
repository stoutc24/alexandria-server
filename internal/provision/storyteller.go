package provision

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Storyteller (smoores-dev/storyteller applications/web/src/app/api/v2,
// read 2026-10-14). Like Kavita it has no "create with this password"
// call: an admin invites ({email, …permissions} → {key}) and the invite is
// accepted ({username, name, email, password, inviteKey}). Both halves
// are done here.
//   - POST /api/v2/token (form usernameOrEmail, password) →
//     {access_token};
//   - GET /api/v2/users (userList) → [{id, username, email, permissions}];
//   - its password reset (users/{id}/reset-password) only EMAILS a reset
//     link, and its user update takes permissions alone — so an existing
//     account's password cannot be set from here.
type storytellerProvider struct{}

func (storytellerProvider) Capability() Capability {
	return Capability{Type: "storyteller", Create: true, Reset: false, Email: EmailRequired,
		Note: "Storyteller can't set someone else's password: an existing account keeps its own."}
}

func (storytellerProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, _ bool) (Result, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/v2/token",
		url.Values{"usernameOrEmail": {admin.Username}, "password": {admin.Password}}, &login); err != nil {
		return Result{}, signIn(err)
	}
	if login.AccessToken == "" {
		return Result{}, &SignInError{Err: errors.New("Storyteller gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+login.AccessToken)

	// Its flags arrive as true/false or as 1/0 (SQLite), by version.
	var users []struct {
		Username    string         `json:"username"`
		Email       string         `json:"email"`
		Permissions map[string]any `json:"permissions"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Username, p.Username) &&
			(p.Email == "" || !strings.EqualFold(u.Email, p.Email)) {
			continue
		}
		if truthy(u.Permissions["userCreate"]) || truthy(u.Permissions["settingsUpdate"]) {
			return Result{}, ErrAdminAccount
		}
		return Result{}, ErrResetUnsupported
	}

	if p.Email == "" {
		return Result{}, ErrEmailRequired
	}
	invite := map[string]any{
		"email":             p.Email,
		"bookRead":          true,
		"bookList":          true,
		"bookDownload":      true,
		"bookCreate":        false,
		"bookDelete":        false,
		"bookProcess":       false,
		"bookUpdate":        false,
		"collectionCreate":  false,
		"inviteDelete":      false,
		"inviteList":        false,
		"settingsUpdate":    false,
		"userCreate":        false,
		"userDelete":        false,
		"userList":          false,
		"userRead":          false,
		"userUpdate":        false,
		"userPasswordReset": false,
	}
	var made struct {
		Key string `json:"key"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/v2/invites", invite, &made); err != nil {
		return Result{}, err
	}
	if made.Key == "" {
		return Result{}, &ServiceError{Status: http.StatusBadGateway, Message: "Storyteller's invite carried no key"}
	}
	if err := c.call(ctx, http.MethodPost, "/api/v2/users", map[string]string{
		"username": p.Username, "name": p.Name, "email": p.Email, "password": p.Password, "inviteKey": made.Key,
	}, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created,
		Note: "Storyteller may also have emailed the invite to " + p.Email + "; the account is ready without it."}, nil
}

// ---- Change access ----------------------------------------------------------
//
// bookDownload in an account's permissions is downloading. PUT
// /api/v2/users/{id} {permissions} (userUpdate) sets every flag Storyteller
// knows from what it is sent, a missing one becoming false, so each flag
// goes back as read, with bookDownload changed; an answer missing one of
// them changes nothing. One collection: no libraries to pick.

// storytellerFlags is every flag that update sets (users.ts,
// updateUserPermissions).
var storytellerFlags = []string{
	"bookCreate", "bookDelete", "bookRead", "bookProcess", "bookDownload", "bookUpdate", "bookList",
	"collectionCreate", "inviteList", "inviteDelete", "userCreate", "userList", "userRead",
	"userDelete", "userUpdate", "userPasswordReset", "settingsUpdate",
}

func (storytellerProvider) AccessCapability() AccessCapability {
	return AccessCapability{Download: true}
}

type storytellerUser struct {
	ID          string         `json:"id"`
	Username    string         `json:"username"`
	Email       string         `json:"email"`
	Permissions map[string]any `json:"permissions"`
}

// storytellerAccount is the account to read or change, by username or
// email (Storyteller signs in with either): never one that makes users or
// changes settings.
func storytellerAccount(ctx context.Context, c *client, username string) (storytellerUser, error) {
	var users []storytellerUser
	if err := c.call(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		return storytellerUser{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Username, username) && !strings.EqualFold(u.Email, username) {
			continue
		}
		if truthy(u.Permissions["userCreate"]) || truthy(u.Permissions["settingsUpdate"]) {
			return storytellerUser{}, ErrAdminAccount
		}
		return u, nil
	}
	return storytellerUser{}, ErrNoAccount
}

func storytellerAccess(ctx context.Context, c *client, username string) (AccessState, error) {
	u, err := storytellerAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	// As flags, whether they arrive as true/false or 1/0.
	rest := map[string]any{}
	for k, v := range u.Permissions {
		if k != "bookDownload" {
			rest["permissions."+k] = truthy(v)
		}
	}
	return AccessState{rest: rest, Access: Access{Download: truthy(u.Permissions["bookDownload"])}}, nil
}

func (storytellerProvider) GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error) {
	c, err := storytellerSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	return storytellerAccess(ctx, c, username)
}

func (storytellerProvider) SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error) {
	c, err := storytellerSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	u, err := storytellerAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	perms := map[string]bool{}
	for k, v := range u.Permissions {
		perms[k] = truthy(v)
	}
	for _, k := range storytellerFlags {
		if _, ok := u.Permissions[k]; !ok {
			return AccessState{}, &ServiceError{Status: http.StatusBadGateway,
				Message: "Storyteller's answer left out the account's " + k + " permission; nothing was changed"}
		}
	}
	perms["bookDownload"] = want.Download
	if err := c.call(ctx, http.MethodPut, "/api/v2/users/"+url.PathEscape(u.ID),
		map[string]any{"permissions": perms}, nil); err != nil {
		return AccessState{}, err
	}
	return storytellerAccess(ctx, c, username)
}
