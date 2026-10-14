package handlers

import (
	"errors"
	"fmt"
	"log"
	"strconv"
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
	// signIns is the allowance of wrong passwords and codes that every
	// sign-in draws on, every visitor together; stepUps is each signed-in
	// phone's own, for the codes its backups and approvals carry (see
	// adminauth.GuessLimit).
	signIns *adminauth.GuessLimit
	stepUps *adminauth.GuessLimit
	// accounts reads people's sign-ins, for a phone that signs in to
	// Manage server with a person's account (IssueAccountDeviceToken) and
	// for that phone's step-up codes (FreshCode). Set by UseAccounts.
	accounts *repository.AccountRepository
}

// UseAccounts gives the handler people's sign-ins, for admin access
// through a person's own account.
func (h *AdminAuthHandler) UseAccounts(accounts *repository.AccountRepository) {
	h.accounts = accounts
}

// everyone is the sign-ins' one allowance key.
const everyone = ""

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
		signIns: adminauth.NewGuessLimit(
			adminauth.SignInGuessBurst, adminauth.SignInGuessRefill),
		stepUps: adminauth.NewGuessLimit(
			adminauth.StepUpGuessBurst, adminauth.StepUpGuessRefill),
	}
}

// spendSignInGuess takes one guess from the sign-ins' shared allowance
// before a password or code is checked. ok false means the reply is
// written (429 guess-limit). The caller refunds a right answer.
func (h *AdminAuthHandler) spendSignInGuess(c *fiber.Ctx, attempt string) (bool, error) {
	ok, wait, announce := h.signIns.Take(everyone)
	if ok {
		return true, nil
	}
	if announce {
		log.Printf("%s refused: more wrong passwords or codes than the "+
			"admin login takes from everyone together (one every %v). "+
			"Someone is guessing at it from several addresses",
			attempt, adminauth.SignInGuessRefill)
	}
	return false, guessLimited(c, wait,
		"too many wrong passwords or codes on this server lately")
}

// spendStepUpGuess is spendSignInGuess for a step-up code, from the
// calling phone's own allowance: nobody else can use it up.
func (h *AdminAuthHandler) spendStepUpGuess(c *fiber.Ctx, action string) (bool, error) {
	ok, wait, announce := h.stepUps.Take(stepUpKey(c))
	if ok {
		return true, nil
	}
	if announce {
		log.Printf("%s step-up refused%s: too many wrong codes from this "+
			"device lately", action, callerForLog(c))
	}
	return false, guessLimited(c, wait, "too many wrong codes from this device lately")
}

// stepUpKey is whose allowance a step-up draws on: the phone's admin
// device id, or the browser's session for a cookie.
func stepUpKey(c *fiber.Ctx) string {
	if id := middleware.AdminDevice(c); id != "" {
		return "device:" + id
	}
	return "browser"
}

// guessLimited answers 429 guess-limit, with how long until the next
// guess.
func guessLimited(c *fiber.Ctx, wait time.Duration, msg string) error {
	secs := int((wait + time.Second - 1) / time.Second)
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(secs))
	return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
		"error":  fmt.Sprintf("%s; try again in %d seconds", msg, secs),
		"reason": "guess-limit",
	})
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

	if ok, err := h.spendSignInGuess(c, "admin sign-in"); !ok {
		return err
	}
	ok, usedRecovery, fault := h.checkCredentials(cred, req, "admin sign-in")
	if fault != nil {
		return serverError(c, fault.msg, fault.err)
	}
	if !ok {
		return c.Status(fiber.StatusUnauthorized).
			JSON(fiber.Map{"error": "wrong password or code"})
	}
	h.signIns.Refund(everyone)

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

	if ok, err := h.spendSignInGuess(c, "admin device sign-in"); !ok {
		return err
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
	h.signIns.Refund(everyone)

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
	if profileID := middleware.AdminProfile(c); profileID != "" {
		return h.accountFreshCode(c, profileID, code, recoveryCode, action)
	}
	cred, err := h.auth.Get()
	if errors.Is(err, repository.ErrNoCredentials) {
		return false, c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "no admin login is set up on this server",
			"reason": "not-enrolled"})
	}
	if err != nil {
		return false, serverError(c, "could not read admin credentials", err)
	}

	if ok, err := h.spendStepUpGuess(c, action); !ok {
		return false, err
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
	h.stepUps.Refund(stepUpKey(c))
	return true, nil
}

// accountFreshCode is FreshCode for a phone signed in with a person's
// account: the code comes from THEIR authenticator and is spent on THEIR
// sign-in, never the admin login's, so the household's codes cannot vouch
// for an account's phone nor the other way round. Recovery codes are the
// admin login's alone. Same refusals and the same per-phone allowance as
// the admin login's step-ups.
func (h *AdminAuthHandler) accountFreshCode(
	c *fiber.Ctx,
	profileID, code, recoveryCode, action string,
) (bool, error) {
	if h.accounts == nil {
		return false, serverError(c, "people's sign-ins are not available",
			errors.New("AdminAuthHandler.UseAccounts was never called"))
	}
	if ok, err := h.spendStepUpGuess(c, action); !ok {
		return false, err
	}
	refused := func(why string) (bool, error) {
		log.Printf("%s step-up refused%s: %s", action, callerForLog(c), why)
		return false, c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "wrong code", "reason": "step-up-failed"})
	}
	login, err := h.accounts.LoginByProfile(profileID)
	switch {
	case errors.Is(err, repository.ErrNoLogin):
		return refused("the account's sign-in is gone")
	case err != nil:
		return false, serverError(c, "could not read the sign-in", err)
	}
	if recoveryCode != "" {
		return refused("a recovery code belongs to the admin login, not an account")
	}
	if !login.TOTPEnabled {
		return refused("the account has no two-step")
	}
	ok, err := h.spendAccountCode(login, code)
	if err != nil {
		return false, serverError(c, "could not check the code", err)
	}
	if !ok {
		return refused("the account's two-step code was wrong or spent")
	}
	h.stepUps.Refund(stepUpKey(c))
	return true, nil
}

