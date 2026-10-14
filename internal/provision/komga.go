package provision

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Komga (gotson/komga interfaces/api/rest/UserController.kt, read
// 2026-10-14). People sign in with an EMAIL address, so the username is
// one. Basic authentication on every call.
//   - GET /api/v2/users (ADMIN) → [{id, email, roles}];
//   - POST /api/v2/users (ADMIN) {email, password, roles} — no
//     sharedLibraries means every library; roles are sent, since an empty
//     list would give none (the defaults are FILE_DOWNLOAD and
//     PAGE_STREAMING);
//   - PATCH /api/v2/users/{id}/password (ADMIN) {password}.
type komgaProvider struct{}

func (komgaProvider) Capability() Capability {
	return Capability{Type: "komga", Create: true, Reset: true, Email: EmailUsername,
		Note: "Komga signs people in with an email address."}
}

func (komgaProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c := newClient(hc, admin.BaseURL)
	req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	req.SetBasicAuth(admin.Username, admin.Password)
	c.header.Set("Authorization", req.Header.Get("Authorization"))

	var users []struct {
		ID    string   `json:"id"`
		Email string   `json:"email"`
		Roles []string `json:"roles"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		return Result{}, signIn(err)
	}
	for _, u := range users {
		if !strings.EqualFold(u.Email, p.Username) {
			continue
		}
		for _, r := range u.Roles {
			if r == "ADMIN" {
				return Result{}, ErrAdminAccount
			}
		}
		if !allowReset {
			return Result{}, ErrExists
		}
		if err := c.call(ctx, http.MethodPatch, "/api/v2/users/"+url.PathEscape(u.ID)+"/password",
			map[string]string{"password": p.Password}, nil); err != nil {
			return Result{}, err
		}
		return Result{Outcome: Reset}, nil
	}

	if err := c.call(ctx, http.MethodPost, "/api/v2/users", map[string]any{
		"email":    p.Username,
		"password": p.Password,
		"roles":    []string{"FILE_DOWNLOAD", "PAGE_STREAMING"},
	}, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created}, nil
}
