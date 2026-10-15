package provision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// Komga (gotson/komga interfaces/api/rest/UserController.kt,
// LibraryController.kt and dto/UserCreationDto.kt, read 2026-10-14).
// People sign in with an EMAIL address, so the username is one. Basic
// authentication on every call.
//   - GET /api/v2/users (ADMIN) → [{id, email, roles}];
//   - GET /api/v1/libraries → [{id, name}] (every one, for an admin);
//   - POST /api/v2/users (ADMIN) {email, password, roles, sharedLibraries}
//     — no sharedLibraries means every library, {all: false, libraryIds}
//     only those; roles are sent, since an empty list would give none (the
//     defaults are FILE_DOWNLOAD and PAGE_STREAMING);
//   - PATCH /api/v2/users/{id}/password (ADMIN) {password}.
type komgaProvider struct{}

func (komgaProvider) Capability() Capability {
	return Capability{Type: "komga", Create: true, Reset: true, Email: EmailUsername,
		Note: "Komga signs people in with an email address."}
}

func komgaClient(hc *http.Client, admin Admin) *client {
	c := newClient(hc, admin.BaseURL)
	req, _ := http.NewRequest(http.MethodGet, "http://x", nil)
	req.SetBasicAuth(admin.Username, admin.Password)
	c.header.Set("Authorization", req.Header.Get("Authorization"))
	return c
}

func komgaLibraries(ctx context.Context, c *client) ([]Library, error) {
	var list []Library
	if err := c.call(ctx, http.MethodGet, "/api/v1/libraries", nil, &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (komgaProvider) Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error) {
	list, err := komgaLibraries(ctx, komgaClient(hc, admin))
	if err != nil {
		var se *ServiceError
		if errors.As(err, &se) && se.Status == http.StatusUnauthorized {
			return nil, signIn(err)
		}
		return nil, err
	}
	return list, nil
}

func (komgaProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c := komgaClient(hc, admin)

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

	body := map[string]any{
		"email":    p.Username,
		"password": p.Password,
		"roles":    []string{"FILE_DOWNLOAD", "PAGE_STREAMING"},
	}
	if p.Libraries != nil {
		all, err := komgaLibraries(ctx, c)
		if err != nil {
			return Result{}, err
		}
		ids, err := picked(all, p.Libraries)
		if err != nil {
			return Result{}, err
		}
		body["sharedLibraries"] = map[string]any{"all": false, "libraryIds": ids}
	}
	if err := c.call(ctx, http.MethodPost, "/api/v2/users", body, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created}, nil
}

// ---- Change access ----------------------------------------------------------
//
// GET /api/v2/users answers each account's roles, sharedAllLibraries and
// sharedLibrariesIds. PATCH /api/v2/users/{id} (ADMIN, never oneself)
// changes only the fields it is sent: roles, the whole set (FILE_DOWNLOAD
// is downloading; the "USER" every list carries is no role, and Komga
// drops what it does not know), and sharedLibraries {all, libraryIds}.

func (komgaProvider) AccessCapability() AccessCapability {
	return AccessCapability{Libraries: true, Download: true, LaterLibraries: true, allFlag: true}
}

type komgaUser struct {
	ID                 string   `json:"id"`
	Email              string   `json:"email"`
	Roles              []string `json:"roles"`
	SharedAllLibraries bool     `json:"sharedAllLibraries"`
	SharedLibrariesIDs []string `json:"sharedLibrariesIds"`
	// Its other limits, which a change must leave as they are.
	LabelsAllow    []string        `json:"labelsAllow"`
	LabelsExclude  []string        `json:"labelsExclude"`
	AgeRestriction json.RawMessage `json:"ageRestriction"`
}

// komgaAccount is the account to read or change: never an ADMIN. Komga
// checks the sign-in on every call, so a 401 here is the admin's.
func komgaAccount(ctx context.Context, c *client, username string) (komgaUser, error) {
	var users []komgaUser
	if err := c.call(ctx, http.MethodGet, "/api/v2/users", nil, &users); err != nil {
		var se *ServiceError
		if errors.As(err, &se) && se.Status == http.StatusUnauthorized {
			return komgaUser{}, signIn(err)
		}
		return komgaUser{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Email, username) {
			continue
		}
		if hasRole(u.Roles, "ADMIN") {
			return komgaUser{}, ErrAdminAccount
		}
		return u, nil
	}
	return komgaUser{}, ErrNoAccount
}

func komgaAccess(ctx context.Context, c *client, username string) (AccessState, error) {
	u, err := komgaAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	all, err := komgaLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	rest := map[string]any{
		"roles":          sortedWithout(u.Roles, "FILE_DOWNLOAD", "USER"),
		"labelsAllow":    sortedWithout(u.LabelsAllow),
		"labelsExclude":  sortedWithout(u.LabelsExclude),
		"ageRestriction": parsed(u.AgeRestriction),
	}
	return AccessState{Available: all, rest: rest, Access: Access{
		AllLibraries: u.SharedAllLibraries,
		Libraries:    u.SharedLibrariesIDs,
		Download:     hasRole(u.Roles, "FILE_DOWNLOAD"),
	}}, nil
}

func (komgaProvider) GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error) {
	return komgaAccess(ctx, komgaClient(hc, admin), username)
}

func (komgaProvider) SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error) {
	c := komgaClient(hc, admin)
	u, err := komgaAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	roles := []string{}
	for _, r := range u.Roles {
		if r != "FILE_DOWNLOAD" && r != "USER" {
			roles = append(roles, r)
		}
	}
	if want.Download {
		roles = append(roles, "FILE_DOWNLOAD")
	}
	shared := map[string]any{"all": true, "libraryIds": []string{}}
	if !want.AllLibraries {
		all, err := komgaLibraries(ctx, c)
		if err != nil {
			return AccessState{}, err
		}
		ids, err := picked(all, want.Libraries)
		if err != nil {
			return AccessState{}, err
		}
		shared = map[string]any{"all": false, "libraryIds": ids}
	}
	if err := c.call(ctx, http.MethodPatch, "/api/v2/users/"+url.PathEscape(u.ID),
		map[string]any{"roles": roles, "sharedLibraries": shared}, nil); err != nil {
		return AccessState{}, err
	}
	return komgaAccess(ctx, c, username)
}
