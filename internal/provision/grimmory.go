package provision

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// Grimmory, BookLore's continuation (grimmory-tools/grimmory, read
// 2026-10-14):
//   - POST /api/v1/auth/login {username, password} → {accessToken};
//   - GET /api/v1/users (admin) → [{id, username, permissions: {admin}}]
//     (Lombok's isAdmin becomes "admin"; "isAdmin" is read as well);
//   - POST /api/v1/auth/register (admin) UserCreateRequest: username,
//     password of 8 to 72 characters, name and email both required,
//     permission* flags, selectedLibraries (GET /api/v1/libraries → [{id,
//     name}]). A new account is flagged
//     defaultPassword, so Grimmory's website asks for a new one on the
//     first sign-in there;
//   - PUT /api/v1/users/change-user-password (admin) {userId, newPassword}.
type grimmoryProvider struct{}

func (grimmoryProvider) Capability() Capability {
	return Capability{Type: "booklore", Create: true, Reset: true, Email: EmailRequired}
}

func grimmorySignIn(ctx context.Context, hc *http.Client, admin Admin) (*client, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/v1/auth/login",
		map[string]string{"username": admin.Username, "password": admin.Password}, &login); err != nil {
		return nil, signIn(err)
	}
	if login.AccessToken == "" {
		return nil, &SignInError{Err: errors.New("Grimmory gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+login.AccessToken)
	return c, nil
}

func grimmoryLibraries(ctx context.Context, c *client) ([]Library, error) {
	var libraries []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/libraries", nil, &libraries); err != nil {
		return nil, err
	}
	out := make([]Library, 0, len(libraries))
	for _, l := range libraries {
		out = append(out, Library{ID: strconv.FormatInt(l.ID, 10), Name: l.Name})
	}
	return out, nil
}

func (grimmoryProvider) Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error) {
	c, err := grimmorySignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	return grimmoryLibraries(ctx, c)
}

func (grimmoryProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c, err := grimmorySignIn(ctx, hc, admin)
	if err != nil {
		return Result{}, err
	}

	var users []struct {
		ID          int64          `json:"id"`
		Username    string         `json:"username"`
		Permissions map[string]any `json:"permissions"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/v1/users", nil, &users); err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Username, p.Username) {
			continue
		}
		if truthy(u.Permissions["admin"]) || truthy(u.Permissions["isAdmin"]) {
			return Result{}, ErrAdminAccount
		}
		if !allowReset {
			return Result{}, ErrExists
		}
		if err := c.call(ctx, http.MethodPut, "/api/v1/users/change-user-password",
			map[string]any{"userId": u.ID, "newPassword": p.Password}, nil); err != nil {
			return Result{}, err
		}
		return Result{Outcome: Reset}, nil
	}

	if p.Email == "" {
		return Result{}, ErrEmailRequired
	}
	// The picked libraries, else every one: a new account with none would
	// see nothing.
	all, err := grimmoryLibraries(ctx, c)
	if err != nil {
		return Result{}, err
	}
	chosen, err := picked(all, p.Libraries)
	if err != nil {
		return Result{}, err
	}
	ids := []int64{}
	for _, l := range all {
		if chosen == nil || slices.Contains(chosen, l.ID) {
			id, _ := strconv.ParseInt(l.ID, 10, 64)
			ids = append(ids, id)
		}
	}
	body := map[string]any{
		"username":               p.Username,
		"password":               p.Password,
		"name":                   p.Name,
		"email":                  p.Email,
		"permissionDownload":     true,
		"permissionAccessOpds":   true,
		"permissionSyncKoreader": true,
		"permissionSyncKobo":     true,
		"selectedLibraries":      ids,
	}
	if err := c.call(ctx, http.MethodPost, "/api/v1/auth/register", body, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created,
		Note: "Grimmory's own website asks for a new password the first time this account signs in there."}, nil
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}
