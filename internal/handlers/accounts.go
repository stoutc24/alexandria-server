package handlers

// Alexandria Accounts over HTTP (owner, 2026-10-12; design doc
// https://claude.ai/artifact/S7muLG9WhVTAcrrpFJu4H2).
//
// Three audiences, three sets of routes:
//
//   - Anyone, rate limited: check an invite, accept it (set a password),
//     sign in. A successful sign-in IS the device link — no admin approval
//     — and answers with an ordinary profile token, so Party Play, stats and
//     the watch work at once.
//   - A signed-in device (RequireProfile): who am I, my services (sealed to
//     this device's key), my password, my two-step code.
//   - The admin (RequireAdmin): a person's sign-in, their invite, their
//     services. The admin can set a service login but never read one back.
//
// Nothing here grants an entitlement; Premium stays on the device.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

type AccountsHandler struct {
	accounts *repository.AccountRepository
	profiles *repository.ProfileRepository
	// stepUp: an invite lets someone into a person's profile, so a phone's
	// admin sign-in adds a fresh code to each, as it does to a link
	// approval (LinkHandler.ApproveLink).
	stepUp StepUp
	// guesses is one allowance of wrong answers for every visitor together
	// (adminauth.GuessLimit), on top of the per-client rate limit and each
	// sign-in's own lockout.
	guesses *adminauth.GuessLimit
	// decoy is verified when a username does not exist, so "no such user"
	// takes as long as "wrong password" and names cannot be probed by
	// timing.
	decoy string
	now   func() time.Time
}

func NewAccountsHandler(
	accounts *repository.AccountRepository,
	profiles *repository.ProfileRepository,
	stepUp StepUp,
) *AccountsHandler {
	decoy, err := adminauth.HashPassword("decoy-password-never-matches")
	if err != nil {
		log.Fatalf("accounts: could not prepare the decoy hash: %v", err)
	}
	return &AccountsHandler{
		accounts: accounts,
		profiles: profiles,
		stepUp:   stepUp,
		guesses:  adminauth.NewGuessLimit(adminauth.SignInGuessBurst, adminauth.SignInGuessRefill),
		decoy:    decoy,
		now:      time.Now,
	}
}

func refuse(c *fiber.Ctx, status int, reason, msg string) error {
	return c.Status(status).JSON(fiber.Map{"error": msg, "reason": reason})
}

type deviceBody struct {
	Label     string `json:"label"`
	PublicKey string `json:"publicKey"`
}

func (d deviceBody) label() string {
	l := strings.TrimSpace(d.Label)
	if l == "" {
		l = "Device"
	}
	if len(l) > 80 {
		l = l[:80]
	}
	return l
}

// signedIn mints the device's profile token, stores its key, and writes
// the answer every successful sign-in shares.
func (h *AccountsHandler) signedIn(
	c *fiber.Ctx, status int, login *repository.AccountLogin, device deviceBody,
) error {
	pub, err := vault.ParseDeviceKey(device.PublicKey)
	if err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-device-key", err.Error())
	}
	token, err := h.profiles.MintToken(login.ProfileID, device.label())
	if err != nil {
		return serverError(c, "could not link the device", err)
	}
	caller, err := h.profiles.FindByToken(token)
	if err != nil {
		return serverError(c, "could not read the new device", err)
	}
	if err := h.accounts.SetDeviceKey(caller.TokenID, vault.EncodeDeviceKey(pub), h.now()); err != nil {
		return serverError(c, "could not store the device key", err)
	}
	log.Printf("account sign-in: %q on %q", login.Username, device.label())
	return c.Status(status).JSON(fiber.Map{
		"deviceToken": token,
		"deviceId":    caller.TokenID,
		"profile":     fiber.Map{"id": login.ProfileID, "name": login.ProfileName},
		"username":    login.Username,
		// Whether this server can hand over service logins at all.
		"servicesAvailable": h.accounts.VaultOn(),
	})
}

