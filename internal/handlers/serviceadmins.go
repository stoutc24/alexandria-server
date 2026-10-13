package handlers

import (
	"encoding/json"
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

// ServiceAdminsHandler is Server Admin's kept sign-ins to the household's
// servers (see repository.ServiceAdminRepository). Every route is behind
// requireAdmin: the web page and admin phones list, save and remove them;
// only an admin phone with a fresh code reads the passwords back (Open).
type ServiceAdminsHandler struct {
	repo   *repository.ServiceAdminRepository
	stepUp StepUp
}

func NewServiceAdminsHandler(repo *repository.ServiceAdminRepository, stepUp StepUp) *ServiceAdminsHandler {
	return &ServiceAdminsHandler{repo: repo, stepUp: stepUp}
}

// List is what is kept, never a password, and whether this server can keep
// any (it needs the secrets key).
func (h *ServiceAdminsHandler) List(c *fiber.Ctx) error {
	admins, err := h.repo.List()
	if err != nil {
		return serverError(c, "could not read the sign-ins", err)
	}
	return c.JSON(fiber.Map{"available": h.repo.Available(), "admins": admins})
}

// Save keeps a sign-in, replacing that service's.
func (h *ServiceAdminsHandler) Save(c *fiber.Ctx) error {
	var in repository.ServiceAdminInput
	if err := c.BodyParser(&in); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	saved, err := h.repo.Save(in)
	switch {
	case errors.Is(err, repository.ErrVaultOff):
		return refuse(c, fiber.StatusServiceUnavailable, "no-secrets-key", err.Error())
	case errors.Is(err, repository.ErrBadServiceAdmin):
		return refuse(c, fiber.StatusBadRequest, "bad-service-admin", err.Error())
	case err != nil:
		return serverError(c, "could not keep the sign-in", err)
	}
	log.Printf("service admins: %s sign-in kept%s", saved.Name, callerForLog(c))
	return c.JSON(saved)
}

// Delete removes one.
func (h *ServiceAdminsHandler) Delete(c *fiber.Ctx) error {
	err := h.repo.Delete(c.Params("id"))
	switch {
	case errors.Is(err, repository.ErrServiceAdminNotFound):
		return refuse(c, fiber.StatusNotFound, "not-found", err.Error())
	case err != nil:
		return serverError(c, "could not remove the sign-in", err)
	}
	log.Printf("service admins: a sign-in removed%s", callerForLog(c))
	return c.SendStatus(fiber.StatusNoContent)
}

type openServiceAdminsBody struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recoveryCode"`
	// The public half of a key the phone made for this one request: the
	// answer is sealed to it, so it is useless to anyone who sees it on
	// the way (an http link inside the home) or replays the admin token
	// without that key.
	PublicKey string `json:"publicKey"`
}

// Open hands every kept sign-in, passwords included, to an admin phone: a
// fresh code from the authenticator on top of its sign-in (each phone
// fetches once), the answer boxed to the key it sent. The web page never
// needs a password, so it never gets one; nor does the environment token.
// Behind the sign-in rate limiter, so code guesses here share its budget.
func (h *ServiceAdminsHandler) Open(c *fiber.Ctx) error {
	if middleware.AdminDevice(c) == "" {
		return refuse(c, fiber.StatusForbidden, "phones-only",
			"only a phone signed in to Manage server can fetch these")
	}
	var body openServiceAdminsBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	pub, err := vault.ParseDeviceKey(body.PublicKey)
	if err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-key", "send the public key of a fresh X25519 key pair")
	}
	if ok, err := h.stepUp(c, body.Code, body.RecoveryCode, "Server Admin sign-ins"); !ok {
		return err
	}
	opened, err := h.repo.OpenAll()
	if errors.Is(err, repository.ErrVaultOff) {
		return refuse(c, fiber.StatusServiceUnavailable, "no-secrets-key", err.Error())
	}
	if err != nil {
		return serverError(c, "could not open the sign-ins", err)
	}
	plain, err := json.Marshal(fiber.Map{"admins": opened})
	if err != nil {
		return serverError(c, "could not prepare the sign-ins", err)
	}
	box, err := vault.SealToDevice(pub, plain)
	if err != nil {
		return serverError(c, "could not seal the sign-ins", err)
	}
	log.Printf("service admins: %d sign-ins handed out%s", len(opened), callerForLog(c))
	return c.JSON(fiber.Map{"box": box})
}
