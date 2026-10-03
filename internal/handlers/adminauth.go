package handlers

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// clock is "now" for the handlers: a variable, so a test can move it.
var clock = time.Now

type AdminAuthHandler struct {
	auth *repository.AdminAuthRepository
	// httpsDeclared is true when ALEXANDRIA_PUBLIC_URL is an https://
	// address: the operator has declared this server is reached only
	// over HTTPS (scheme basis "config", for a sign-in that stays and
	// whose headers name no scheme — see decideSchemeBasis).
	httpsDeclared bool
}

// NewAdminAuthHandler takes the configured public URL (config.PublicURL,
// "" when unset) so an admin sign-in can record an operator's https://
// declaration as the basis of its HTTPS.
func NewAdminAuthHandler(
	auth *repository.AdminAuthRepository,
	publicURL string,
) *AdminAuthHandler {
	return &AdminAuthHandler{
		auth:          auth,
		httpsDeclared: strings.HasPrefix(strings.ToLower(publicURL), "https://"),
	}
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

	ok, usedRecovery, fault := h.checkCredentials(cred, req, "admin sign-in")
	if fault != nil {
		return serverError(c, fault.msg, fault.err)
	}
	if !ok {
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

// authFault is a server-side failure while checking credentials. The
// caller answers 500 with msg; serverError logs err.
type authFault struct {
	msg string
	err error
}

// checkCredentials is Login's whole check, shared with the phone's
// sign-in (IssueDeviceToken) so the two can never drift apart: the
// password, then one second factor. attempt names the attempt in the
// server log ("admin sign-in"); the log says which half failed, and the
// caller's response never does.
func (h *AdminAuthHandler) checkCredentials(
	cred *repository.AdminCredential,
	req loginRequest,
	attempt string,
) (ok, usedRecovery bool, fault *authFault) {
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
		// The RESPONSE stays deliberately vague; the server log does not.
		// Whoever runs the box can read it, and an attacker cannot, so
		// "my password is unchanged and it still will not let me in" is
		// answerable without handing anyone half the answer.
		log.Printf("%s refused: password did not match", attempt)
		return false, false, nil
	}

	ok, usedRecovery, refusal, fault := h.checkSecondFactor(
		cred, req.Code, req.RecoveryCode)
	if fault != nil {
		return false, false, fault
	}
	if !ok {
		log.Printf("%s refused: password was correct, %s", attempt, refusal)
	}
	return ok, usedRecovery, nil
}

// checkSecondFactor consumes a recovery code, or claims the TOTP step a
// code matched. A non-empty recovery code wins over a code, as it always
// has in Login. refusal explains a false ok, for the server log only.
//
// Single use holds under concurrency for both: the recovery code's
// UPDATE is conditional on used_at IS NULL, and the step claim on the
// stored step (repository.ClaimTOTPStep). So when a sign-in, a phone's
// sign-in and a backup step-up carry the same code at once, exactly one
// of them wins.
func (h *AdminAuthHandler) checkSecondFactor(
	cred *repository.AdminCredential,
	code, recovery string,
) (ok, usedRecovery bool, refusal string, fault *authFault) {
	if recovery != "" {
		spent, err := h.auth.SpendRecoveryCode(
			adminauth.HashToken(adminauth.NormalizeRecoveryCode(recovery)))
		if err != nil {
			return false, false, "",
				&authFault{"could not check the recovery code", err}
		}
		if !spent {
			return false, false, "recovery code unknown or already used", nil
		}
		return true, true, "", nil
	}

	now := clock().Unix()
	step, matched := adminauth.Verify(cred.TOTPSecret, code, now, cred.LastTOTPStep)
	if !matched {
		return false, false, fmt.Sprintf(
			"TOTP code rejected (server step %d, last accepted step %d)",
			now/30, cred.LastTOTPStep), nil
	}
	// Verify judged the code against the step read with the credential;
	// the claim decides, atomically, which of two concurrent requests
	// carrying it gets in.
	claimed, err := h.auth.ClaimTOTPStep(step, adminauth.StepCeiling(now))
	if err != nil {
		return false, false, "", &authFault{"could not record the TOTP step", err}
	}
	if !claimed {
		return false, false, "TOTP step already spent", nil
	}
	return true, false, "", nil
}

// --- the phone's admin sign-in ----------------------------------------

type deviceTokenRequest struct {
	Password     string `json:"password"`
	Code         string `json:"code"`
	RecoveryCode string `json:"recoveryCode"`
	DeviceLabel  string `json:"deviceLabel"`
	Persistent   bool   `json:"persistent"`
}

// IssueDeviceToken signs a phone in to manage the server: POST
// /api/v1/admin/auth/device-token, inside the rate-limited /admin/auth
// group, so it shares the 10-a-minute budget with the web sign-in.
//
// It asks for exactly what the web sign-in asks for — the password and a
// fresh second factor, through the same checkCredentials — and answers
// with an admin device token ("alxa_…", shown once, stored only as a
// hash) instead of a cookie. It never reads Authorization, so a token
// cannot mint another token, and it refuses outright when no login is
// enrolled, so the environment token can never become a phone's
// credential.
//
// Persistence is the phone's decision (plan amendment A1). The phone
// asks to stay signed in only when it reached this server through an
// https:// address whose certificate the OS validated. Behind a TLS-
// terminating reverse proxy this server sees plain HTTP and cannot check
// that for itself, so it honours the request and records how HTTPS was
// established instead (schemeBasis), for the owner to see on the web
// page — plain HTTP when a header says http, whatever was declared. A
// request that does not ask gets a 12-hour session.
func (h *AdminAuthHandler) IssueDeviceToken(c *fiber.Ctx) error {
	var req deviceTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body", "reason": "bad-request"})
	}

	cred, err := h.auth.Get()
	if errors.Is(err, repository.ErrNoCredentials) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "no admin login is set up on this server",
			"reason": "not-enrolled"})
	}
	if err != nil {
		return serverError(c, "could not read admin credentials", err)
	}

	ok, usedRecovery, fault := h.checkCredentials(cred, loginRequest{
		Password:     req.Password,
		Code:         req.Code,
		RecoveryCode: req.RecoveryCode,
	}, "admin device sign-in")
	if fault != nil {
		return serverError(c, fault.msg, fault.err)
	}
	if !ok {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "wrong password or code", "reason": "bad-credentials"})
	}

	token, err := adminauth.NewDeviceToken()
	if err != nil {
		return serverError(c, "could not create a device sign-in", err)
	}
	via := repository.AdminDeviceViaTOTP
	if usedRecovery {
		via = repository.AdminDeviceViaRecovery
	}
	dev, err := h.auth.CreateDeviceToken(adminauth.HashToken(token),
		repository.NewAdminDevice{
			Label:       sanitizeDeviceLabel(req.DeviceLabel),
			Persistent:  req.Persistent,
			CreatedVia:  via,
			SchemeBasis: schemeBasis(c, h.httpsDeclared, req.Persistent),
		}, clock())
	if err != nil {
		return serverError(c, "could not save the device sign-in", err)
	}

	log.Printf("admin device signed in: %q (%s, persistent=%v, via=%s, https=%s)",
		dev.Label, dev.ID, dev.Persistent, dev.CreatedVia,
		schemeBasisForLog(dev.SchemeBasis))

	remaining, _ := h.auth.RecoveryCodesRemaining()
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"token":                  token,
		"device":                 adminDeviceView(dev),
		"usedRecoveryCode":       usedRecovery,
		"recoveryCodesRemaining": remaining,
	})
}