func serverDisplayName() string {
	if configuredName != "" {
		return configuredName
	}
	host, _ := os.Hostname()
	return host
}

// spendGuess takes one of every visitor's shared wrong-answer allowance.
func (h *AccountsHandler) spendGuess(c *fiber.Ctx) bool {
	ok, wait, announce := h.guesses.Take("accounts")
	if !ok {
		if announce {
			log.Printf("accounts: sign-in guesses exhausted for every visitor; waiting %s", wait.Round(time.Second))
		}
		_ = c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":             "too many sign-in attempts right now; wait a moment",
			"reason":            "busy",
			"retryAfterSeconds": int(wait.Seconds()) + 1,
		})
	}
	return ok
}

// ── public: invites and sign-in ─────────────────────────────────────

type inviteBody struct {
	Invite string `json:"invite"`
}

// CheckInvite says who an invite is for, so the app can greet the person
// by name before they choose a password.
func (h *AccountsHandler) CheckInvite(c *fiber.Ctx) error {
	var body inviteBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	if !h.spendGuess(c) {
		return nil
	}
	inv, err := h.accounts.FindInvite(body.Invite, h.now())
	if errors.Is(err, repository.ErrInviteInvalid) {
		return refuse(c, fiber.StatusNotFound, "invite-invalid", err.Error())
	}
	if err != nil {
		return serverError(c, "could not read the invite", err)
	}
	h.guesses.Refund("accounts")
	out := fiber.Map{
		"profileName": inv.ProfileName,
		"purpose":     inv.Purpose,
		"expiresAt":   inv.ExpiresAt,
		"serverName":  serverDisplayName(),
	}
	if inv.Purpose == "reset" {
		if login, err := h.accounts.LoginByProfile(inv.ProfileID); err == nil {
			out["username"] = login.Username
		}
	} else if suggested, err := repository.NormalizeUsername(strings.ReplaceAll(inv.ProfileName, " ", ".")); err == nil {
		out["suggestedUsername"] = suggested
	}
	return c.JSON(out)
}

type acceptBody struct {
	Invite   string     `json:"invite"`
	Username string     `json:"username"`
	Password string     `json:"password"`
	Device   deviceBody `json:"device"`
}

// AcceptInvite sets the password an invite promised and signs this device
// in: a first sign-in for "invite", a new password for "reset".
func (h *AccountsHandler) AcceptInvite(c *fiber.Ctx) error {
	var body acceptBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	if !h.spendGuess(c) {
		return nil
	}
	now := h.now()
	inv, err := h.accounts.FindInvite(body.Invite, now)
	if errors.Is(err, repository.ErrInviteInvalid) {
		return refuse(c, fiber.StatusNotFound, "invite-invalid", err.Error())
	}
	if err != nil {
		return serverError(c, "could not read the invite", err)
	}
	h.guesses.Refund("accounts")
	if _, err := vault.ParseDeviceKey(body.Device.PublicKey); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-device-key", err.Error())
	}

	username := ""
	if inv.Purpose == "invite" {
		username, err = repository.NormalizeUsername(body.Username)
		if err != nil {
			return refuse(c, fiber.StatusBadRequest, "bad-username", err.Error())
		}
		free, err := h.accounts.UsernameAvailable(username, inv.ProfileID)
		if err != nil {
			return serverError(c, "could not check the username", err)
		}
		if !free {
			return refuse(c, fiber.StatusConflict, "username-taken", repository.ErrUsernameTaken.Error())
		}
	}
	hash, err := adminauth.HashPasswordAtLeast(body.Password, repository.MinAccountPasswordLength)
	if err != nil {
		return refuse(c, fiber.StatusBadRequest, "password-too-short", err.Error())
	}
	switch err := h.accounts.AcceptInvite(inv, username, hash, now); {
	case errors.Is(err, repository.ErrInviteInvalid):
		return refuse(c, fiber.StatusNotFound, "invite-invalid", err.Error())
	case errors.Is(err, repository.ErrUsernameTaken):
		return refuse(c, fiber.StatusConflict, "username-taken", err.Error())
	case err != nil:
		return serverError(c, "could not accept the invite", err)
	}
	login, err := h.accounts.LoginByProfile(inv.ProfileID)
	if err != nil {
		return serverError(c, "could not read the new sign-in", err)
	}
	_ = h.accounts.RecordSuccess(inv.ProfileID, now)
	return h.signedIn(c, fiber.StatusCreated, login, body.Device)
}

