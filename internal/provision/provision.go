// Package provision gives a person an account on one of the household's
// media servers, or sets that account's password, with the admin sign-in
// this server keeps for the service (repository.ServiceAdminRepository).
//
// Owner, 2026-10-14: "i establish a new alexandria account for Emily and
// create two connected services for ABS and Grimmory. Using the saved
// admin credentials in the Alexandria server > create that Emily account
// with provided credentials if the account doesn't exist. if it exists >
// reset pass to what was provided." — "Ideally it should be for all
// supported services."
//
// These are the only outbound connections the server makes: to the
// household's own servers, at the addresses the admin saved, and only when
// an admin asks for an account to be created or reset (or, for that, for
// the service's libraries to pick from).
//
// Every service's calls were read from its own source (2026-10-14):
// Audiobookshelf, Grimmory, Jellyfin, Navidrome, Komga, Kavita, Stump,
// Storyteller and Calibre-Web. These rules hold everywhere:
//   - an admin (or owner/root) account is never touched, so a mistyped
//     username can never reset the admin's own password;
//   - an existing account's password changes only when the caller says so
//     (Request.AllowReset), after the admin was asked;
//   - a new account can read and download, and nothing more: no uploads,
//     edits, deletes or admin rights (owner, 2026-10-14: "by default we
//     should be doing read only, plus download for media");
//   - it sees every library unless the admin picked some (Person.Libraries:
//     "need to also pick libraries available"). A reset leaves an account's
//     libraries as they are.
package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Admin is the kept admin sign-in for one service.
type Admin struct {
	BaseURL  string
	Username string
	Password string
}

// Person is the account to make or update.
type Person struct {
	Username string
	Password string
	// Name is the display name (the person's profile name by default);
	// Email is asked for by the services that need one.
	Name  string
	Email string
	// Libraries is what a NEW account may see: nil is every library (and,
	// where the service has the notion, the ones added later); otherwise
	// only these, by the ids Libraries lists. A reset never changes them.
	Libraries []string
}

// Library is one of a service's libraries, for the admin to pick from.
type Library struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Outcome is what happened to the account.
type Outcome string

const (
	Created Outcome = "created"
	Reset   Outcome = "reset"
)

// Result is a success. Note says what else the service did, in words for
// the admin (for example "Kavita also emailed her the invite").
type Result struct {
	Outcome Outcome `json:"outcome"`
	Note    string  `json:"note,omitempty"`
}

var (
	// ErrExists: the account is there and the caller did not allow a reset.
	ErrExists = errors.New("an account with that username already exists")
	// ErrAdminAccount: the account is an admin, owner or root one.
	ErrAdminAccount = errors.New("that is an admin account on the service, which is never changed from here")
	// ErrEmailRequired: this service needs an email address for a new account.
	ErrEmailRequired = errors.New("this service needs an email address for a new account")
	// ErrEmailUsername: this service signs people in with an email address.
	ErrEmailUsername = errors.New("this service signs in with an email address, so the username must be one")
	// ErrResetUnsupported: the account exists, and the service cannot set
	// someone else's password.
	ErrResetUnsupported = errors.New("this service cannot set someone else's password")
	// ErrUnsupported: accounts cannot be made on this service at all.
	ErrUnsupported = errors.New("accounts cannot be created on this service from here")
	// ErrNoLibraries: libraries were picked, and none was.
	ErrNoLibraries = errors.New("pick at least one library")
	// ErrLibrariesGone: none of the picked libraries is on the service now.
	ErrLibrariesGone = errors.New("none of the picked libraries is on the service any more")
)

// ServiceError is the service saying no: its status and its own words.
type ServiceError struct {
	Status  int
	Message string
}

func (e *ServiceError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the service answered %d", e.Status)
	}
	return fmt.Sprintf("the service answered %d: %s", e.Status, e.Message)
}

// SignInError is the kept admin sign-in not working.
type SignInError struct{ Err error }

func (e *SignInError) Error() string { return "the saved admin sign-in did not work: " + e.Err.Error() }
func (e *SignInError) Unwrap() error { return e.Err }

// UnreachableError is the service not answering at all.
type UnreachableError struct{ Err error }

func (e *UnreachableError) Error() string {
	return "the service could not be reached: " + e.Err.Error()
}
func (e *UnreachableError) Unwrap() error { return e.Err }