// spendAccountCode checks a code from a person's authenticator and spends
// its time step (AccountRepository.SpendTOTPStep), so one code serves one
// request — the same rule the account's own sign-in keeps.
func (h *AdminAuthHandler) spendAccountCode(login *repository.AccountLogin, code string) (bool, error) {
	secret, err := h.accounts.OpenTOTP(login)
	if err != nil || secret == "" {
		return false, err
	}
	step, ok := adminauth.Verify(secret, code, clock().Unix(), login.LastTOTPStep)
	if !ok {
		return false, nil
	}
	return h.accounts.SpendTOTPStep(login.ProfileID, step, false)
}

// --- Manage server with a person's own account ------------------------

type accountDeviceTokenRequest struct {
	Password    string `json:"password"`
	Code        string `json:"code"`
	DeviceLabel string `json:"deviceLabel"`
	Persistent  bool   `json:"persistent"`
}

// IssueAccountDeviceToken signs a phone in to Manage server with a
// person's OWN account (owner, 2026-10-13: admin access per person, set on
// the admin page): POST /api/v1/accounts/admin-sign-in, on a phone signed
// in with that account (RequireProfile), for the account's password and a
// fresh code from its authenticator. The answer is the admin device token
// IssueDeviceToken gives, carrying the account's access as its scope;
// middleware.AdminScopeGate holds it to that, and FreshCode asks this
// account's authenticator for its step-ups.
//
// Refusals: 404 no-login; 403 no-admin-access; 403 two-step-required;
// 409 not-enrolled (the admin login is not set up, so no admin sign-in
// works yet); 429 locked; 401 code-required; 401 bad-credentials for a
// wrong password or code, which counts against the account as a wrong
// sign-in does (enough of them pause it) and against everyone's sign-in
// allowance.
func (h *AdminAuthHandler) IssueAccountDeviceToken(c *fiber.Ctx) error {
	var req accountDeviceTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body", "reason": "bad-request"})
	}
	if h.accounts == nil {
		return serverError(c, "people's sign-ins are not available",
			errors.New("AdminAuthHandler.UseAccounts was never called"))
	}
	enrolled, err := h.auth.Enrolled()
	if err != nil {
		return serverError(c, "could not read admin credentials", err)
	}
	if !enrolled {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error":  "this server's admin login is not set up yet",
			"reason": "not-enrolled"})
	}
	login, err := h.accounts.LoginByProfile(middleware.ProfileID(c))
	switch {
	case errors.Is(err, repository.ErrNoLogin):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "this device is not signed in with an account", "reason": "no-login"})
	case err != nil:
		return serverError(c, "could not read the sign-in", err)
	}
	if login.AdminAccess == "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "this account has no admin access on this server", "reason": "no-admin-access"})
	}
	if !login.TOTPEnabled {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "turn on two-step for this account first", "reason": "two-step-required"})
	}
	now := clock()
	if login.LockedUntil > now.Unix() {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":             "too many wrong attempts; this sign-in is paused",
			"reason":            "locked",
			"retryAfterSeconds": login.LockedUntil - now.Unix(),
		})
	}
	if ok, err := h.spendSignInGuess(c, "account admin sign-in"); !ok {
		return err
	}
	wrong := func(why string) error {
		log.Printf("account admin sign-in refused for %q: %s", login.Username, why)
		if until, err := h.accounts.RecordFailure(login.ProfileID, now); err == nil && until > 0 {
			log.Printf("account sign-in %q paused until %s after repeated wrong answers",
				login.Username, time.Unix(until, 0).Format(time.RFC3339))
		}
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "wrong password or code", "reason": "bad-credentials"})
	}
	// The password before the code, for the reason checkCredentials gives:
	// a code is spent when it matches, so it must not be spendable by
	// someone who does not know the password.
	if !adminauth.VerifyPassword(login.PasswordHash, req.Password) {
		return wrong("password did not match")
	}
	if strings.TrimSpace(req.Code) == "" {
		h.signIns.Refund(everyone)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "enter the code from your authenticator app", "reason": "code-required"})
	}
	ok, err := h.spendAccountCode(login, req.Code)
	if err != nil {
		return serverError(c, "could not check the code", err)
	}
	if !ok {
		return wrong("the two-step code was wrong or spent")
	}
	h.signIns.Refund(everyone)
	_ = h.accounts.RecordSuccess(login.ProfileID, now)

	token, err := adminauth.NewDeviceToken()
	if err != nil {
		return serverError(c, "could not create a device sign-in", err)
	}
	dev, err := h.auth.CreateDeviceToken(adminauth.HashToken(token),
		repository.NewAdminDevice{
			Label:       sanitizeDeviceLabel(req.DeviceLabel),
			Persistent:  req.Persistent,
			CreatedVia:  repository.AdminDeviceViaAccount,
			SchemeBasis: schemeBasis(c, h.httpsDeclared, req.Persistent),
			ProfileID:   login.ProfileID,
			Scope:       login.AdminAccess,
		}, now)
	if err != nil {
		return serverError(c, "could not save the device sign-in", err)
	}
	log.Printf("admin device signed in with %q's account: %q (%s, access=%s, persistent=%v, https=%s)",
		login.Username, dev.Label, dev.ID, dev.Scope, dev.Persistent,
		schemeBasisForLog(dev.SchemeBasis))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"token":            token,
		"device":           adminDeviceView(dev),
		"access":           dev.Scope,
		"usedRecoveryCode": false,
	})
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
