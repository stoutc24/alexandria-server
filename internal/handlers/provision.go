package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/provision"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// ProvisionHandler gives a person an account on one of the household's
// media servers — or sets that account's password — with the Server Admin
// sign-in this server keeps for the service (internal/provision; owner,
// 2026-10-14). Both the admin page and an admin phone use it, before they
// save the person's login for that service.
//
// The one place this server reaches out: to the household's own servers,
// at the address saved with the Server Admin sign-in, when an admin asks.
// The kept password is opened here and never leaves the server.
type ProvisionHandler struct {
	admins   *repository.ServiceAdminRepository
	profiles *repository.ProfileRepository
	client   *http.Client
	// ensure is provision.Ensure, libraries provision.Libraries; tests swap them.
	ensure    func(ctx context.Context, hc *http.Client, serviceType string, admin provision.Admin, p provision.Person, allowReset bool) (provision.Result, error)
	libraries func(ctx context.Context, hc *http.Client, serviceType string, admin provision.Admin) ([]provision.Library, error)
	selfTest  func(ctx context.Context, hc *http.Client, serviceType string, admin provision.Admin) (provision.SelfTestReport, error)
	// getAccess and changeAccess are provision.GetAccess and
	// provision.ChangeAccess; services lists a person's saved logins
	// (UseAccounts).
	getAccess    func(ctx context.Context, hc *http.Client, serviceType string, admin provision.Admin, username string) (provision.AccessState, error)
	changeAccess func(ctx context.Context, hc *http.Client, serviceType string, admin provision.Admin, username string, want provision.Access) (provision.AccessState, error)
	services     func(profileID string) ([]repository.AccountService, error)
	// stepUp: setting an existing person's password from a phone asks for
	// a fresh code (UseStepUp).
	stepUp StepUp
}

// UseStepUp gives Provision the fresh-code check (AdminAuthHandler.
// FreshCode): a phone's admin sign-in that resets someone's password on a
// media server must add a code from its authenticator, as an invite from
// a phone does — a lost "stay signed in" phone alone must not be able to
// take over a person's media-server account (security review, 2026-11-02).
func (h *ProvisionHandler) UseStepUp(s StepUp) { h.stepUp = s }

func NewProvisionHandler(admins *repository.ServiceAdminRepository, profiles *repository.ProfileRepository) *ProvisionHandler {
	return &ProvisionHandler{admins: admins, profiles: profiles, client: provision.NewHTTPClient(),
		ensure: provision.Ensure, libraries: provision.Libraries, selfTest: provision.SelfTest,
		getAccess: provision.GetAccess, changeAccess: provision.ChangeAccess}
}

// UseAccounts gives Change access the people's saved logins: the account
// to read or change is the one a login names.
func (h *ProvisionHandler) UseAccounts(accounts *repository.AccountRepository) {
	h.services = func(profileID string) ([]repository.AccountService, error) {
		return accounts.ListServices(profileID, false)
	}
}

type provisionService struct {
	provision.Capability
	Name string `json:"name"`
	// ServerAdmin: a Server Admin sign-in is kept for it, so accounts can
	// be made; Host is where (shown beside the tick).
	ServerAdmin bool   `json:"serverAdmin"`
	Host        string `json:"host,omitempty"`
	// BaseURL: that sign-in's address, which the forms offer as a new
	// service's address (owner, 2026-10-17: "the service address from
	// server admin, or a new url"). Not a secret: Server Admin lists it.
	BaseURL string `json:"baseUrl,omitempty"`
}

// Capabilities is what can be done on each service, and where a Server
// Admin sign-in makes it possible. No secret crosses it.
func (h *ProvisionHandler) Capabilities(c *fiber.Ctx) error {
	kept, err := h.admins.List()
	if err != nil {
		return serverError(c, "could not read the sign-ins", err)
	}
	byType := map[string]repository.ServiceAdmin{}
	for _, a := range kept {
		byType[a.Type] = a
	}
	out := []provisionService{}
	for _, cap := range provision.Capabilities() {
		s := provisionService{Capability: cap, Name: repository.ServiceAdminTypes[cap.Type]}
		if a, ok := byType[cap.Type]; ok {
			s.ServerAdmin = true
			s.Host = hostOf(a.BaseURL)
			s.BaseURL = a.BaseURL
		}
		out = append(out, s)
	}
	return c.JSON(fiber.Map{"available": h.admins.Available(), "services": out})
}

