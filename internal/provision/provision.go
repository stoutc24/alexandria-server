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
// an admin asks for an account to be created or reset.
//
// Every service's calls were read from its own source (2026-10-14):
// Audiobookshelf, Grimmory, Jellyfin, Navidrome, Komga, Kavita, Stump,
// Storyteller and Calibre-Web. Two rules hold everywhere:
//   - an admin (or owner/root) account is never touched, so a mistyped
//     username can never reset the admin's own password;
//   - an existing account's password changes only when the caller says so
//     (Request.AllowReset), after the admin was asked.
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
	// Note is a sentence the forms show (for example why reset is off).
	Note string `json:"note,omitempty"`
}

// Provider makes or updates accounts on one kind of service.
type Provider interface {
	Capability() Capability
	Ensure(ctx context.Context, hc *http.Client, admin Admin, p Person, allowReset bool) (Result, error)
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
		out = append(out, p.Capability())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// NewHTTPClient is the client the handler uses: a bounded wait, TLS
// checked as usual.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 20 * time.Second}
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
