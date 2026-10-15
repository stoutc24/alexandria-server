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

// ---- Change access ----------------------------------------------------------
//
// GET /api/v1/users answers each account's permissions (canDownload, …) and
// assignedLibraries [{id}]. PUT /api/v1/users/{id} {name, email,
// permissions, assignedLibraries} (admin) sets ALL of them — the name and
// email too, and every permission flag, a missing one becoming false — so
// the account's own go back as read, with canDownload changed (demoUser is
// in the answer only, so it stays out). At least one library: Grimmory has
// no "every library" setting.

func (grimmoryProvider) AccessCapability() AccessCapability {
	return AccessCapability{Libraries: true, Download: true}
}

type grimmoryUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	// As the account has them, null included: they go back as they are.
	Name              any            `json:"name"`
	Email             any            `json:"email"`
	Permissions       map[string]any `json:"permissions"`
	AssignedLibraries []struct {
		ID int64 `json:"id"`
	} `json:"assignedLibraries"`
}

// grimmoryAccount is the account to read or change: never an admin.
func grimmoryAccount(ctx context.Context, c *client, username string) (grimmoryUser, error) {
	var users []grimmoryUser
	if err := c.call(ctx, http.MethodGet, "/api/v1/users", nil, &users); err != nil {
		return grimmoryUser{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Username, username) {
			continue
		}
		if truthy(u.Permissions["admin"]) || truthy(u.Permissions["isAdmin"]) {
			return grimmoryUser{}, ErrAdminAccount
		}
		return u, nil
	}
	return grimmoryUser{}, ErrNoAccount
}

func grimmoryAccess(ctx context.Context, c *client, username string) (AccessState, error) {
	u, err := grimmoryAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	all, err := grimmoryLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	libs := []string{}
	for _, l := range u.AssignedLibraries {
		libs = append(libs, strconv.FormatInt(l.ID, 10))
	}
	rest := map[string]any{"name": u.Name, "email": u.Email}
	restOf(rest, "permissions.", u.Permissions, "canDownload")
	return AccessState{Available: all, rest: rest,
		Access: Access{Libraries: libs, Download: truthy(u.Permissions["canDownload"])}}, nil
}

func (grimmoryProvider) GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error) {
	c, err := grimmorySignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	return grimmoryAccess(ctx, c, username)
}

func (grimmoryProvider) SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error) {
	c, err := grimmorySignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	u, err := grimmoryAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	if len(u.Permissions) == 0 {
		// Sent without them, every one of its rights would become false.
		return AccessState{}, &ServiceError{Status: http.StatusBadGateway,
			Message: "Grimmory's answer left out the account's permissions; nothing was changed"}
	}
	all, err := grimmoryLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	chosen, err := chosenIDs(all, want)
	if err != nil {
		return AccessState{}, err
	}
	ids := []int64{}
	for _, id := range chosen {
		n, _ := strconv.ParseInt(id, 10, 64)
		ids = append(ids, n)
	}
	if len(ids) == 0 {
		return AccessState{}, ErrNoLibraries
	}
	perms := map[string]any{}
	for k, v := range u.Permissions {
		if k != "demoUser" && k != "isDemoUser" {
			perms[k] = v
		}
	}
	perms["canDownload"] = want.Download
	if err := c.call(ctx, http.MethodPut, "/api/v1/users/"+strconv.FormatInt(u.ID, 10), map[string]any{
		"name":              u.Name,
		"email":             u.Email,
		"permissions":       perms,
		"assignedLibraries": ids,
	}, nil); err != nil {
		return AccessState{}, err
	}
	return grimmoryAccess(ctx, c, username)
}

// truthy reads a permission flag: JSON true, or a non-zero number —
// Storyteller keeps its flags in SQLite and sends them as 1 and 0 (owner's
// server, 2026-10-14: "cannot unmarshal number into … permissions.bookCreate
// of type bool").
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x == "true" || x == "1"
	}
	return false
}
