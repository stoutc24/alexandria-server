package handlers

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

type AdminAuthHandler struct {
	auth *repository.AdminAuthRepository
}

func NewAdminAuthHandler(auth *repository.AdminAuthRepository) *AdminAuthHandler {
	return &AdminAuthHandler{auth: auth}
}

// Status is open, and says only whether a login exists.
//
// The admin page needs to know which of two screens to draw before
// anyone has authenticated, so this cannot require a credential. It
// leaks one bit — "this companion has an admin login" — which is not
// worth protecting and is obvious from the login form anyway.
func (h *AdminAuthHandler) Status(c *fiber.Ctx) error {
	enrolled, err := h.auth.Enrolled()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).
			JSON(fiber.Map{"error": "could not read admin credentials"})
	}

	signedIn := false
	if raw := c.Cookies(middleware.AdminSessionCookie); raw != "" {
		if ok, err := h.auth.SessionValid(adminauth.HashToken(raw)); err == nil {
			signedIn = ok
		}
	}
	return c.JSON(fiber.Map{"enrolled": enrolled, "signedIn": signedIn})
}

type enrollRequest struct {
	Password string `json:"password"`
}

// Enroll sets up (or resets) the admin login. Gated on the environment
// token by the route, never on a session.
//
// Returns the TOTP secret and recovery codes ONCE. They are not stored
// in a form that can show them again — the secret could be, but handing
// it back on demand would turn any later session compromise into a
// permanent second factor compromise.
func (h *AdminAuthHandler) Enroll(c *fiber.Ctx) error {
	var req enrollRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(fiber.Map{"error": "invalid request body"})
	}

	hash, err := adminauth.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(fiber.Map{"error": err.Error()})
	}

	secret, err := adminauth.NewSecret()
	if err != nil {
		return serverError(c, "could not generate a TOTP secret", err)
	}

	codes, err := adminauth.NewRecoveryCodes()
	if err != nil {
		return serverError(c, "could not generate recovery codes", err)
	}
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hashes = append(hashes,
			adminauth.HashToken(adminauth.NormalizeRecoveryCode(code)))
	}

	if err := h.auth.Enroll(hash, secret, hashes); err != nil {
		return serverError(c, "could not save admin credentials", err)
	}

	log.Print("admin login enrolled — the environment token no longer " +
		"opens /admin, only enrolment")

	return c.JSON(fiber.Map{
		"secret":          secret,
		"provisioningUri": adminauth.ProvisioningURI(secret, "admin", "Alexandria"),
		"recoveryCodes":   codes,
	})
}

type loginRequest struct {
	Password     string `json:"password"`
	Code         string `json:"code"`
	RecoveryCode string `json:"recoveryCode"`
}

// Login takes a password and either a TOTP code or a recovery code.
//
// Every failure returns the same message. Distinguishing "wrong
// password" from "wrong code" would tell an attacker which half they had
// already solved, turning one 2FA problem into two 1FA problems.
func (h *AdminAuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).
			JSON(fiber.Map{"error": "invalid request body"})
	}

	cred, err := h.auth.Get()
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).
			JSON(fiber.Map{"error": "no admin login is set up on this server"})
	}

	// The password gates everything that follows, and nothing before it
	// may change state.
	//
	// Both second factors are CONSUMED when they succeed — a TOTP step is
	// marked spent, a recovery code is burned — so checking them against
	// an unproven password would hand anyone who can reach this route two
	// attacks for free. They could burn all ten recovery codes without
	// knowing the password, and they could spend the current TOTP step to
	// lock the real admin out for the rest of its window. The same thing
	// happens by accident every time someone fat-fingers their password
	// and retypes it while the code on screen has not rolled over yet,
	// which is how this was found.
	if !adminauth.VerifyPassword(cred.PasswordHash, req.Password) {
		return c.Status(fiber.StatusUnauthorized).
			JSON(fiber.Map{"error": "wrong password or code"})
	}

	secondFactorOK := false
	usedRecovery := false
	if req.RecoveryCode != "" {
		spent, err := h.auth.SpendRecoveryCode(
			adminauth.HashToken(adminauth.NormalizeRecoveryCode(req.RecoveryCode)))
		if err != nil {
			return serverError(c, "could not check the recovery code", err)
		}
		secondFactorOK = spent
		usedRecovery = spent
	} else if step, ok := adminauth.Verify(
		cred.TOTPSecret, req.Code, time.Now().Unix(), cred.LastTOTPStep,
	); ok {
		if err := h.auth.NoteTOTPStep(step); err != nil {
			return serverError(c, "could not record the TOTP step", err)
		}
		secondFactorOK = true
	}

	if !secondFactorOK {
		return c.Status(fiber.StatusUnauthorized).
			JSON(fiber.Map{"error": "wrong password or code"})
	}

	token, err := adminauth.NewToken()
	if err != nil {
		return serverError(c, "could not create a session", err)
	}
	if err := h.auth.CreateSession(adminauth.HashToken(token)); err != nil {
		return serverError(c, "could not save the session", err)
	}

	c.Cookie(&fiber.Cookie{
		Name:     middleware.AdminSessionCookie,
		Value:    token,
		Path:     "/",
		HTTPOnly: true,
		// Secure is set only on HTTPS, because this server is also
		// reached at http://<lan-ip>:4000, where a Secure cookie would
		// never be sent and the login would appear to silently fail.
		// Fiber reads X-Forwarded-Proto when proxy headers are trusted,
		// so a reverse-proxied HTTPS host still gets the flag.
		Secure:   c.Protocol() == "https",
		SameSite: "Strict",
		Expires:  time.Now().Add(repository.SessionLifetime),
	})

	remaining, _ := h.auth.RecoveryCodesRemaining()
	return c.JSON(fiber.Map{
		"ok":                     true,
		"usedRecoveryCode":       usedRecovery,
		"recoveryCodesRemaining": remaining,
	})
}

func (h *AdminAuthHandler) Logout(c *fiber.Ctx) error {
	if raw := c.Cookies(middleware.AdminSessionCookie); raw != "" {
		if err := h.auth.DeleteSession(adminauth.HashToken(raw)); err != nil {
			return serverError(c, "could not end the session", err)
		}
	}
	c.Cookie(&fiber.Cookie{
		Name:     middleware.AdminSessionCookie,
		Value:    "",
		Path:     "/",
		HTTPOnly: true,
		Secure:   c.Protocol() == "https",
		SameSite: "Strict",
		Expires:  time.Now().Add(-time.Hour),
	})
	return c.JSON(fiber.Map{"ok": true})
}

// serverError logs the cause and tells the caller nothing about it.
func serverError(c *fiber.Ctx, msg string, err error) error {
	log.Printf("%s: %v", msg, err)
	return c.Status(fiber.StatusInternalServerError).
		JSON(fiber.Map{"error": msg})
}
