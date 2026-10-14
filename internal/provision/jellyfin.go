package provision

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Jellyfin (jellyfin/jellyfin Jellyfin.Api/Controllers/UserController.cs,
// read 2026-10-14):
//   - POST /Users/AuthenticateByName {Username, Pw} → {AccessToken}, sent
//     with the client header Jellyfin 12 still accepts (Authorization:
//     MediaBrowser …, then Token="…" on it);
//   - GET /Users → [{Id, Name, Policy: {IsAdministrator}}];
//   - POST /Users/New (elevated) {Name, Password}. New users get every
//     library, playback and downloads by default (AddDefaultPermissions)
//     and are hidden from the login screen;
//   - POST /Users/{id}/Password {NewPw, ResetPassword: false}: an
//     administrator changing another user needs no current password, and
//     that user's other sessions are signed out. The {id} path is the
//     form every version since 10.8 answers.
type jellyfinProvider struct{}

func (jellyfinProvider) Capability() Capability {
	return Capability{Type: "jellyfin", Create: true, Reset: true, Email: EmailOptional}
}

const jellyfinClient = `MediaBrowser Client="Alexandria Server", Device="Alexandria Server", DeviceId="alexandria-server-accounts", Version="1.0"`

func (jellyfinProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c := newClient(hc, admin.BaseURL)
	c.header.Set("Authorization", jellyfinClient)
	var login struct {
		AccessToken string `json:"AccessToken"`
	}
	if err := c.call(ctx, http.MethodPost, "/Users/AuthenticateByName",
		map[string]string{"Username": admin.Username, "Pw": admin.Password}, &login); err != nil {
		return Result{}, signIn(err)
	}
	if login.AccessToken == "" {
		return Result{}, &SignInError{Err: errors.New("Jellyfin gave no token")}
	}
	c.header.Set("Authorization", fmt.Sprintf(`%s, Token="%s"`, jellyfinClient, login.AccessToken))
	// Leave no admin session behind.
	defer func() { _ = c.call(context.WithoutCancel(ctx), http.MethodPost, "/Sessions/Logout", nil, nil) }()

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

	if err := c.call(ctx, http.MethodPost, "/Users/New",
		map[string]string{"Name": p.Username, "Password": p.Password}, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created}, nil
}