type loginBody struct {
	Username string     `json:"username"`
	Password string     `json:"password"`
	Code     string     `json:"code"`
	Device   deviceBody `json:"device"`
}

// Login signs a device in with a username, password and — when the person
// turned it on — a two-step code. The answer is the device's own token.
func (h *AccountsHandler) Login(c *fiber.Ctx) error {
	var body loginBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	if !h.spendGuess(c) {
		return nil
	}
	now := h.now()
	login, err := h.accounts.LoginByUsername(body.Username)
	if errors.Is(err, repository.ErrNoLogin) {
		adminauth.VerifyPassword(h.decoy, body.Password)
		return refuse(c, fiber.StatusUnauthorized, "bad-credentials", "wrong username or password")
	}
	if err != nil {
		return serverError(c, "could not read the sign-in", err)
	}
	if login.LockedUntil > now.Unix() {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error":             "too many wrong attempts; this sign-in is paused",
			"reason":            "locked",
			"retryAfterSeconds": login.LockedUntil - now.Unix(),
		})
	}
	if !adminauth.VerifyPassword(login.PasswordHash, body.Password) {
		h.fail(login, now)
		return refuse(c, fiber.StatusUnauthorized, "bad-credentials", "wrong username or password")
	}
	if login.TOTPEnabled {
		if strings.TrimSpace(body.Code) == "" {
			// The password was right: not a failure, just the next step.
			h.guesses.Refund("accounts")
			return refuse(c, fiber.StatusUnauthorized, "code-required", "enter the code from your authenticator app")
		}
		ok, err := h.checkCode(login, body.Code, now, false)
		if err != nil {
			return serverError(c, "could not check the code", err)
		}
		if !ok {
			h.fail(login, now)
			return refuse(c, fiber.StatusUnauthorized, "bad-code", "wrong code")
		}
	}
	h.guesses.Refund("accounts")
	_ = h.accounts.RecordSuccess(login.ProfileID, now)
	return h.signedIn(c, fiber.StatusOK, login, body.Device)
}

func (h *AccountsHandler) fail(login *repository.AccountLogin, now time.Time) {
	if until, err := h.accounts.RecordFailure(login.ProfileID, now); err == nil && until > 0 {
		log.Printf("account sign-in %q paused until %s after repeated wrong answers",
			login.Username, time.Unix(until, 0).Format(time.RFC3339))
	}
}

// checkCode verifies a two-step code and spends its time step, so the same
// code cannot be used twice. enable switches two-step on with it.
func (h *AccountsHandler) checkCode(login *repository.AccountLogin, code string, now time.Time, enable bool) (bool, error) {
	secret, err := h.accounts.OpenTOTP(login)
	if err != nil || secret == "" {
		return false, err
	}
	step, ok := adminauth.Verify(secret, code, now.Unix(), login.LastTOTPStep)
	if !ok {
		return false, nil
	}
	return h.accounts.SpendTOTPStep(login.ProfileID, step, enable)
}

// ── a signed-in device ──────────────────────────────────────────────

// Me says who this device is signed in as.
func (h *AccountsHandler) Me(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	out := fiber.Map{
		"profile":           fiber.Map{"id": profileID, "name": middleware.ProfileName(c)},
		"hasLogin":          false,
		"servicesAvailable": h.accounts.VaultOn(),
	}
	login, err := h.accounts.LoginByProfile(profileID)
	switch {
	case errors.Is(err, repository.ErrNoLogin):
	case err != nil:
		return serverError(c, "could not read the sign-in", err)
	default:
		out["hasLogin"] = true
		out["username"] = login.Username
		out["totpEnabled"] = login.TOTPEnabled
	}
	if _, err := h.accounts.DeviceKey(deviceID(c)); err == nil {
		out["deviceKey"] = true
	}
	return c.JSON(out)
}