// Email is how a service treats a new account's email address.
type Email string

const (
	EmailOptional Email = "optional"
	EmailRequired Email = "required"
	// EmailUsername: the username IS the email address (Komga).
	EmailUsername Email = "username"
)

// Capability is what the server can do on one service, for the admin
// page and the phone.
type Capability struct {
	Type   string `json:"type"`
	Create bool   `json:"create"`
	Reset  bool   `json:"reset"`
	Email  Email  `json:"email"`
	// Libraries: the service has libraries to pick from (Libraries). The
	// others have one collection, so there is nothing to pick.
	Libraries bool `json:"libraries"`
	// Note is a sentence the forms show (for example why reset is off).
	Note string `json:"note,omitempty"`
	// Access is what Change access can set on an existing account
	// (AccessEditor); absent where it can set nothing.
	Access *AccessCapability `json:"access,omitempty"`
}

// Provider makes or updates accounts on one kind of service.
type Provider interface {
	Capability() Capability
	Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error)
}

// Librarian is a Provider whose service has libraries to pick from.
type Librarian interface {
	Libraries(ctx context.Context, hc *http.Client, admin Admin) ([]Library, error)
}

var providers = map[string]Provider{
	"abs":         absProvider{},
	"booklore":    grimmoryProvider{},
	"jellyfin":    jellyfinProvider{},
	"navidrome":   navidromeProvider{},
	"komga":       komgaProvider{},
	"kavita":      kavitaProvider{},
	"stump":       stumpProvider{},
	"storyteller": storytellerProvider{},
	"calibreweb":  calibreWebProvider{},
}

// For is the provider for a service type (the app's names, as
// repository.ServiceAdminTypes has them).
func For(serviceType string) (Provider, bool) {
	p, ok := providers[serviceType]
	return p, ok
}

// Capabilities is every service's, by type.
func Capabilities() []Capability {
	out := make([]Capability, 0, len(providers))
	for _, p := range providers {
		c := p.Capability()
		_, c.Libraries = p.(Librarian)
		if ed, ok := p.(AccessEditor); ok {
			a := ed.AccessCapability()
			c.Access = &a
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Libraries is a service's libraries, read with the kept admin sign-in, in
// the service's own order; none for a service without libraries to pick.
func Libraries(ctx context.Context, hc *http.Client, serviceType string, admin Admin) ([]Library, error) {
	prov, ok := For(serviceType)
	if !ok {
		return nil, ErrUnsupported
	}
	lib, ok := prov.(Librarian)
	if !ok {
		return []Library{}, nil
	}
	list, err := lib.Libraries(ctx, hc, admin)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Library{}
	}
	return list, nil
}

// picked is the ids of the libraries a new account may see, out of every
// one the service has, in the service's order: nil when the admin picked
// none (every library). Ids the service no longer has are dropped; when
// none is left, ErrLibrariesGone.
func picked(all []Library, chosen []string) ([]string, error) {
	if chosen == nil {
		return nil, nil
	}
	want := map[string]bool{}
	for _, id := range chosen {
		want[strings.TrimSpace(id)] = true
	}
	out := []string{}
	for _, l := range all {
		if want[l.ID] {
			out = append(out, l.ID)
		}
	}
	if len(out) == 0 {
		return nil, ErrLibrariesGone
	}
	return out, nil
}

// NewHTTPClient is the client the handler uses: a bounded wait, TLS
// checked as usual, and redirects followed only on the same server
// (sameServerRedirect).
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second, CheckRedirect: sameServerRedirect}
}

// sameServerRedirect follows a redirect only to the server the admin saved:
// the same host and port, and never from https down to http (plain http up
// to https is fine). Go re-sends a request's body and most headers on a
// 307 or 308, and strips only Authorization and Cookie for another host, so
// a redirect elsewhere would carry the admin's password in a sign-in body,
// or Navidrome's X-ND-Authorization, to a server nobody saved (security
// review, 2026-11-02).
func sameServerRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
		return fmt.Errorf("the server sent the request on to %s; only the saved address is used", req.URL.Host)
	}
	prev := via[len(via)-1].URL
	if strings.EqualFold(prev.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
		return errors.New("the server sent the request on from https to plain http; refused")
	}
	return nil
}

