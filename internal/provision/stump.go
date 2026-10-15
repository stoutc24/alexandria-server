package provision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
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
//     they are, with the new password;
//     libraries(pagination) { nodes { id name excludedUsers { id } } };
//     a person sees every library they are not kept out of, and
//     updateLibraryExcludedUsers(id, userIds) REPLACES a library's list
//     (MANAGE_LIBRARY and READ_USERS; never the owner or oneself).
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

func stumpSignIn(ctx context.Context, hc *http.Client, admin Admin) (*client, error) {
	c := newClient(hc, admin.BaseURL)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	if err := c.call(ctx, http.MethodPost, "/api/v2/auth/login?generate_token=true&create_session=false",
		map[string]string{"username": admin.Username, "password": admin.Password}, &login); err != nil {
		return nil, signIn(err)
	}
	if login.AccessToken == "" {
		return nil, &SignInError{Err: errors.New("Stump gave no token")}
	}
	c.header.Set("Authorization", "Bearer "+login.AccessToken)
	return c, nil
}

// stumpLibrary is a library and who is kept out of it.
type stumpLibrary struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ExcludedUsers []struct {
		ID string `json:"id"`
	} `json:"excludedUsers"`
}

func stumpLibraries(ctx context.Context, c *client) ([]stumpLibrary, error) {
	var out struct {
		Libraries struct {
			Nodes []stumpLibrary `json:"nodes"`
		} `json:"libraries"`
	}
	if err := stumpQuery(ctx, c, `query { libraries(pagination: {offset: {page: 1, pageSize: 1000, zeroBased: false}}) {
		nodes { id name excludedUsers { id } } } }`, nil, &out); err != nil {
		return nil, err
	}
	return out.Libraries.Nodes, nil
}

func stumpLibraryList(libs []stumpLibrary) []Library {
	out := make([]Library, 0, len(libs))
	for _, l := range libs {
		out = append(out, Library{ID: l.ID, Name: l.Name})
	}
	return out
}

func (stumpProvider) Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error) {
	c, err := stumpSignIn(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	libs, err := stumpLibraries(ctx, c)
	if err != nil {
		return nil, err
	}
	return stumpLibraryList(libs), nil
}

func (stumpProvider) Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error) {
	c, err := stumpSignIn(ctx, hc, admin)
	if err != nil {
		return Result{}, err
	}

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

	// Stump shows a person every library they are not kept out of: the
	// ones not picked are found (and the pick checked) before anything is
	// made.
	var hide []stumpLibrary
	if p.Libraries != nil {
		libs, err := stumpLibraries(ctx, c)
		if err != nil {
			return Result{}, err
		}
		chosen, err := picked(stumpLibraryList(libs), p.Libraries)
		if err != nil {
			return Result{}, err
		}
		for _, l := range libs {
			if !slices.Contains(chosen, l.ID) {
				hide = append(hide, l)
			}
		}
	}
	var made struct {
		CreateUser struct {
			ID string `json:"id"`
		} `json:"createUser"`
	}
	if err := stumpQuery(ctx, c, `mutation($input: CreateUserInput!) { createUser(input: $input) { id } }`,
		map[string]any{"input": map[string]any{
			"username":    p.Username,
			"password":    p.Password,
			"permissions": []string{"DOWNLOAD_FILE", "CHANGE_PASSWORD", "ACCESS_KOREADER_SYNC"},
		}}, &made); err != nil {
		return Result{}, err
	}
	// Each library's whole list of who is kept out goes back, with them on it.
	var missed []string
	for _, l := range hide {
		ids := []string{}
		for _, u := range l.ExcludedUsers {
			ids = append(ids, u.ID)
		}
		err := errors.New("Stump did not say which account it made")
		if made.CreateUser.ID != "" {
			err = stumpQuery(ctx, c, `mutation($id: ID!, $userIds: [String!]!) { updateLibraryExcludedUsers(id: $id, userIds: $userIds) { id } }`,
				map[string]any{"id": l.ID, "userIds": append(ids, made.CreateUser.ID)}, nil)
		}
		if err != nil {
			missed = append(missed, l.Name)
		}
	}
	if len(missed) > 0 {
		return Result{Outcome: Created,
			Note: "The account was made, but Stump did not hide " + strings.Join(missed, ", ") +
				" from it: it can see them until you change that in Stump."}, nil
	}
	return Result{Outcome: Created}, nil
}

// ---- Change access ----------------------------------------------------------
//
// DOWNLOAD_FILE in an account's permission list is downloading:
// updateUser takes the whole list back, with the age limit (left out, it
// is deleted) and the session cap, and no password, so the password stays.
// An account sees every library it is not kept out of, so a library added
// later is seen by everyone until someone is kept out of it; each library
// whose list has to change gets its whole list back with the account put
// on or taken off.