// maxDeviceLabelRunes caps a phone's label in runes, not bytes, so a
// name in any script is never cut through the middle of a character.
const maxDeviceLabelRunes = 80

// sanitizeDeviceLabel makes a phone's self-chosen label safe to list:
// control characters dropped, outer space trimmed, at most 80 runes, and
// "phone" when nothing is left.
func sanitizeDeviceLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxDeviceLabelRunes {
		s = strings.TrimSpace(string(r[:maxDeviceLabelRunes]))
	}
	if s == "" {
		return "phone"
	}
	return s
}

// --- the phone's backup step-up ---------------------------------------

type stepUpRequest struct {
	Code         string `json:"code"`
	RecoveryCode string `json:"recoveryCode"`
}

// RequireFreshCode gates the phone's backup (POST /admin/backup) on a
// second factor typed for it, on top of the admin sign-in that got the
// request this far. The backup carries everyone's history, so a phone
// token alone — lost with the phone, say — is not enough to take it.
// The one other admin act that leads into anyone's history, approving a
// link, asks a phone for a fresh code the same way, every time
// (LinkHandler.ApproveLink, through FreshCode).
//
// A wrong code answers 401 step-up-failed, which the phone must NOT
// read as "signed out": its sign-in is untouched.
func (h *AdminAuthHandler) RequireFreshCode(c *fiber.Ctx) error {
	var req stepUpRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body", "reason": "bad-request"})
	}
	if ok, err := h.FreshCode(c, req.Code, req.RecoveryCode, "admin backup"); !ok {
		return err
	}
	return c.Next()
}

// StepUp spends a fresh second factor typed for one request, on top of
// the admin sign-in that got it this far. It answers a refusal itself:
// ok false means the response is written, and the caller returns err.
// AdminAuthHandler.FreshCode is the one implementation.
type StepUp func(c *fiber.Ctx, code, recoveryCode, action string) (ok bool, err error)

// FreshCode is RequireFreshCode's check, for a handler that needs a
// fresh code only sometimes. It consumes the factor exactly as a
// sign-in does (checkSecondFactor), so one code cannot serve a sign-in
// and a step-up, nor two step-ups. action names the attempt in the
// server log ("admin backup").
//
// Refusals: 401 not-enrolled when there is no login (reachable only
// with the environment token before enrolment: no code could be
// fresh), and 401 step-up-failed for a wrong, spent or raced code.
func (h *AdminAuthHandler) FreshCode(
	c *fiber.Ctx,
	code, recoveryCode, action string,
) (bool, error) {
	cred, err := h.auth.Get()
	if errors.Is(err, repository.ErrNoCredentials) {
		return false, c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "no admin login is set up on this server",
			"reason": "not-enrolled"})
	}
	if err != nil {
		return false, serverError(c, "could not read admin credentials", err)
	}

	ok, _, refusal, fault := h.checkSecondFactor(cred, code, recoveryCode)
	if fault != nil {
		return false, serverError(c, fault.msg, fault.err)
	}
	if !ok {
		log.Printf("%s step-up refused%s: %s", action, callerForLog(c), refusal)
		return false, c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "wrong code", "reason": "step-up-failed"})
	}
	return true, nil
}

// callerForLog names the admin device behind a request, for the server
// log: ` for "Chris · iPhone" (<id>)`, or "" for the browser's cookie.
func callerForLog(c *fiber.Ctx) string {
	id := middleware.AdminDevice(c)
	if id == "" {
		return ""
	}
	return fmt.Sprintf(" for %q (%s)", middleware.AdminDeviceLabel(c), id)
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