type provisionBody struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Password string `json:"password"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	// AllowReset: the admin was told the account exists and said to
	// change its password.
	AllowReset bool `json:"allowReset"`
	// Libraries: the ids (from Libraries) a NEW account may see; absent or
	// null is every library. A reset leaves them alone.
	Libraries []string `json:"libraries"`
	// A phone's admin sign-in adds a fresh code to a reset (allowReset),
	// the same fields as an invite's.
	TOTPCode     string `json:"totpCode"`
	RecoveryCode string `json:"recoveryCode"`
}

// Provision makes the person's account on the service, or (with
// allowReset) sets its password. Refusals carry a reason the forms act on:
// account-exists (ask, then send allowReset), admin-account, email-required,
// email-username, reset-unsupported, no-service-admin, no-libraries,
// libraries-gone, and the service's own answer.
func (h *ProvisionHandler) Provision(c *fiber.Ctx) error {
	var in provisionBody
	if err := c.BodyParser(&in); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	p, err := h.profiles.GetByID(c.Params("id"))
	if errors.Is(err, repository.ErrProfileNotFound) {
		return refuse(c, fiber.StatusNotFound, "profile-not-found", "profile not found")
	}
	if err != nil {
		return serverError(c, "could not read the profile", err)
	}
	name := repository.ServiceAdminTypes[in.Type]
	if _, ok := provision.For(in.Type); !ok || name == "" {
		return refuse(c, fiber.StatusBadRequest, "not-supported", provision.ErrUnsupported.Error())
	}
	if strings.TrimSpace(in.Username) == "" || in.Password == "" {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "a username and a password are needed")
	}
	if in.AllowReset && middleware.AdminDevice(c) != "" {
		totp := strings.TrimSpace(in.TOTPCode)
		recovery := strings.TrimSpace(in.RecoveryCode)
		if totp == "" && recovery == "" {
			return refuse(c, fiber.StatusForbidden, "step-up-required",
				"setting a password from a phone needs a fresh code from your authenticator; update Alexandria if it does not ask for one")
		}
		if h.stepUp == nil {
			return serverError(c, "fresh codes are not available", errors.New("ProvisionHandler.UseStepUp was never called"))
		}
		if ok, err := h.stepUp(c, totp, recovery, "media-server password reset"); !ok {
			return err
		}
	}
	admin, ok, err := h.openAdmin(c, in.Type, name)
	if !ok {
		return err
	}
	person := provision.Person{Username: in.Username, Password: in.Password, Name: in.Name, Email: in.Email,
		Libraries: in.Libraries}
	if strings.TrimSpace(person.Name) == "" {
		person.Name = p.Name
	}

	ctx, cancel := context.WithTimeout(c.UserContext(), 60*time.Second)
	defer cancel()
	res, err := h.ensure(ctx, h.client, in.Type,
		provision.Admin{BaseURL: admin.BaseURL, Username: admin.Username, Password: admin.Password},
		person, in.AllowReset)
	if err != nil {
		return h.refusal(c, name, in.Username, err)
	}
	picks := ""
	if in.Libraries != nil && res.Outcome == provision.Created {
		picks = fmt.Sprintf(" with %d picked libraries", len(in.Libraries))
	}
	log.Printf("provision: %s account %q %s on %s (%s)%s%s", name, strings.TrimSpace(in.Username),
		res.Outcome, hostOf(admin.BaseURL), p.Name, picks, callerForLog(c))
	return c.JSON(fiber.Map{"outcome": res.Outcome, "note": res.Note, "service": name, "host": hostOf(admin.BaseURL)})
}

// Libraries lists a service's libraries with its kept Server Admin
// sign-in, for the forms to pick the ones a new account may see. Only ids
// and names cross it.
func (h *ProvisionHandler) Libraries(c *fiber.Ctx) error {
	typ := c.Params("type")
	name := repository.ServiceAdminTypes[typ]
	if _, ok := provision.For(typ); !ok || name == "" {
		return refuse(c, fiber.StatusBadRequest, "not-supported", provision.ErrUnsupported.Error())
	}
	admin, ok, err := h.openAdmin(c, typ, name)
	if !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 30*time.Second)
	defer cancel()
	list, err := h.libraries(ctx, h.client, typ,
		provision.Admin{BaseURL: admin.BaseURL, Username: admin.Username, Password: admin.Password})
	if err != nil {
		return h.refusal(c, name, "", err)
	}
	return c.JSON(fiber.Map{"service": name, "host": hostOf(admin.BaseURL), "libraries": list})
}

// SelfTest is the "Test accounts" check on one service, with its kept
// Server Admin sign-in: a throwaway account made, signed in as and deleted,
// after deleting whatever an earlier run left (provision.SelfTest). The
// report says how each step went; a step that failed is no HTTP error.
// POST /api/v1/admin/provision/:type/selftest
func (h *ProvisionHandler) SelfTest(c *fiber.Ctx) error {
	typ := c.Params("type")
	name := repository.ServiceAdminTypes[typ]
	if !provision.SelfTestable(typ) || name == "" {
		return refuse(c, fiber.StatusBadRequest, "not-supported", provision.ErrUnsupported.Error())
	}
	admin, ok, err := h.openAdmin(c, typ, name)
	if !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 90*time.Second)
	defer cancel()
	rep, err := h.selfTest(ctx, h.client, typ,
		provision.Admin{BaseURL: admin.BaseURL, Username: admin.Username, Password: admin.Password})
	if err != nil {
		return h.refusal(c, name, "", err)
	}
	steps := make([]string, 0, len(rep.Steps))
	for _, s := range rep.Steps {
		mark := "ok"
		if !s.OK {
			mark = "FAILED: " + s.Detail
		}
		steps = append(steps, s.Name+" "+mark)
	}
	log.Printf("provision: test accounts on %s (%s): %s", name, hostOf(admin.BaseURL), strings.Join(steps, "; "))
	return c.JSON(fiber.Map{"service": name, "host": hostOf(admin.BaseURL), "report": rep})
}

// accessTarget is what Change access works on: a person's saved login, its
// service, and the kept Server Admin sign-in for that kind of service.
type accessTarget struct {
	person  string
	service repository.AccountService
	name    string
	admin   provision.Admin
	can     provision.AccessCapability
}

// accessTargetOf is the login a Change access request names. ok is false
// when a refusal was sent instead (err is what sending it gave).
func (h *ProvisionHandler) accessTargetOf(c *fiber.Ctx) (t accessTarget, ok bool, err error) {
	p, err := h.profiles.GetByID(c.Params("id"))
	if errors.Is(err, repository.ErrProfileNotFound) {
		return t, false, refuse(c, fiber.StatusNotFound, "profile-not-found", "profile not found")
	}
	if err != nil {
		return t, false, serverError(c, "could not read the profile", err)
	}
	if h.services == nil {
		return t, false, serverError(c, "could not read the logins", errors.New("no accounts store"))
	}
	list, err := h.services(p.ID)
	if err != nil {
		return t, false, serverError(c, "could not read the logins", err)
	}
	found := false
	for _, s := range list {
		if s.ID == c.Params("sid") {
			t.service, found = s, true
		}
	}
	if !found {
		return t, false, refuse(c, fiber.StatusNotFound, "service-not-found", "service not found")
	}
	t.person = p.Name
	t.name = repository.ServiceAdminTypes[t.service.Type]
	can, supported := provision.AccessCapabilityOf(t.service.Type)
	if !supported || t.name == "" {
		return t, false, refuse(c, fiber.StatusBadRequest, "not-supported", provision.ErrAccessUnsupported.Error())
	}
	t.can = can
	if t.service.LoginKind != "password" || strings.TrimSpace(t.service.Username) == "" {
		return t, false, refuse(c, fiber.StatusConflict, "no-username",
			"this login has no username to look up on "+t.name+"; edit it to add one")
	}
	admin, ok, err := h.openAdmin(c, t.service.Type, t.name)
	if !ok {
		return t, false, err
	}
	t.admin = provision.Admin{BaseURL: admin.BaseURL, Username: admin.Username, Password: admin.Password}
	return t, true, nil
}

// accessJSON is an account's access as the page shows it. host is where
// the change goes (the Server Admin sign-in's server); loginHost is the
// address the person's own login uses, which may name the same server
// differently.
func accessJSON(t accessTarget, st provision.AccessState) fiber.Map {
	return fiber.Map{
		"service":   t.name,
		"host":      hostOf(t.admin.BaseURL),
		"loginHost": hostOf(t.service.BaseURL),
		"username":  strings.TrimSpace(t.service.Username),
		"can":       t.can,
		"access":    st.Access,
		"libraries": st.Available,
	}
}

// accessSummary is an account's access in a log line.
func accessSummary(can provision.AccessCapability, st provision.AccessState) string {
	parts := []string{}
	if can.Libraries && len(st.Available) > 0 {
		if st.AllLibraries {
			parts = append(parts, "every library")
		} else {
			parts = append(parts, fmt.Sprintf("%d of %d libraries", len(st.Libraries), len(st.Available)))
		}
	}
	if can.Download {
		if st.Download {
			parts = append(parts, "downloads on")
		} else {
			parts = append(parts, "downloads off")
		}
	}
	return strings.Join(parts, ", ")
}

// Access reads what a person's saved login may see and do on its service
// — its libraries, and whether it may download — with the kept Server
// Admin sign-in (Change access; provision.GetAccess). No secret crosses it.
// GET /api/v1/admin/profiles/:id/services/:sid/provision
func (h *ProvisionHandler) Access(c *fiber.Ctx) error {
	t, ok, err := h.accessTargetOf(c)
	if !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 30*time.Second)
	defer cancel()
	st, err := h.getAccess(ctx, h.client, t.service.Type, t.admin, t.service.Username)
	if err != nil {
		return h.refusal(c, t.name, t.service.Username, err)
	}
	return c.JSON(accessJSON(t, st))
}

type accessBody struct {
	AllLibraries bool     `json:"allLibraries"`
	Libraries    []string `json:"libraries"`
	Download     bool     `json:"download"`
}

// ChangeAccess sets a person's saved login's libraries and downloads on its
// service, and answers the account as read back afterwards
// (provision.ChangeAccess). A service that said yes and kept something as
// it was answers 502 not-taken, and one that changed other rights too 502
// also-changed, each with what the account has.
// PUT /api/v1/admin/profiles/:id/services/:sid/provision
func (h *ProvisionHandler) ChangeAccess(c *fiber.Ctx) error {
	var in accessBody
	if err := c.BodyParser(&in); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	t, ok, err := h.accessTargetOf(c)
	if !ok {
		return err
	}
	ctx, cancel := context.WithTimeout(c.UserContext(), 60*time.Second)
	defer cancel()
	st, err := h.changeAccess(ctx, h.client, t.service.Type, t.admin, t.service.Username,
		provision.Access{AllLibraries: in.AllLibraries, Libraries: in.Libraries, Download: in.Download})
	var nt *provision.NotTakenError
	if errors.As(err, &nt) {
		log.Printf("provision: %s answered yes for %q on %s (%s) but kept its %s: %s%s", t.name,
			strings.TrimSpace(t.service.Username), hostOf(t.admin.BaseURL), t.person,
			strings.Join(nt.Parts, " and "), accessSummary(t.can, nt.State), callerForLog(c))
		out := accessJSON(t, nt.State)
		out["reason"] = "not-taken"
		out["error"] = t.name + " said yes, but reading the account back shows its " +
			strings.Join(nt.Parts, " and ") + " unchanged"
		return c.Status(fiber.StatusBadGateway).JSON(out)
	}
	var ac *provision.AlsoChangedError
	if errors.As(err, &ac) {
		log.Printf("provision: %s access for %q on %s (%s) set to %s, but these changed too: %s%s", t.name,
			strings.TrimSpace(t.service.Username), hostOf(t.admin.BaseURL), t.person,
			accessSummary(t.can, ac.State), strings.Join(ac.Fields, ", "), callerForLog(c))
		out := accessJSON(t, ac.State)
		out["reason"] = "also-changed"
		out["error"] = "The change was made, but " + t.name + " also changed " +
			strings.Join(ac.Fields, ", ") + " on this account; check it in " + t.name
		return c.Status(fiber.StatusBadGateway).JSON(out)
	}
	if err != nil {
		return h.refusal(c, t.name, t.service.Username, err)
	}
	log.Printf("provision: %s access for %q on %s (%s) set to %s%s", t.name,
		strings.TrimSpace(t.service.Username), hostOf(t.admin.BaseURL), t.person, accessSummary(t.can, st), callerForLog(c))
	return c.JSON(accessJSON(t, st))
}

// openAdmin is the kept Server Admin sign-in for a service, opened. ok is
// false when there is none to use: the refusal is then sent already, and
// err is what sending it gave (nil, usually).
func (h *ProvisionHandler) openAdmin(c *fiber.Ctx, typ, name string) (admin *repository.OpenedServiceAdmin, ok bool, err error) {
	admin, err = h.admins.Open(typ)
	switch {
	case errors.Is(err, repository.ErrVaultOff):
		return nil, false, refuse(c, fiber.StatusServiceUnavailable, "no-secrets-key", err.Error())
	case errors.Is(err, repository.ErrServiceAdminNotFound):
		return nil, false, refuse(c, fiber.StatusConflict, "no-service-admin",
			"save your own "+name+" admin sign-in under Server Admin first")
	case err != nil:
		return nil, false, serverError(c, "could not read the sign-in", err)
	case admin.Unreadable:
		return nil, false, refuse(c, fiber.StatusConflict, "service-admin-unreadable",
			"the saved "+name+" admin sign-in was sealed with another secrets key; save it again")
	}
	return admin, true, nil
}

func (h *ProvisionHandler) refusal(c *fiber.Ctx, name, username string, err error) error {
	var se *provision.ServiceError
	var si *provision.SignInError
	var un *provision.UnreachableError
	switch {
	case errors.Is(err, provision.ErrExists):
		return refuse(c, fiber.StatusConflict, "account-exists",
			strings.TrimSpace(username)+" already has an account on "+name)
	case errors.Is(err, provision.ErrAdminAccount):
		return refuse(c, fiber.StatusConflict, "admin-account",
			strings.TrimSpace(username)+" is an admin account on "+name+"; it is never changed from here")
	case errors.Is(err, provision.ErrEmailRequired):
		return refuse(c, fiber.StatusBadRequest, "email-required", name+" needs an email address for a new account")
	case errors.Is(err, provision.ErrEmailUsername):
		return refuse(c, fiber.StatusBadRequest, "email-username", name+" signs people in with an email address, so use one as the username")
	case errors.Is(err, provision.ErrResetUnsupported):
		return refuse(c, fiber.StatusConflict, "reset-unsupported",
			strings.TrimSpace(username)+" already has an account on "+name+", and "+name+" can't set someone else's password; use theirs")
	case errors.Is(err, provision.ErrUnsupported), errors.Is(err, provision.ErrAccessUnsupported):
		return refuse(c, fiber.StatusBadRequest, "not-supported", err.Error())
	case errors.Is(err, provision.ErrNoAccount):
		return refuse(c, fiber.StatusNotFound, "no-account",
			strings.TrimSpace(username)+" has no account on "+name)
	case errors.Is(err, provision.ErrNothingToChange):
		return refuse(c, fiber.StatusConflict, "nothing-to-change",
			name+" has one library, and it sets downloads for everyone: there is nothing to change here")
	case errors.Is(err, provision.ErrNoLibraries):
		return refuse(c, fiber.StatusBadRequest, "no-libraries", "pick at least one "+name+" library")
	case errors.Is(err, provision.ErrLibrariesGone):
		return refuse(c, fiber.StatusConflict, "libraries-gone",
			"none of the picked libraries is on "+name+" any more; open the list again")
	case errors.As(err, &si):
		log.Printf("provision: the %s admin sign-in failed: %v", name, err)
		return refuse(c, fiber.StatusBadGateway, "admin-sign-in-failed",
			"the saved "+name+" admin sign-in did not work; check it under Server Admin")
	case errors.As(err, &un):
		log.Printf("provision: %s did not answer: %v", name, err)
		return refuse(c, fiber.StatusBadGateway, "service-unreachable",
			"this server could not reach "+name+" at its saved address")
	case errors.As(err, &se):
		log.Printf("provision: %s refused: %v", name, err)
		msg := name + " said no"
		if se.Message != "" {
			msg += ": " + se.Message
		}
		return refuse(c, fiber.StatusBadGateway, "service-refused", msg)
	}
	return serverError(c, "could not set up the account", err)
}

func hostOf(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	return u.Host
}
