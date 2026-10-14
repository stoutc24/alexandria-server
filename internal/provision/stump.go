package provision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Stump (stumpapp/stump apps/server/src/routers/api/v2/auth.rs and
// crates/graphql, read 2026-10-14). Users are managed over GraphQL.
//   - POST /api/v2/auth/login?generate_token=true&create_session=false
//     {username, password} → {accessToken, …};
//   - POST /api/graphql with "Authorization: Bearer …":
//     users(pagination) { nodes { id username isServerOwner permissions
//     maxSessionsAllowed ageRestriction { age restrictOnUnset } } };
//     createUser(input: {username, password, permissions}) needs
//     MANAGE_USERS; updateUser(id, input) takes the whole permission list
//     (and age restriction and session cap), so those are sent back as
//     they are, with the new password.
type stumpProvider struct{}

func (stumpProvider) Capability() Capability {
	return Capability{Type: "stump", Create: true, Reset: true, Email: EmailOptional}
}

type stumpUser struct {
	ID                 string   `json:"id"`
	Username           string   `json:"username"`
	IsServerOwner      bool     `json:"isServerOwner"`
	Permissions        []string `json:"permissions"`
	MaxSessionsAllowed *int     `json:"maxSessionsAllowed"`
	AgeRestriction     *struct {
		Age             int  `json:"age"`
		RestrictOnUnset bool `json:"restrictOnUnset"`
	} `json:"ageRestriction"`
}

func (stumpProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/v2/auth/login?generate_token=true&create_session=false",
		map[string]string{"username": admin.Username, "password": admin.Password}, &login); err != nil {
		return Result{}, signIn(err)
	}
	if login.AccessToken == "" {
		return Result{}, &SignInError{Err: errors.New("Stump gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+login.AccessToken)

	var list struct {
		Users struct {
			Nodes []stumpUser `json:"nodes"`
		} `json:"users"`
	}
	if err := stumpQuery(ctx, c, `query { users(pagination: {offset: {page: 1, pageSize: 1000, zeroBased: false}}) {
		nodes { id username isServerOwner permissions maxSessionsAllowed ageRestriction { age restrictOnUnset } } } }`,
		nil, &list); err != nil {
		return Result{}, err
	}
	for _, u := range list.Users.Nodes {
		if !strings.EqualFold(u.Username, p.Username) {
			continue
		}
		if u.IsServerOwner {
			return Result{}, ErrAdminAccount
		}
		for _, perm := range u.Permissions {
			if perm == "MANAGE_USERS" || perm == "MANAGE_SERVER" {
				return Result{}, ErrAdminAccount
			}
		}
		if !allowReset {
			return Result{}, ErrExists
		}
		input := map[string]any{
			"username":           u.Username,
			"password":           p.Password,
			"permissions":        nonNil(u.Permissions),
			"maxSessionsAllowed": u.MaxSessionsAllowed,
		}
		if u.AgeRestriction != nil {
			input["ageRestriction"] = map[string]any{"age": u.AgeRestriction.Age, "restrictOnUnset": u.AgeRestriction.RestrictOnUnset}
		}
		if err := stumpQuery(ctx, c, `mutation($id: ID!, $input: UpdateUserInput!) { updateUser(id: $id, input: $input) { id } }`,
			map[string]any{"id": u.ID, "input": input}, nil); err != nil {
			return Result{}, err
		}
		return Result{Outcome: Reset}, nil
	}

	if err := stumpQuery(ctx, c, `mutation($input: CreateUserInput!) { createUser(input: $input) { id } }`,
		map[string]any{"input": map[string]any{
			"username":    p.Username,
			"password":    p.Password,
			"permissions": []string{"DOWNLOAD_FILE", "CHANGE_PASSWORD", "ACCESS_KOREADER_SYNC"},
		}}, nil); err != nil {
		return Result{}, err
	}
	return Result{Outcome: Created}, nil
}

// stumpQuery runs one GraphQL document; GraphQL's own errors (answered
// with 200) become a ServiceError.
func stumpQuery(ctx context.Context, c *client, query string, vars map[string]any, out any) error {
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	body := map[string]any{"query": query}
	if vars != nil {
		body["variables"] = vars
	}
	if err := c.call(ctx, http.MethodPost, "/api/graphql", body, &res); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, 0, len(res.Errors))
		for _, e := range res.Errors {
			msgs = append(msgs, e.Message)
		}
		return &ServiceError{Status: http.StatusBadRequest, Message: strings.Join(msgs, "; ")}
	}
	if out != nil && len(res.Data) > 0 {
		return json.Unmarshal(res.Data, out)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
