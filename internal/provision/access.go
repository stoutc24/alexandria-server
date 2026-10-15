package provision

// Change access (owner, 2026-10-14: "i think we should either allow the
// admins to adjust permissions and library access from the server, or
// state what permissions are added during account creation" — then "b"):
// an existing account's libraries, and whether it may download, read and
// changed with the kept admin sign-in.
//
// Only those two things change. Each service's own record of the account
// goes back as it was read, with them changed, so nothing else about it
// (its password, its other rights, its age limit) moves. The account is
// read back afterwards with the same sign-in, so a service that answers
// yes but keeps something as it was is caught (NotTakenError), not
// reported done. Admin, owner and root accounts are never changed, as with
// Ensure, and neither is the admin's own sign-in.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// Access is what an account may see and do: the part Change access sets.
type Access struct {
	// AllLibraries: every library. Otherwise Libraries, by id, says which.
	AllLibraries bool     `json:"allLibraries"`
	Libraries    []string `json:"libraries"`
	// Download: it may download files to keep.
	Download bool `json:"download"`
}

// AccessState is an account's access as the service has it, with every
// library the service has to pick from (Available, in its order; none for
// a service with one collection). With AllLibraries, Libraries names them
// all.
type AccessState struct {
	Access
	Available []Library `json:"available"`
	// rest is the account's other rights, as read: what a change must
	// leave as it was (AlsoChangedError). Never sent anywhere.
	rest map[string]any
}

// AccessCapability is what Change access can set on a service.
type AccessCapability struct {
	// Libraries: which libraries an account sees. Download: whether it may
	// download.
	Libraries bool `json:"libraries"`
	Download  bool `json:"download"`
	// LaterLibraries: "every library" takes in the ones added later too.
	// Otherwise it is every one there is now, and a library added later
	// goes to nobody until someone picks it.
	LaterLibraries bool   `json:"laterLibraries"`
	Note           string `json:"note,omitempty"`
	// allFlag: the service keeps "every library" as a setting of its own,
	// apart from naming each one, so a read-back tells the two apart.
	allFlag bool
}

// AccessEditor is a Provider that can read and change an existing
// account's access.
type AccessEditor interface {
	AccessCapability() AccessCapability
	GetAccess(ctx context.Context, hc *http.Client, admin Admin, username string) (AccessState, error)
	// SetAccess changes the parts AccessCapability names to want, leaves
	// everything else as it was, and answers the account as read back.
	SetAccess(ctx context.Context, hc *http.Client, admin Admin, username string, want Access) (AccessState, error)
}

// libraryIDNormalizer is a service that writes one library id more than
// one way: a picked id is put in the form its AccessState uses.
type libraryIDNormalizer interface {
	libraryID(id string) string
}

var (
	// ErrNoAccount: no account on the service has that username.
	ErrNoAccount = errors.New("there is no account with that username on the service")
	// ErrAccessUnsupported: this service's accounts can't be changed from here.
	ErrAccessUnsupported = errors.New("an account's access can't be changed on this service from here")
	// ErrNothingToChange: one library, and downloads set for everyone.
	ErrNothingToChange = errors.New("there is nothing to change here: the service has one library, and it sets downloads for everyone")
)

// NotTakenError is the service answering yes while the account, read back,
// still has Parts ("downloads", "libraries") as they were. State is what
// it has.
type NotTakenError struct {
	Parts []string
	State AccessState
}

func (e *NotTakenError) Error() string {
	return "the service said yes, but reading the account back shows its " +
		strings.Join(e.Parts, " and ") + " unchanged"
}

// AlsoChangedError is the service changing more than was asked: the
// account, read back, has other rights (Fields) than it had just before.
// The change itself took; State is what the account has now.
type AlsoChangedError struct {
	Fields []string
	State  AccessState
}

func (e *AlsoChangedError) Error() string {
	return "the change was made, but reading the account back shows these changed too: " +
		strings.Join(e.Fields, ", ")
}

// AccessCapabilityOf is what Change access can set on a service type; ok
// is false where it can set nothing.
func AccessCapabilityOf(serviceType string) (AccessCapability, bool) {
	ed, err := accessEditorFor(serviceType)
	if err != nil {
		return AccessCapability{}, false
	}
	return ed.AccessCapability(), true
}

func accessEditorFor(serviceType string) (AccessEditor, error) {
	prov, ok := For(serviceType)
	if !ok {
		return nil, ErrUnsupported
	}
	ed, ok := prov.(AccessEditor)
	if !ok {
		return nil, ErrAccessUnsupported
	}
	return ed, nil
}

// accessTarget is the account to read or change: named, and never the
// admin's own sign-in.
func accessTarget(admin Admin, username string) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", &ServiceError{Status: http.StatusBadRequest, Message: "a username is needed"}
	}
	if strings.EqualFold(username, strings.TrimSpace(admin.Username)) {
		return "", ErrAdminAccount
	}
	return username, nil
}

