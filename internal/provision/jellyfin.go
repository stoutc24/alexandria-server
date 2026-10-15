package provision

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Jellyfin (jellyfin/jellyfin Jellyfin.Api/Controllers/UserController.cs
// and LibraryController.cs, and jellyfin-web's add-user page, read
// 2026-10-14):
//   - POST /Users/AuthenticateByName {Username, Pw} → {AccessToken}, sent
//     with the client header Jellyfin 12 still accepts (Authorization:
//     MediaBrowser …, then Token="…" on it);
//   - GET /Users → [{Id, Name, Policy: {IsAdministrator}}];
//   - GET /Library/MediaFolders?isHidden=false (elevated) → {Items: [{Id,
//     Name}]}: the libraries, by the ids a policy's EnabledFolders takes;
//   - POST /Users/New (elevated) {Name, Password} → the user with its whole
//     Policy. New users get every library, playback and downloads by
//     default (AddDefaultPermissions) and are hidden from the login screen;
//   - POST /Users/{id}/Policy (elevated) takes the WHOLE policy back, as
//     Jellyfin's own add-user page does after /Users/New, with
//     EnableAllFolders off and EnabledFolders set;
//   - POST /Users/{id}/Password {NewPw, ResetPassword: false}: an
//     administrator changing another user needs no current password, and
//     that user's other sessions are signed out. The {id} path is the
//     form every version since 10.8 answers.
type jellyfinProvider struct{}

func (jellyfinProvider) Capability() Capability {
	return Capability{Type: "jellyfin", Create: true, Reset: true, Email: EmailOptional}
}

const jellyfinClient = `MediaBrowser Client="Alexandria Server", Device="Alexandria Server", DeviceId="alexandria-server-accounts", Version="1.0"`

// jellyfinSignIn signs the admin in; done signs that session out again.
func jellyfinSignIn(ctx context.Context, hc *http.Client, admin Admin) (c *client, done func(), err error) {
	c = newClient(hc, admin.BaseURL)
	c.header.Set("Authorization", jellyfinClient)
	var login struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := c.call(ctx, http.MethodPost, "/Users/AuthenticateByName",
		map[string]string{"Username": admin.Username, "Pw": admin.Password}, &login); err != nil {
		return nil, nil, signIn(err)
	}
	if login.AccessToken == "" {
		return nil, nil, &SignInError{Err: errors.New("Jellyfin gave no token")}
	}
	c.header.Set("Authorization", fmt.Sprintf(`%s, Token="%s"`, jellyfinClient, login.AccessToken))
	// Leave no admin session behind.
	done = func() { _ = c.call(context.WithoutCancel(ctx), http.MethodPost, "/Sessions/Logout", nil, nil) }
	return c, done, nil
}

func jellyfinLibraries(ctx context.Context, c *client) ([]Library, error) {
	var out struct {
		Items []struct {
			ID   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"Items"`
	}
	if err := c.call(ctx, http.MethodGet, "/Library/MediaFolders?isHidden=false", nil, &out); err != nil {
		return nil, err
	}
	list := make([]Library, 0, len(out.Items))
	for _, f := range out.Items {
		list = append(list, Library{ID: f.ID, Name: f.Name})
	}
	return list, nil
}

func (jellyfinProvider) Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error) {
	c, done, err := jellyfinSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	defer done()
	return jellyfinLibraries(ctx, c)
}

func (jellyfinProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c, done, err := jellyfinSignIn(ctx, hc, admin)
	if err != nil {
		return Result{}, err
	}
	defer done()

	var users []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.call(ctx, http.MethodGet, "/Users", nil, &users); err != nil {
		return Result{}, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Name, p.Username) {
			continue
		}
		if u.Policy.IsAdministrator {
			return Result{}, ErrAdminAccount
		}
		if !allowReset {
			return Result{}, ErrExists
		}
		if err := c.call(ctx, http.MethodPost, "/Users/"+url.PathEscape(u.ID)+"/Password",
			map[string]any{"CurrentPw": "", "NewPw": p.Password, "ResetPassword": false}, nil); err != nil {
			return Result{}, err
		}
		return Result{Outcome: Reset, Note: "Jellyfin signed this account out of its other sessions."}, nil
	}

	// The picked libraries are checked before anything is made.
	var folders []string
	if p.Libraries != nil {
		all, err := jellyfinLibraries(ctx, c)
		if err != nil {
			return Result{}, err
		}
		if folders, err = picked(all, p.Libraries); err != nil {
			return Result{}, err
		}
	}
	var made struct {
		ID     string         `json:"Id"`
		Policy map[string]any `json:"Policy"`
	}
	if err := c.call(ctx, http.MethodPost, "/Users/New",
		map[string]string{"Name": p.Username, "Password": p.Password}, &made); err != nil {
		return Result{}, err
	}
	if folders == nil {
		return Result{Outcome: Created}, nil
	}
	if err := jellyfinLimitFolders(ctx, c, made.ID, made.Policy, folders); err != nil {
		return Result{Outcome: Created,
			Note: "The account was made, but Jellyfin did not take the libraries picked (" + err.Error() +
				"): it can see every library until you change that in Jellyfin."}, nil
	}
	return Result{Outcome: Created}, nil
}

// ---- Change access ----------------------------------------------------------
//
// GET /Users/{id} answers the account's whole Policy
// (EnableContentDownloading, EnableAllFolders, EnabledFolders, …), and
// POST /Users/{id}/Policy takes it back whole, with those changed. Library
// ids are compared without dashes and in lower case: Jellyfin writes the
// same id both ways in different answers, and reads either.

func (jellyfinProvider) AccessCapability() AccessCapability {
	return AccessCapability{Libraries: true, Download: true, LaterLibraries: true, allFlag: true}
}

func jellyfinID(id string) string { return strings.ToLower(strings.ReplaceAll(id, "-", "")) }

func (jellyfinProvider) libraryID(id string) string { return jellyfinID(id) }

// jellyfinAccount is the account to read or change, never an
// administrator, and its whole policy.
func jellyfinAccount(ctx context.Context, c *client, username string) (string, map[string]any, error) {
	var users []struct {
		ID     string `json:"Id"`
		Name   string `json:"Name"`
		Policy struct {
			IsAdministrator bool `json:"IsAdministrator"`
		} `json:"Policy"`
	}
	if err := c.call(ctx, http.MethodGet, "/Users", nil, &users); err != nil {
		return "", nil, err
	}
	for _, u := range users {
		if !strings.EqualFold(u.Name, username) {
			continue
		}
		if u.Policy.IsAdministrator {
			return "", nil, ErrAdminAccount
		}
		var user struct {
			Policy map[string]any `json:"Policy"`
		}
		if err := c.call(ctx, http.MethodGet, "/Users/"+url.PathEscape(u.ID), nil, &user); err != nil {
			return "", nil, err
		}
		if user.Policy == nil {
			return "", nil, &ServiceError{Status: http.StatusBadGateway, Message: "the account's settings could not be read"}
		}
		if truthy(user.Policy["IsAdministrator"]) {
			return "", nil, ErrAdminAccount
		}
		return u.ID, user.Policy, nil
	}
	return "", nil, ErrNoAccount
}

func jellyfinAccessLibraries(ctx context.Context, c *client) ([]Library, error) {
	all, err := jellyfinLibraries(ctx, c)
	if err != nil {
		return nil, err
	}
	for i := range all {
		all[i].ID = jellyfinID(all[i].ID)
	}
	return all, nil
}

func jellyfinAccess(ctx context.Context, c *client, username string) (AccessState, error) {
	_, policy, err := jellyfinAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	all, err := jellyfinAccessLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	libs := []string{}
	if list, ok := policy["EnabledFolders"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				libs = append(libs, jellyfinID(s))
			}
		}
	}
	rest := map[string]any{}
	restOf(rest, "Policy.", policy, "EnableContentDownloading", "EnableAllFolders", "EnabledFolders")
	return AccessState{Available: all, rest: rest, Access: Access{
		AllLibraries: truthy(policy["EnableAllFolders"]),
		Libraries:    libs,
		Download:     truthy(policy["EnableContentDownloading"]),
	}}, nil
}

func (jellyfinProvider) GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error) {
	c, done, err := jellyfinSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	defer done()
	return jellyfinAccess(ctx, c, username)
}

func (jellyfinProvider) SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error) {
	c, done, err := jellyfinSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	defer done()
	id, policy, err := jellyfinAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	folders := []string{}
	if !want.AllLibraries {
		all, err := jellyfinAccessLibraries(ctx, c)
		if err != nil {
			return AccessState{}, err
		}
		chosen := make([]string, 0, len(want.Libraries))
		for _, l := range want.Libraries {
			chosen = append(chosen, jellyfinID(l))
		}
		if folders, err = picked(all, chosen); err != nil {
			return AccessState{}, err
		}
	}
	policy["EnableContentDownloading"] = want.Download
	policy["EnableAllFolders"] = want.AllLibraries
	policy["EnabledFolders"] = folders
	if err := c.call(ctx, http.MethodPost, "/Users/"+url.PathEscape(id)+"/Policy", policy, nil); err != nil {
		return AccessState{}, err
	}
	return jellyfinAccess(ctx, c, username)
}

// jellyfinLimitFolders gives a new account only these libraries: its whole
// policy back, as Jellyfin's own add-user page sends it.
func jellyfinLimitFolders(ctx context.Context, c *client, id string, policy map[string]any, folders []string) error {
	if id == "" {
		return errors.New("Jellyfin did not say which account it made")
	}
	if policy == nil {
		var user struct {
			Policy map[string]any `json:"Policy"`
		}
		if err := c.call(ctx, http.MethodGet, "/Users/"+url.PathEscape(id), nil, &user); err != nil {
			return err
		}
		if policy = user.Policy; policy == nil {
			return errors.New("the account's settings could not be read")
		}
	}
	policy["EnableAllFolders"] = false
	policy["EnabledFolders"] = folders
	return c.call(ctx, http.MethodPost, "/Users/"+url.PathEscape(id)+"/Policy", policy, nil)
}