// Ensure runs the provider after the checks every service shares.
func Ensure(ctx context.Context, hc *http.Client, serviceType string, admin Admin, p Person, allowReset bool) (Result, error) {
	prov, ok := For(serviceType)
	if !ok {
		return Result{}, ErrUnsupported
	}
	p.Username = strings.TrimSpace(p.Username)
	p.Email = strings.TrimSpace(p.Email)
	p.Name = strings.TrimSpace(p.Name)
	if p.Username == "" || p.Password == "" {
		return Result{}, &ServiceError{Status: http.StatusBadRequest, Message: "a username and a password are needed"}
	}
	// The admin's own sign-in is never the account being changed.
	if strings.EqualFold(p.Username, strings.TrimSpace(admin.Username)) {
		return Result{}, ErrAdminAccount
	}
	if p.Name == "" {
		p.Name = p.Username
	}
	if p.Libraries != nil {
		if _, ok := prov.(Librarian); !ok {
			p.Libraries = nil // one collection: nothing to pick
		} else if len(p.Libraries) == 0 {
			return Result{}, ErrNoLibraries
		}
	}
	switch prov.Capability().Email {
	case EmailUsername:
		if !looksLikeEmail(p.Username) {
			return Result{}, ErrEmailUsername
		}
		if p.Email == "" {
			p.Email = p.Username
		}
	}
	return prov.Ensure(ctx, hc, admin, p, allowReset)
}

// looksLikeEmail is Komga's own rule (".+@.+\..+").
func looksLikeEmail(s string) bool {
	at := strings.Index(s, "@")
	if at < 1 {
		return false
	}
	domain := s[at+1:]
	dot := strings.LastIndex(domain, ".")
	return dot > 0 && dot < len(domain)-1
}

// ---- talking to a service ------------------------------------------------

// client talks to one service: JSON in and out, its own headers.
type client struct {
	http   *http.Client
	base   string
	header http.Header
}

func newClient(hc *http.Client, base string) *client {
	return &client{http: hc, base: strings.TrimRight(strings.TrimSpace(base), "/"), header: http.Header{}}
}

// call sends body — JSON, or a form for url.Values — and decodes a 2xx
// answer into out. Anything else is a ServiceError carrying the service's
// own message; no answer at all is an UnreachableError.
func (c *client) call(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	ctype := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		rd = strings.NewReader(b.Encode())
		ctype = "application/x-www-form-urlencoded"
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(buf)
		ctype = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	for k, v := range c.header {
		req.Header[k] = v
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return &UnreachableError{Err: err}
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return &ServiceError{Status: res.StatusCode, Message: serviceMessage(data)}
	}
	if out != nil && len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("unexpected answer from %s: %w", path, err)
		}
	}
	return nil
}

// signIn turns a failed admin sign-in into a SignInError (a service that
// did not answer stays unreachable).
func signIn(err error) error {
	var unreachable *UnreachableError
	if errors.As(err, &unreachable) {
		return err
	}
	return &SignInError{Err: err}
}

// serviceMessage is a service's own explanation, short: the usual JSON
// fields, else the text itself.
func serviceMessage(data []byte) string {
	text := strings.TrimSpace(string(data))
	var obj map[string]any
	if json.Unmarshal(data, &obj) == nil {
		for _, k := range []string{"message", "error", "detail", "title", "Message", "errors"} {
			if v, ok := obj[k]; ok {
				if s := flatten(v); s != "" {
					text = s
					break
				}
			}
		}
	} else {
		var list []any
		if json.Unmarshal(data, &list) == nil && len(list) > 0 {
			if s := flatten(list); s != "" {
				text = s
			}
		}
	}
	if strings.HasPrefix(text, "<") {
		text = "" // an HTML error page says nothing useful here
	}
	if len(text) > 240 {
		text = text[:240] + "…"
	}
	return text
}

func flatten(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		parts := []string{}
		for _, e := range t {
			if s := flatten(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "; ")
	case map[string]any:
		for _, k := range []string{"message", "description", "Description", "error", "detail"} {
			if s, ok := t[k].(string); ok && s != "" {
				return s
			}
		}
		parts := []string{}
		for _, e := range t {
			if s := flatten(e); s != "" {
				parts = append(parts, s)
			}
		}
		sort.Strings(parts)
		return strings.Join(parts, "; ")
	}
	return ""
}