// GetAccess is an account's access, read with the kept admin sign-in.
func GetAccess(ctx context.Context, hc *http.Client, serviceType string, admin Admin, username string) (AccessState, error) {
	ed, err := accessEditorFor(serviceType)
	if err != nil {
		return AccessState{}, err
	}
	if username, err = accessTarget(admin, username); err != nil {
		return AccessState{}, err
	}
	st, err := ed.GetAccess(ctx, hc, admin, username)
	if err != nil {
		return AccessState{}, err
	}
	return tidyAccess(ed.AccessCapability(), st), nil
}

// ChangeAccess sets an account's access to want and answers it as read
// back; a part that read back unchanged is a NotTakenError.
func ChangeAccess(ctx context.Context, hc *http.Client, serviceType string, admin Admin, username string, want Access) (AccessState, error) {
	ed, err := accessEditorFor(serviceType)
	if err != nil {
		return AccessState{}, err
	}
	if username, err = accessTarget(admin, username); err != nil {
		return AccessState{}, err
	}
	can := ed.AccessCapability()
	if n, ok := ed.(libraryIDNormalizer); ok {
		ids := make([]string, 0, len(want.Libraries))
		for _, id := range want.Libraries {
			ids = append(ids, n.libraryID(id))
		}
		want.Libraries = ids
	}
	if can.Libraries && !want.AllLibraries {
		want.Libraries = cleanIDs(want.Libraries)
		if len(want.Libraries) == 0 {
			return AccessState{}, ErrNoLibraries
		}
	}
	// The account's other rights, just before: after the change they must
	// read back the same.
	before, err := ed.GetAccess(ctx, hc, admin, username)
	if err != nil {
		return AccessState{}, err
	}
	got, err := ed.SetAccess(ctx, hc, admin, username, want)
	if err != nil {
		return AccessState{}, err
	}
	got = tidyAccess(can, got)
	if parts := untaken(can, want, got); len(parts) > 0 {
		return got, &NotTakenError{Parts: parts, State: got}
	}
	if moved := changedFields(before.rest, got.rest); len(moved) > 0 {
		return got, &AlsoChangedError{Fields: moved, State: got}
	}
	return got, nil
}

// changedFields is every field whose value differs, by name.
func changedFields(before, after map[string]any) []string {
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	var out []string
	for k := range keys {
		if !reflect.DeepEqual(before[k], after[k]) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// restOf is a map of rights without the ones Change access sets, each
// under prefix.
func restOf(into map[string]any, prefix string, rights map[string]any, skip ...string) {
	for k, v := range rights {
		if !slices.Contains(skip, k) {
			into[prefix+k] = v
		}
	}
}

// sortedWithout is a list of roles or permissions without the ones Change
// access sets, in order, for comparing.
func sortedWithout(list []string, skip ...string) []string {
	out := []string{}
	for _, s := range list {
		if !slices.Contains(skip, s) {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// parsed is raw JSON as plain values, for comparing.
func parsed(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

// tidyAccess: lists never nil, only libraries the service has (in its
// order), all of them named with AllLibraries — which, on a service with
// no setting of its own for it, is having every one there is. One
// collection is all of it.
func tidyAccess(can AccessCapability, st AccessState) AccessState {
	if st.Available == nil {
		st.Available = []Library{}
	}
	has := map[string]bool{}
	for _, id := range st.Libraries {
		has[id] = true
	}
	if len(st.Available) == 0 {
		st.AllLibraries = true
	} else if !can.allFlag {
		st.AllLibraries = true
		for _, l := range st.Available {
			if !has[l.ID] {
				st.AllLibraries = false
			}
		}
	}
	libs := []string{}
	for _, l := range st.Available {
		if st.AllLibraries || has[l.ID] {
			libs = append(libs, l.ID)
		}
	}
	st.Libraries = libs
	return st
}

// untaken is what the account, read back, does not have of what was asked.
func untaken(can AccessCapability, want Access, got AccessState) []string {
	var parts []string
	if can.Download && want.Download != got.Download {
		parts = append(parts, "downloads")
	}
	if can.Libraries && len(got.Available) > 0 {
		asked := map[string]bool{}
		if want.AllLibraries {
			for _, l := range got.Available {
				asked[l.ID] = true
			}
		} else {
			for _, id := range want.Libraries {
				asked[id] = true
			}
		}
		sees := map[string]bool{}
		for _, id := range got.Libraries {
			sees[id] = true
		}
		same := !can.allFlag || want.AllLibraries == got.AllLibraries
		for _, l := range got.Available {
			if asked[l.ID] != sees[l.ID] {
				same = false
			}
		}
		if !same {
			parts = append(parts, "libraries")
		}
	}
	return parts
}

// cleanIDs drops blanks and repeats, keeping the order.
func cleanIDs(ids []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// allIDs is every library's id, in the service's order.
func allIDs(all []Library) []string {
	out := make([]string, 0, len(all))
	for _, l := range all {
		out = append(out, l.ID)
	}
	return out
}

// chosenIDs is the libraries want gives: every one, or the picked ones the
// service still has (ErrLibrariesGone when none is left).
func chosenIDs(all []Library, want Access) ([]string, error) {
	if want.AllLibraries {
		return allIDs(all), nil
	}
	return picked(all, want.Libraries)
}