func (stumpProvider) AccessCapability() AccessCapability {
	return AccessCapability{Libraries: true, Download: true, LaterLibraries: true}
}

// stumpAccount is the account to read or change: never the server owner
// or one that manages users or the server.
func stumpAccount(ctx context.Context, c *client, username string) (stumpUser, error) {
	var list struct {
		Users struct {
			Nodes []stumpUser `json:"nodes"`
		} `json:"users"`
	}
	if err := stumpQuery(ctx, c, `query { users(pagination: {offset: {page: 1, pageSize: 1000, zeroBased: false}}) {
		nodes { id username isServerOwner permissions maxSessionsAllowed ageRestriction { age restrictOnUnset } } } }`,
		nil, &list); err != nil {
		return stumpUser{}, err
	}
	for _, u := range list.Users.Nodes {
		if !strings.EqualFold(u.Username, username) {
			continue
		}
		if u.IsServerOwner || slices.Contains(u.Permissions, "MANAGE_USERS") || slices.Contains(u.Permissions, "MANAGE_SERVER") {
			return stumpUser{}, ErrAdminAccount
		}
		return u, nil
	}
	return stumpUser{}, ErrNoAccount
}

func stumpKeptOut(l stumpLibrary, userID string) bool {
	for _, u := range l.ExcludedUsers {
		if u.ID == userID {
			return true
		}
	}
	return false
}

func stumpAccess(ctx context.Context, c *client, username string) (AccessState, error) {
	u, err := stumpAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	libs, err := stumpLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	sees := []string{}
	for _, l := range libs {
		if !stumpKeptOut(l, u.ID) {
			sees = append(sees, l.ID)
		}
	}
	rest := map[string]any{"permissions": sortedWithout(u.Permissions, "DOWNLOAD_FILE"), "maxSessionsAllowed": nil, "ageRestriction": nil}
	if u.MaxSessionsAllowed != nil {
		rest["maxSessionsAllowed"] = *u.MaxSessionsAllowed
	}
	if u.AgeRestriction != nil {
		rest["ageRestriction"] = *u.AgeRestriction
	}
	return AccessState{Available: stumpLibraryList(libs), rest: rest, Access: Access{
		Libraries: sees,
		Download:  slices.Contains(u.Permissions, "DOWNLOAD_FILE"),
	}}, nil
}

func (stumpProvider) GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error) {
	c, err := stumpSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	return stumpAccess(ctx, c, username)
}

func (stumpProvider) SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error) {
	c, err := stumpSignIn(ctx, hc, admin)
	if err != nil {
		return AccessState{}, err
	}
	u, err := stumpAccount(ctx, c, username)
	if err != nil {
		return AccessState{}, err
	}
	libs, err := stumpLibraries(ctx, c)
	if err != nil {
		return AccessState{}, err
	}
	// The pick is checked before anything changes.
	keep, err := chosenIDs(stumpLibraryList(libs), want)
	if err != nil {
		return AccessState{}, err
	}

	if slices.Contains(u.Permissions, "DOWNLOAD_FILE") != want.Download {
		perms := []string{}
		for _, p := range u.Permissions {
			if p != "DOWNLOAD_FILE" {
				perms = append(perms, p)
			}
		}
		if want.Download {
			perms = append(perms, "DOWNLOAD_FILE")
		}
		input := map[string]any{
			"username":           u.Username,
			"permissions":        perms,
			"maxSessionsAllowed": u.MaxSessionsAllowed,
		}
		if u.AgeRestriction != nil {
			input["ageRestriction"] = map[string]any{"age": u.AgeRestriction.Age, "restrictOnUnset": u.AgeRestriction.RestrictOnUnset}
		}
		if err := stumpQuery(ctx, c, `mutation($id: ID!, $input: UpdateUserInput!) { updateUser(id: $id, input: $input) { id } }`,
			map[string]any{"id": u.ID, "input": input}, nil); err != nil {
			return AccessState{}, err
		}
	}

	for _, l := range libs {
		out := !slices.Contains(keep, l.ID)
		if stumpKeptOut(l, u.ID) == out {
			continue
		}
		ids := []string{}
		for _, x := range l.ExcludedUsers {
			if x.ID != u.ID {
				ids = append(ids, x.ID)
			}
		}
		if out {
			ids = append(ids, u.ID)
		}
		if err := stumpQuery(ctx, c, `mutation($id: ID!, $userIds: [String!]!) { updateLibraryExcludedUsers(id: $id, userIds: $userIds) { id } }`,
			map[string]any{"id": l.ID, "userIds": ids}, nil); err != nil {
			return AccessState{}, err
		}
	}
	return stumpAccess(ctx, c, username)
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