func deviceID(c *fiber.Ctx) string {
	id, _ := middleware.Device(c)
	return id
}

// Services hands this device its services, sealed to the key it signed in
// with. ?since=<revision> answers "unchanged" without sending anything
// when nothing has changed, which is how a device checks cheaply.
func (h *AccountsHandler) Services(c *fiber.Ctx) error {
	if !h.accounts.VaultOn() {
		return refuse(c, fiber.StatusServiceUnavailable, "secrets-key-missing",
			"this server has no secrets key, so it keeps no service logins")
	}
	profileID := middleware.ProfileID(c)
	revision, err := h.accounts.ServicesRevision(profileID)
	if err != nil {
		return serverError(c, "could not read the services", err)
	}
	if since := strings.TrimSpace(c.Query("since")); since != "" && since == revision {
		return c.JSON(fiber.Map{"revision": revision, "unchanged": true})
	}
	key, err := h.accounts.DeviceKey(deviceID(c))
	if errors.Is(err, repository.ErrNoDeviceKey) {
		return refuse(c, fiber.StatusConflict, "device-key-required",
			"this device has no key yet; send one to PUT /api/v1/accounts/device-key")
	}
	if err != nil {
		return serverError(c, "could not read the device key", err)
	}
	pub, err := vault.ParseDeviceKey(key)
	if err != nil {
		return serverError(c, "the stored device key is unreadable", err)
	}
	services, err := h.accounts.ListServices(profileID, true)
	if err != nil {
		return serverError(c, "could not read the services", err)
	}
	type sealedService struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		BaseURL   string `json:"baseUrl"`
		LoginKind string `json:"loginKind"`
		Priority  int    `json:"priority"`
		Username  string `json:"username,omitempty"`
		Password  string `json:"password,omitempty"`
		Token     string `json:"token,omitempty"`
		APIKey    string `json:"apiKey,omitempty"`
		// Plex: the picked person's server, name and addresses
		// ("plexuser"); Token is their key to that server.
		Plex *repository.PlexService `json:"plex,omitempty"`
		// Unreadable: the server key changed since the admin saved this
		// login; the device keeps whatever it already has.
		Unreadable bool `json:"unreadable,omitempty"`
	}
	payload := struct {
		Revision string          `json:"revision"`
		Services []sealedService `json:"services"`
	}{Revision: revision, Services: []sealedService{}}
	for _, s := range services {
		out := sealedService{ID: s.ID, Type: s.Type, Name: s.Name, BaseURL: s.BaseURL,
			LoginKind: s.LoginKind, Priority: s.Priority, Plex: s.Plex, Unreadable: s.Unreadable}
		if s.Credentials != nil {
			out.Username = s.Credentials.Username
			out.Password = s.Credentials.Password
			out.Token = s.Credentials.Token
			out.APIKey = s.Credentials.APIKey
		}
		payload.Services = append(payload.Services, out)
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return serverError(c, "could not prepare the services", err)
	}
	box, err := vault.SealToDevice(pub, plain)
	if err != nil {
		return serverError(c, "could not seal the services", err)
	}
	return c.JSON(fiber.Map{"revision": revision, "box": box})
}

type deviceKeyBody struct {
	PublicKey string `json:"publicKey"`
}

// SetDeviceKey gives a device linked the old way (a link code) a key, once,
// so its services can be sealed to it too.
func (h *AccountsHandler) SetDeviceKey(c *fiber.Ctx) error {
	var body deviceKeyBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	pub, err := vault.ParseDeviceKey(body.PublicKey)
	if err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-device-key", err.Error())
	}
	switch err := h.accounts.SetDeviceKey(deviceID(c), vault.EncodeDeviceKey(pub), h.now()); {
	case errors.Is(err, repository.ErrDeviceKeyExists):
		return refuse(c, fiber.StatusConflict, "device-key-exists",
			"this device already has a key; sign in again to replace it")
	case err != nil:
		return serverError(c, "could not store the device key", err)
	}
	return c.JSON(fiber.Map{"deviceKey": true})
}

