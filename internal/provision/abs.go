package provision

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Audiobookshelf (advplyr/audiobookshelf server/controllers/UserController.js
// and LibraryController.js, read 2026-10-14):
//   - POST /login {username, password} → {user: {accessToken, token}};
//   - GET /api/users (admin) → {users: [{id, username, type}]}, type being
//     root, admin, user or guest;
//   - GET /api/libraries → {libraries: [{id, name}]} (every one, for an
//     admin);
//   - POST /api/users (admin) {username, password, type, isActive,
//     permissions, librariesAccessible} — isActive defaults to FALSE, so it
//     is sent; librariesAccessible counts only with accessAllLibraries off;
//   - PATCH /api/users/:id {password} rehashes it and signs that person
//     out of their other sessions. An admin cannot change root (403).
type absProvider struct{}

func (absProvider) Capability() Capability {
	return Capability{Type: "abs", Create: true, Reset: true, Email: EmailOptional}
}

func absSignIn(ctx context.Context, hc *http.Client, admin Admin) (*client, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		User struct {
			AccessToken string `json:"accessToken"`
			Token       string `json:"token"`
		} `json:"user"`
	}
	if err := c.call(ctx, http.MethodPost, "/login",
		map[string]string{"username": admin.Username, "password": admin.Password}, &login); err != nil {
		return nil, signIn(err)
	}
	token := login.User.AccessToken
	if token == "" {
		token = login.User.Token
	}
	if token == "" {
		return nil, &SignInError{Err: errors.New("Audiobookshelf gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+token)
	return c, nil
}

func absLibraries(ctx context.Context, c *client) ([]Library, error) {
	var out struct {
		Libraries []Library `json:"libraries"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/libraries", nil, &out); err != nil {
		return nil, err
	}
	return out.Libraries, nil
}

func (absProvider) Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error) {
	c, err := absSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	return absLibraries(ctx, c)
}

func (absProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c, err := absSignIn(ctx, hc, admin)
	if err != nil {
		return Result{}, err
	}

	var list struct {
		Users []struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Type     string `json:"type"`
		} `json:"users"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/users", nil, &list); err != nil {
		return Result{}, err
	}
	for _, u := range list.Users {
		if !strings.EqualFold(u.Username, p.Username) {
			continue
		}
		if u.Type == "root" || u.Type == "admin" {
			return Result{}, ErrAdminAccount
		}
		if !allowReset {
			return Result{}, ErrExists
		}
		if err := c.call(ctx, http.MethodPatch, "/api/users/"+url.PathEscape(u.ID),
			map[string]string{"password": p.Password}, nil); err != nil {
			return Result{}, err
		}
		return Result{Outcome: Reset, Note: "Audiobookshelf signed this account out of its other sessions."}, nil
	}

	permissions := map[string]bool{
		"download":              true,
		"update":                false,
		"delete":                false,
		"upload":                false,
		"accessAllLibraries":    true,
		"accessAllTags":         true,
		"accessExplicitContent": true,
	}
	body := map[string]any{
		"username":    p.Username,
		"password":    p.Password,
		"type":        "user",
		"isActive":    true,
		"permissions": permissions,
	}
	if p.Libraries != nil {
		all, err := absLibraries(ctx, c)
		if err != nil {
			return Result{}, err
		}
		ids, err := picked(all, p.Libraries)
		if err != nil {
			return Result{}, err
		}
		permissions["accessAllLibraries"] = false
		body["librariesAccessible"] = ids
	}
	if p.Email != "" {
		body["email"] = p.Email
	}
	if err := c.call(ctx, http.MethodPost, "/api/users", body, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created}, nil
}
