package provision

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Navidrome (navidrome/navidrome persistence/user_repository.go,
// server/nativeapi, cmd/user.go, read 2026-10-14):
//   - POST /auth/login {username, password} → {token}; the native API
//     takes it as "X-ND-Authorization: Bearer …";
//   - GET /api/user (admin) → [{id, userName, isAdmin}];
//   - POST /api/user (admin) {userName, name, email, password, isAdmin};
//   - libraries (GET /api/library → [{id, name}]): only admins get every
//     library by themselves (a new account gets the ones marked for new
//     users), so a new account is given the picked ones, else them all
//     (PUT /api/user/{id}/library {libraryIds}, at least one), as
//     Navidrome's own "user create" command does. Servers from before
//     multiple libraries answer 404 there and need nothing;
//   - PUT /api/user/{id} replaces the WHOLE user, so the current one is
//     read first and sent back with the new password; an admin changing
//     someone else needs no current password.
type navidromeProvider struct{}

func (navidromeProvider) Capability() Capability {
	return Capability{Type: "navidrome", Create: true, Reset: true, Email: EmailOptional}
}

func navidromeSignIn(ctx context.Context, hc *http.Client, admin Admin) (*client, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, http.MethodPost, "/auth/login",
		map[string]string{"username": admin.Username, "password": admin.Password}, &login); err != nil {
		return nil, signIn(err)
	}
	if login.Token == "" {
		return nil, &SignInError{Err: errors.New("Navidrome gave no token")}
	}
	c.header.Set("X-ND-Authorization", "Bearer "+login.Token)
	return c, nil
}

// navidromeLibraries is every library; none on a server from before
// multiple libraries (404), which has one.
func navidromeLibraries(ctx context.Context, c *client) ([]Library, error) {
	var libraries []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/library", nil, &libraries); err != nil {
		var se *ServiceError
		if errors.As(err, &se) && (se.Status == http.StatusNotFound || se.Status == http.StatusMethodNotAllowed) {
			return []Library{}, nil
		}
		return nil, err
	}
	out := make([]Library, 0, len(libraries))
	for _, l := range libraries {
		out = append(out, Library{ID: strconv.Itoa(l.ID), Name: l.Name})
	}
	return out, nil
}

func (navidromeProvider) Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error) {
	c, err := navidromeSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	return navidromeLibraries(ctx, c)
}

func (navidromeProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c, err := navidromeSignIn(ctx, hc, admin)
	if err != nil {
		return Result{}, err
	}

	var users []struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/user", nil, &users); err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.UserName, p.Username) {
			continue
		}
		if u.IsAdmin {
			return Result{}, ErrAdminAccount
		}
		if !allowReset {
			return Result{}, ErrExists
		}
		path := "/api/user/" + url.PathEscape(u.ID)
		var current map[string]any
		if err := c.call(ctx, http.MethodGet, path, nil, &current); err != nil {
			return Result{}, err
		}
		if current == nil {
			return Result{}, &ServiceError{Status: http.StatusNotFound, Message: "the account could not be read"}
		}
		current["password"] = p.Password
		delete(current, "currentPassword")
		if err := c.call(ctx, http.MethodPut, path, current, nil); err != nil {
			return Result{}, err
		}
		return Result{Outcome: Reset}, nil
	}

	// The libraries are read (and the picked ones checked) before anything
	// is made.
	all, err := navidromeLibraries(ctx, c)
	if err != nil {
		return Result{}, err
	}
	var ids []int
	if len(all) > 0 {
		chosen, err := picked(all, p.Libraries)
		if err != nil {
			return Result{}, err
		}
		for _, l := range all {
			if chosen == nil || slices.Contains(chosen, l.ID) {
				id, _ := strconv.Atoi(l.ID)
				ids = append(ids, id)
			}
		}
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/user", map[string]any{
		"userName": p.Username,
		"name":     p.Name,
		"email":    p.Email,
		"password": p.Password,
		"isAdmin":  false,
	}, &created); err != nil {
		return Result{}, err
	}
	if created.ID == "" {
		// Older answers carry no id: look the new account up.
		var again []struct {
			ID       string `json:"id"`
			UserName string `json:"userName"`
		}
		if err := c.call(ctx, http.MethodGet, "/api/user", nil, &again); err == nil {
			for _, u := range again {
				if strings.EqualFold(u.UserName, p.Username) {
					created.ID = u.ID
				}
			}
		}
	}
	if len(ids) == 0 {
		return Result{Outcome: Created}, nil // one library: nothing to set
	}
	err = errors.New("Navidrome did not say which account it made")
	if created.ID != "" {
		err = c.call(ctx, http.MethodPut, "/api/user/"+url.PathEscape(created.ID)+"/library",
			map[string]any{"libraryIds": ids}, nil)
	}
	if err != nil {
		return Result{Outcome: Created,
			Note: "The account was made, but its libraries could not be set (" + err.Error() + "); pick them in Navidrome."}, nil
	}
	return Result{Outcome: Created}, nil
}

// ---- Change access ----------------------------------------------------------
//
// GET /api/user/{id}/library (admin) answers the libraries an account has;
// PUT the same {libraryIds} replaces them (at least one, never an
// admin's). Downloads are one switch for the whole server
// (EnableDownloads), not a person's, so they are not offered; and a server
// from before multiple libraries has nothing to change.

func (navidromeProvider) AccessCapability() AccessCapability {
	return AccessCapability{Libraries: true,
		Note: "Navidrome turns downloads on or off for the whole server, not for each person."}
}

// navidromeAccount is the account to read or change: never an admin.
func navidromeAccount(ctx context.Context, c *client, username string) (string, error) {
	var users []struct {
		ID       string `json:"id"`
		UserName string `json:"userName"`
		IsAdmin  bool   `json:"isAdmin"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/user", nil, &users); err != nil {
		return "", err
	}
	for _, u := range users {
		if !strings.EqualFold(u.UserName, username) {
			continue
		}
		if u.IsAdmin {
			return "", ErrAdminAccount
		}
		return u.ID, nil
	}
	return "", ErrNoAccount
}

func navidromeAccess(ctx context.Context, c *client, username string) (AccessState, error) {
	id, err := navidromeAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	all, err := navidromeLibraries(ctx, c)
	if err != nil || len(all) == 0 {
		return AccessState{Available: all}, err
	}
	var has []struct {
		ID int `json:"id"`
	}
	if err := c.call(ctx, http.MethodGet, "/api/user/"+url.PathEscape(id)+"/library", nil, &has); err != nil {
		return AccessState{}, err
	}
	libs := []string{}
	for _, l := range has {
		libs = append(libs, strconv.Itoa(l.ID))
	}
	return AccessState{Available: all, Access: Access{Libraries: libs}}, nil
}

func (navidromeProvider) GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error) {
	c, err := navidromeSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	return navidromeAccess(ctx, c, username)
}

func (navidromeProvider) SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error) {
	c, err := navidromeSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	id, err := navidromeAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	all, err := navidromeLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	if len(all) == 0 {
		return AccessState{}, ErrNothingToChange
	}
	chosen, err := chosenIDs(all, want)
	if err != nil {
		return AccessState{}, err
	}
	ids := []int{}
	for _, l := range chosen {
		n, _ := strconv.Atoi(l)
		ids = append(ids, n)
	}
	if err := c.call(ctx, http.MethodPut, "/api/user/"+url.PathEscape(id)+"/library",
		map[string]any{"libraryIds": ids}, nil); err != nil {
		return AccessState{}, err
	}
	return navidromeAccess(ctx, c, username)
}