type passwordBody struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// ChangePassword replaces the signed-in person's password. Other devices
// stay signed in: their tokens are not the password.
func (h *AccountsHandler) ChangePassword(c *fiber.Ctx) error {
	var body passwordBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	now := h.now()
	login, err := h.accounts.LoginByProfile(middleware.ProfileID(c))
	if errors.Is(err, repository.ErrNoLogin) {
		return refuse(c, fiber.StatusNotFound, "no-login", err.Error())
	}
	if err != nil {
		return serverError(c, "could not read the sign-in", err)
	}
	if login.LockedUntil > now.Unix() {
		return refuse(c, fiber.StatusTooManyRequests, "locked", "too many wrong attempts; try again later")
	}
	if !adminauth.VerifyPassword(login.PasswordHash, body.CurrentPassword) {
		h.fail(login, now)
		return refuse(c, fiber.StatusUnauthorized, "bad-credentials", "the current password is wrong")
	}
	hash, err := adminauth.HashPasswordAtLeast(body.NewPassword, repository.MinAccountPasswordLength)
	if err != nil {
		return refuse(c, fiber.StatusBadRequest, "password-too-short", err.Error())
	}
	if err := h.accounts.SetPassword(login.ProfileID, hash, now); err != nil {
		return serverError(c, "could not change the password", err)
	}
	return c.JSON(fiber.Map{"changed": true})
}

// TOTPSetup starts two-step: a fresh secret for the person's authenticator
// app, switched on only once a code from it is confirmed (TOTPEnable).
func (h *AccountsHandler) TOTPSetup(c *fiber.Ctx) error {
	if !h.accounts.VaultOn() {
		return refuse(c, fiber.StatusServiceUnavailable, "secrets-key-missing",
			"two-step codes need the server's secrets key")
	}
	login, err := h.accounts.LoginByProfile(middleware.ProfileID(c))
	if errors.Is(err, repository.ErrNoLogin) {
		return refuse(c, fiber.StatusNotFound, "no-login", err.Error())
	}
	if err != nil {
		return serverError(c, "could not read the sign-in", err)
	}
	if login.TOTPEnabled {
		return refuse(c, fiber.StatusConflict, "totp-enabled", "two-step is already on; turn it off first")
	}
	secret, err := adminauth.NewSecret()
	if err != nil {
		return serverError(c, "could not make a secret", err)
	}
	if err := h.accounts.SetPendingTOTP(login.ProfileID, secret, h.now()); err != nil {
		return serverError(c, "could not store the secret", err)
	}
	return c.JSON(fiber.Map{
		"secret": secret,
		"uri":    adminauth.ProvisioningURI(secret, login.Username, "Alexandria"),
	})
}

type codeBody struct {
	Code     string `json:"code"`
	Password string `json:"password"`
}

// TOTPEnable confirms a code from the new secret and switches two-step on.
func (h *AccountsHandler) TOTPEnable(c *fiber.Ctx) error {
	var body codeBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	login, err := h.accounts.LoginByProfile(middleware.ProfileID(c))
	if err != nil {
		return refuse(c, fiber.StatusNotFound, "no-login", "this person has no sign-in")
	}
	if login.TOTPSecret == "" {
		return refuse(c, fiber.StatusConflict, "no-pending-secret", "start two-step setup first")
	}
	ok, err := h.checkCode(login, body.Code, h.now(), true)
	if err != nil {
		return serverError(c, "could not check the code", err)
	}
	if !ok {
		return refuse(c, fiber.StatusUnauthorized, "bad-code", "wrong code")
	}
	return c.JSON(fiber.Map{"totpEnabled": true})
}

