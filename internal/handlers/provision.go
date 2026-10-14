package handlers

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
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
	// ensure is provision.Ensure; tests swap it.
	ensure func(ctx context.Context, hc *http.Client, serviceType string, admin provision.Admin, p provision.Person, allowReset bool) (provision.Result, error)
}

func NewProvisionHandler(admins *repository.ServiceAdminRepository, profiles *repository.ProfileRepository) *ProvisionHandler {
	return &ProvisionHandler{admins: admins, profiles: profiles, client: provision.NewHTTPClient(), ensure: provision.Ensure}
}

type provisionService struct {
	provision.Capability
	Name string `json:"name"`
	// ServerAdmin: a Server Admin sign-in is kept for it, so accounts can
	// be made; Host is where (shown beside the tick).
	ServerAdmin bool   `json:"serverAdmin"`
	Host        string `json:"host,omitempty"`
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
}

// Provision makes the person's account on the service, or (with
// allowReset) sets its password. Refusals carry a reason the forms act on:
// account-exists (ask, then send allowReset), admin-account, email-required,
// email-username, reset-unsupported, no-service-admin, and the service's own
// answer.
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
	admin, err := h.admins.Open(in.Type)
	switch {
	case errors.Is(err, repository.ErrVaultOff):
		return refuse(c, fiber.StatusServiceUnavailable, "no-secrets-key", err.Error())
	case errors.Is(err, repository.ErrServiceAdminNotFound):
		return refuse(c, fiber.StatusConflict, "no-service-admin",
			"save your own "+name+" admin sign-in under Server Admin first")
	case err != nil:
		return serverError(c, "could not read the sign-in", err)
	case admin.Unreadable:
		return refuse(c, fiber.StatusConflict, "service-admin-unreadable",
			"the saved "+name+" admin sign-in was sealed with another secrets key; save it again")
	}
	person := provision.Person{Username: in.Username, Password: in.Password, Name: in.Name, Email: in.Email}
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
	log.Printf("provision: %s account %q %s on %s (%s)%s", name, strings.TrimSpace(in.Username),
		res.Outcome, hostOf(admin.BaseURL), p.Name, callerForLog(c))
	return c.JSON(fiber.Map{"outcome": res.Outcome, "note": res.Note, "service": name, "host": hostOf(admin.BaseURL)})
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
	case errors.Is(err, provision.ErrUnsupported):
		return refuse(c, fiber.StatusBadRequest, "not-supported", err.Error())
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
