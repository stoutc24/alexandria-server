package provision

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Kavita (Kareadita/Kavita Kavita.Server/Controllers/AccountController.cs
// and UsersController.cs, read 2026-10-14). Kavita has no "create a user
// with this password" call: an admin invites, and the invite is accepted.
// Both halves are done here.
//   - POST /api/Account/login {username, password, apiKey} → {token};
//   - GET /api/Users?includePending=true (admin) → [{id, username, email,
//     roles}];
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

func (kavitaProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/Account/login",
		map[string]string{"username": admin.Username, "password": admin.Password, "apiKey": ""}, &login); err != nil {
		return Result{}, signIn(err)
	}
	if login.Token == "" {
		return Result{}, &SignInError{Err: errors.New("Kavita gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+login.Token)

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

	var libraries []struct {
		ID int `json:"id"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/Library/libraries", nil, &libraries); err != nil {
		return Result{}, err
	}
	ids := make([]int, 0, len(libraries))
	for _, l := range libraries {
		ids = append(ids, l.ID)
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