// TOTPDisable switches two-step off, for the password.
func (h *AccountsHandler) TOTPDisable(c *fiber.Ctx) error {
	var body codeBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	now := h.now()
	login, err := h.accounts.LoginByProfile(middleware.ProfileID(c))
	if err != nil {
		return refuse(c, fiber.StatusNotFound, "no-login", "this person has no sign-in")
	}
	if !adminauth.VerifyPassword(login.PasswordHash, body.Password) {
		h.fail(login, now)
		return refuse(c, fiber.StatusUnauthorized, "bad-credentials", "wrong password")
	}
	if err := h.accounts.DisableTOTP(login.ProfileID, now); err != nil {
		return serverError(c, "could not switch two-step off", err)
	}
	return c.JSON(fiber.Map{"totpEnabled": false})
}

// ── the admin ───────────────────────────────────────────────────────

func (h *AccountsHandler) profileFromPath(c *fiber.Ctx) (*repository.Profile, error) {
	p, err := h.profiles.GetByID(c.Params("id"))
	if errors.Is(err, repository.ErrProfileNotFound) {
		return nil, refuse(c, fiber.StatusNotFound, "profile-not-found", "profile not found")
	}
	if err != nil {
		return nil, serverError(c, "could not read the profile", err)
	}
	return p, nil
}

// Account is the admin's view of one person's sign-in, invite and
// services. No secret crosses it.
func (h *AccountsHandler) Account(c *fiber.Ctx) error {
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	now := h.now()
	out := fiber.Map{
		"profile":              fiber.Map{"id": p.ID, "name": p.Name},
		"login":                nil,
		"invite":               nil,
		"secretsKeyConfigured": h.accounts.VaultOn(),
		"serviceTypes":         repository.ServiceTypes,
	}
	login, err := h.accounts.LoginByProfile(p.ID)
	switch {
	case errors.Is(err, repository.ErrNoLogin):
	case err != nil:
		return serverError(c, "could not read the sign-in", err)
	default:
		l := fiber.Map{
			"username":    login.Username,
			"totpEnabled": login.TOTPEnabled,
			"createdAt":   login.CreatedAt,
			"locked":      login.LockedUntil > now.Unix(),
		}
		if login.LastLoginAt > 0 {
			l["lastLoginAt"] = login.LastLoginAt
		}
		out["login"] = l
	}
	if inv, err := h.accounts.PendingInvite(p.ID, now); err != nil {
		return serverError(c, "could not read the invite", err)
	} else if inv != nil {
		out["invite"] = fiber.Map{"purpose": inv.Purpose, "createdAt": inv.CreatedAt, "expiresAt": inv.ExpiresAt}
	}
	services, err := h.accounts.ListServices(p.ID, false)
	if err != nil {
		return serverError(c, "could not read the services", err)
	}
	out["services"] = services
	return c.JSON(out)
}

// Summary is the People list's line for each person, everyone at once,
// so the admin page can show who has a sign-in without opening anyone:
// the sign-in's username, two-step and pause, a waiting invite (its
// purpose and expiry), and how many services are set up. The same facts
// Account gives for one person, read the same way, and like it no
// secret: no code, token, password, key or address.
func (h *AccountsHandler) Summary(c *fiber.Ctx) error {
	ids, err := h.profiles.ListIDs()
	if err != nil {
		return serverError(c, "could not list the people", err)
	}
	now := h.now()
	people := make([]fiber.Map, 0, len(ids))
	for _, id := range ids {
		p := fiber.Map{"profileId": id, "login": nil, "invite": nil}
		login, err := h.accounts.LoginByProfile(id)
		switch {
		case errors.Is(err, repository.ErrNoLogin):
		case err != nil:
			return serverError(c, "could not read a sign-in", err)
		default:
			l := fiber.Map{
				"username":    login.Username,
				"totpEnabled": login.TOTPEnabled,
				"locked":      login.LockedUntil > now.Unix(),
			}
			if login.LastLoginAt > 0 {
				l["lastLoginAt"] = login.LastLoginAt
			}
			p["login"] = l
		}
		if inv, err := h.accounts.PendingInvite(id, now); err != nil {
			return serverError(c, "could not read an invite", err)
		} else if inv != nil {
			p["invite"] = fiber.Map{"purpose": inv.Purpose, "expiresAt": inv.ExpiresAt}
		}
		services, err := h.accounts.ListServices(id, false)
		if err != nil {
			return serverError(c, "could not read the services", err)
		}
		p["services"] = len(services)
		people = append(people, p)
	}
	return c.JSON(fiber.Map{"people": people, "secretsKeyConfigured": h.accounts.VaultOn()})
}

