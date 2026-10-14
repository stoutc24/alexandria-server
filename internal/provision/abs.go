package provision

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Audiobookshelf (advplyr/audiobookshelf server/controllers/UserController.js,
// read 2026-10-14):
//   - POST /login {username, password} → {user: {accessToken, token}};
//   - GET /api/users (admin) → {users: [{id, username, type}]}, type being
//     root, admin, user or guest;
//   - POST /api/users (admin) {username, password, type, isActive,
//     permissions} — isActive defaults to FALSE, so it is sent;
//   - PATCH /api/users/:id {password} rehashes it and signs that person
//     out of their other sessions. An admin cannot change root (403).
type absProvider struct{}

func (absProvider) Capability() Capability {
	return Capability{Type: "abs", Create: true, Reset: true, Email: EmailOptional}
}

func (absProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		User struct {
			AccessToken string `json:"accessToken"`
			Token       string `json:"token"`
		} `json:"user"`
	}
	if err := c.call(ctx, http.MethodPost, "/login",
		map[string]string{"username": admin.Username, "password": admin.Password}, &login); err != nil {
		return Result{}, signIn(err)
	}
	token := login.User.AccessToken
	if token == "" {
		token = login.User.Token
	}
	if token == "" {
		return Result{}, &SignInError{Err: errors.New("Audiobookshelf gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+token)

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

	body := map[string]any{
		"username": p.Username,
		"password": p.Password,
		"type":     "user",
		"isActive": true,
		"permissions": map[string]bool{
			"download":              true,
			"update":                false,
			"delete":                false,
			"upload":                false,
			"accessAllLibraries":    true,
			"accessAllTags":         true,
			"accessExplicitContent": true,
		},
	}
	if p.Email != "" {
		body["email"] = p.Email
	}
	if err := c.call(ctx, http.MethodPost, "/api/users", body, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created}, nil
}
