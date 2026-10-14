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

	var users []struct {
		Username    string          `json:"username"`
		Email       string          `json:"email"`
		Permissions map[string]bool `json:"permissions"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Username, p.Username) &&
			(p.Email == "" || !strings.EqualFold(u.Email, p.Email)) {
			continue
		}
		if u.Permissions["userCreate"] || u.Permissions["settingsUpdate"] {
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