type adminInviteBody struct {
	// A phone's admin sign-in adds a fresh code to each invite.
	TOTPCode     string `json:"totpCode"`
	RecoveryCode string `json:"recoveryCode"`
}

// CreateInvite makes a person's invite: a first sign-in, or a new password
// when they already have one. The token and code are shown once.
func (h *AccountsHandler) CreateInvite(c *fiber.Ctx) error {
	var body adminInviteBody
	_ = c.BodyParser(&body)
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	if middleware.AdminDevice(c) != "" {
		totp := strings.TrimSpace(body.TOTPCode)
		recovery := strings.TrimSpace(body.RecoveryCode)
		if totp == "" && recovery == "" {
			return refuse(c, fiber.StatusForbidden, "step-up-required",
				"an invite from a phone needs a fresh code from your authenticator")
		}
		if ok, err := h.stepUp(c, totp, recovery, "account invite"); !ok {
			return err
		}
	}
	purpose := "invite"
	if _, err := h.accounts.LoginByProfile(p.ID); err == nil {
		purpose = "reset"
	}
	token, code, inv, err := h.accounts.CreateInvite(p.ID, purpose, repository.InviteTTL, h.now())
	if err != nil {
		return serverError(c, "could not create the invite", err)
	}
	server := configuredPublicURL
	if server == "" {
		server = c.BaseURL()
	}
	q := url.Values{}
	q.Set("server", server)
	q.Set("invite", token)
	log.Printf("account %s created for %q%s", purpose, p.Name, callerForLog(c))
	out := fiber.Map{
		"invite": fiber.Map{
			"token":       token,
			"code":        code,
			"purpose":     purpose,
			"expiresAt":   inv.ExpiresAt,
			"profileName": p.Name,
		},
		"serverUrl": server,
		// The link the app opens; the QR code carries the same.
		"link": "alexandria://accounts/invite?" + q.Encode(),
		// The link to SHARE: messaging apps make web links tappable, not
		// alexandria:// ones. It opens this server's /invite page, which
		// hands the invite to the app; the invite rides in the fragment, so
		// it never reaches a log.
		"webLink": strings.TrimRight(server, "/") + "/invite#invite=" + token,
	}
	if w := unreachableLinkWarning(server); w != "" {
		out["serverUrlWarning"] = w
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

// unreachableLinkWarning: the link carries the address the admin reached
// this page at, and one opened on the server itself (localhost) sends every
// phone to its OWN loopback.
func unreachableLinkWarning(server string) string {
	u, err := url.Parse(server)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return ""
	}
	return "This link points at " + host + ", which their phone can't reach. Open " +
		"this page at the server's network address (or set ALEXANDRIA_PUBLIC_URL) " +
		"and make the invite again — or give them the code to type with the " +
		"server's address."
}

// CancelInvite withdraws a person's waiting invite.
func (h *AccountsHandler) CancelInvite(c *fiber.Ctx) error {
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	if err := h.accounts.CancelInvites(p.ID); err != nil {
		return serverError(c, "could not cancel the invite", err)
	}
	return c.JSON(fiber.Map{"cancelled": true})
}

// RemoveLogin deletes a person's sign-in. Their devices stay linked and
// their services stay set up.
func (h *AccountsHandler) RemoveLogin(c *fiber.Ctx) error {
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	if err := h.accounts.DeleteLogin(p.ID); errors.Is(err, repository.ErrNoLogin) {
		return refuse(c, fiber.StatusNotFound, "no-login", err.Error())
	} else if err != nil {
		return serverError(c, "could not remove the sign-in", err)
	}
	_ = h.accounts.CancelInvites(p.ID)
	log.Printf("account sign-in removed for %q%s", p.Name, callerForLog(c))
	return c.JSON(fiber.Map{"removed": true})
}

// ListServices is the admin's list of a person's services, no secrets.
func (h *AccountsHandler) ListServices(c *fiber.Ctx) error {
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	services, err := h.accounts.ListServices(p.ID, false)
	if err != nil {
		return serverError(c, "could not read the services", err)
	}
	return c.JSON(fiber.Map{
		"services":             services,
		"secretsKeyConfigured": h.accounts.VaultOn(),
		"serviceTypes":         repository.ServiceTypes,
	})
}

func (h *AccountsHandler) serviceError(c *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, repository.ErrServiceNotFound):
		return refuse(c, fiber.StatusNotFound, "service-not-found", err.Error())
	case errors.Is(err, repository.ErrBadService), errors.Is(err, repository.ErrBadServiceURL),
		errors.Is(err, repository.ErrBadPlex):
		return refuse(c, fiber.StatusBadRequest, "bad-service", err.Error())
	case errors.Is(err, repository.ErrLoginMustBeRetyped):
		return refuse(c, fiber.StatusBadRequest, "login-required", err.Error())
	case errors.Is(err, repository.ErrVaultOff):
		return refuse(c, fiber.StatusServiceUnavailable, "secrets-key-missing", err.Error())
	case errors.Is(err, repository.ErrProfileNotFound):
		return refuse(c, fiber.StatusNotFound, "profile-not-found", "profile not found")
	default:
		return serverError(c, "could not save the service", err)
	}
}

// AddService sets a service up for a person.
func (h *AccountsHandler) AddService(c *fiber.Ctx) error {
	var in repository.ServiceInput
	if err := c.BodyParser(&in); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	s, err := h.accounts.AddService(p.ID, in, h.now())
	if err != nil {
		return h.serviceError(c, err)
	}
	log.Printf("account service %s (%s) added for %q%s", s.Type, s.BaseURL, p.Name, callerForLog(c))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"service": s})
}

// UpdateService edits a person's service; secrets left out keep their
// value.
func (h *AccountsHandler) UpdateService(c *fiber.Ctx) error {
	var in repository.ServiceInput
	if err := c.BodyParser(&in); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	s, err := h.accounts.UpdateService(p.ID, c.Params("sid"), in, h.now())
	if err != nil {
		return h.serviceError(c, err)
	}
	return c.JSON(fiber.Map{"service": s})
}

// DeleteService removes a person's service; their devices sign out of it.
func (h *AccountsHandler) DeleteService(c *fiber.Ctx) error {
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	if err := h.accounts.DeleteService(p.ID, c.Params("sid")); err != nil {
		return h.serviceError(c, err)
	}
	return c.JSON(fiber.Map{"deleted": true})
}

type orderBody struct {
	IDs []string `json:"ids"`
}

// OrderServices sets a person's priority order — first is kept first on
// the free tier.
func (h *AccountsHandler) OrderServices(c *fiber.Ctx) error {
	var body orderBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	p, err := h.profileFromPath(c)
	if p == nil {
		return err
	}
	if err := h.accounts.ReorderServices(p.ID, body.IDs, h.now()); err != nil {
		if errors.Is(err, repository.ErrServiceNotFound) {
			return refuse(c, fiber.StatusBadRequest, "bad-order",
				fmt.Sprintf("name each of %s's services exactly once", p.Name))
		}
		return serverError(c, "could not reorder", err)
	}
	services, err := h.accounts.ListServices(p.ID, false)
	if err != nil {
		return serverError(c, "could not read the services", err)
	}
	return c.JSON(fiber.Map{"services": services})
}
