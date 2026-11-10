package middleware

import (
	"crypto/subtle"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// This server has no accounts. It has an ADMIN token, set in the
// environment by whoever runs the box, and one PROFILE token per person,
// which exists only to keep their stats in their own bucket.
//
// Neither token is ever an entitlement. Nothing here decides what a
// person may buy or unlock — that is settled on the device against
// RevenueCat. See docs/COMPANION_DESIGN.md.

const (
	profileLocalsKey     = "profileID"
	profileNameLocalsKey = "profileName"
)

func bearerToken(c *fiber.Ctx) (string, bool) {
	authHeader := c.Get("Authorization")
	if authHeader == "" {
		return "", false
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return "", false
	}
	return parts[1], true
}

func isAdminToken(cfg *config.Config, token string) bool {
	// Constant time, so a wrong token cannot be discovered one byte at a
	// time by measuring how fast we say no.
	return subtle.ConstantTimeCompare([]byte(token), []byte(cfg.APIToken)) == 1
}

func unauthorized(c *fiber.Ctx, msg string) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": msg})
}

// AdminSessionCookie is the browser's half of the admin login.
const AdminSessionCookie = "alx_admin"

// RequireAdmin gates routes that manage the server: creating and
// deleting profiles, revoking devices, backups.
//
// Which ways in are open depends on whether a login has been enrolled:
//
//   - BEFORE enrolment, the environment token works. It has to: it is
//     the only credential that exists, and it is what authorises
//     enrolment itself.
//   - AFTER enrolment, the environment token STOPS WORKING HERE. Only
//     credentials earned with the password + TOTP login are accepted:
//     the browser's session cookie, or a phone's admin device token
//     ("Bearer alxa_…", see requireAdminDevice).
//
// That second rule is the entire point. Leaving the static token valid
// alongside the login would make the login decoration: an attacker with
// the token would simply not visit the page, and GET /admin/backup --
// which returns the whole database -- would still be one header away on
// an internet-reachable companion. A second factor you can skip is not a
// second factor.
//
// The token is not dead, it is demoted: it still authorises enrolment
// and re-enrolment (see handlers.AdminAuthHandler), which is the
// break-glass path for a lost authenticator. Whoever holds the .env can
// always get back in, but they must set up a login to do it, and doing
// so kills every existing session.
func RequireAdmin(
	cfg *config.Config,
	auth *repository.AdminAuthRepository,
) fiber.Handler {
	publicHost := publicHostOf(cfg.PublicURL)
	return func(c *fiber.Ctx) error {
		enrolled, err := auth.Enrolled()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).
				JSON(fiber.Map{"error": "could not read admin credentials"})
		}

		if enrolled {
			if raw := c.Cookies(AdminSessionCookie); raw != "" {
				ok, err := auth.SessionValid(adminauth.HashToken(raw))
				if err == nil && ok {
					// The cookie counts only for the admin page's own
					// requests (sameOriginGuard).
					if !sameOriginGuard(c, publicHost) {
						return refuseCrossOrigin(c)
					}
					return c.Next()
				}
			}
			if tok, ok := bearerToken(c); ok &&
				strings.HasPrefix(tok, adminauth.DeviceTokenPrefix) {
				return requireAdminDevice(c, auth, tok)
			}
			return unauthorized(c, "admin session required — sign in at /admin")
		}

		token, ok := bearerToken(c)
		if !ok {
			return unauthorized(c, "missing or malformed authorization header")
		}
		if !isAdminToken(cfg, token) {
			return unauthorized(c, "admin token required")
		}
		return c.Next()
	}
}

const (
	adminDeviceLocalsKey      = "adminDeviceID"
	adminDeviceLabelLocalsKey = "adminDeviceLabel"
	adminProfileLocalsKey     = "adminDeviceProfile"
	adminScopeLocalsKey       = "adminDeviceScope"
)

// requireAdminDevice admits a phone's admin sign-in, an admin device
// token issued by POST /admin/auth/device-token for a fresh password and
// code. It is accepted HERE ONLY: RequireProfile and RequireAnyToken
// never look at it, so a phone that manages the server still cannot read
// anyone's stats or the party line, nor use the shared catalog routes
// (it reads the index through the /admin/catalog mirrors), and
// RequireAdminToken never takes it, so it cannot re-enrol. The two admin
// acts that DO lead into people's history — the backup, and approving a
// link (every approval, someone new included) — each ask it for a fresh
// code as well (handlers.RequireFreshCode, handlers.LinkHandler.ApproveLink).
//
// The lookup is by the token's SHA-256, never by the token itself, so how
// long the database takes to answer says nothing about the token's bytes
// — the same reason sessions and profile tokens are found by hash.
//
// The two refusals carry a reason, which is how the phone tells "you
// were signed out" (revoked on the web page, or the login re-enrolled)
// from "your sign-in lapsed". A lapsed row is deleted by the lookup.
func requireAdminDevice(
	c *fiber.Ctx,
	auth *repository.AdminAuthRepository,
	token string,
) error {
	now := time.Now()
	dev, state, err := auth.LookupDeviceToken(adminauth.HashToken(token), now)
	switch {
	case err != nil:
		return c.Status(fiber.StatusInternalServerError).
			JSON(fiber.Map{"error": "could not read admin credentials"})
	case state == repository.DeviceTokenValid:
		auth.TouchDeviceToken(dev, now)
		c.Locals(adminDeviceLocalsKey, dev.ID)
		c.Locals(adminDeviceLabelLocalsKey, dev.Label)
		c.Locals(adminProfileLocalsKey, dev.ProfileID)
		c.Locals(adminScopeLocalsKey, dev.Scope)
		return c.Next()
	case state == repository.DeviceTokenExpired:
		log.Printf("admin device sign-in expired: %q (%s)", dev.Label, dev.ID)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "this phone's sign-in expired",
			"reason": "device-token-expired",
		})
	default:
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error":  "this phone is no longer signed in",
			"reason": "device-token-invalid",
		})
	}
}

// AdminDevice returns the id of the admin device token that
// authenticated this request, or "" when it was the browser's session
// cookie or the environment token.
func AdminDevice(c *fiber.Ctx) string {
	id, _ := c.Locals(adminDeviceLocalsKey).(string)
	return id
}

// AdminDeviceLabel is that phone's label, for log lines; "" otherwise.
func AdminDeviceLabel(c *fiber.Ctx) string {
	label, _ := c.Locals(adminDeviceLabelLocalsKey).(string)
	return label
}

// AdminProfile is the person whose account signed this phone in to
// Manage server, "" for the admin login (its phones, the browser, the
// environment token).
func AdminProfile(c *fiber.Ctx) string {
	id, _ := c.Locals(adminProfileLocalsKey).(string)
	return id
}

// AdminScope is the access that person's sign-in carries
// (repository.AdminAccess…), "" for the admin login.
func AdminScope(c *fiber.Ctx) string {
	scope, _ := c.Locals(adminScopeLocalsKey).(string)
	return scope
}

// AdminScopeGate keeps a phone signed in with a person's account
// (AdminProfile set) to the access it was given; the admin login's own
// sign-ins pass straight on. It goes after RequireAdmin on the /admin
// group, and decides by the path under /api/v1/admin:
//
//   - /service-admins…: the kept media-server sign-ins — "serveradmin"
//     and "full".
//   - /provision, /provision/… and …/provision: making or resetting a
//     person's account on a media server WITH those sign-ins (and listing
//     its libraries for that) — people and media servers both, so "full"
//     alone.
//   - DELETE /admin-devices/<its own id>: signing itself out — always.
//   - any other change to /admin-devices (another phone, or all of
//     them): the admin login's alone, so no account can sign the admin
//     out.
//   - …/admin-access: who has access is the admin login's call alone,
//     so no account can raise its own or anyone's.
//   - everything else (people, devices, invites, services, backups, the
//     signed-in phones list): "manage" and "full".
//
// The refusal is 403 access-denied: the phone stays signed in.
func AdminScopeGate() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if AdminProfile(c) == "" {
			return c.Next()
		}
		scope := AdminScope(c)
		path := adminSubPath(c.Path())
		reading := c.Method() == fiber.MethodGet || c.Method() == fiber.MethodHead
		allowed := false
		switch {
		case path == "/service-admins" || strings.HasPrefix(path, "/service-admins/"):
			allowed = scope == repository.AdminAccessServerAdmin || scope == repository.AdminAccessFull
		case path == "/provision" || strings.HasPrefix(path, "/provision/") || strings.HasSuffix(path, "/provision"):
			allowed = scope == repository.AdminAccessFull
		case c.Method() == fiber.MethodDelete && path == "/admin-devices/"+strings.ToLower(AdminDevice(c)):
			allowed = true
		case (path == "/admin-devices" || strings.HasPrefix(path, "/admin-devices/")) && !reading:
			allowed = false
		case strings.HasSuffix(path, "/admin-access"):
			allowed = false
		default:
			allowed = scope == repository.AdminAccessManage || scope == repository.AdminAccessFull
		}
		if !allowed {
			log.Printf("admin access refused: %s %s for %q (%s, access %q)",
				c.Method(), c.Path(), AdminDeviceLabel(c), AdminDevice(c), scope)
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":  "this sign-in's admin access does not cover that",
				"reason": "access-denied",
			})
		}
		return c.Next()
	}
}

// adminSubPath is the path under /api/v1/admin in the form the router
// matched it: Fiber matches routes ignoring case and any trailing slashes,
// so the gate does too, or "/Admin-Access" and "/admin-access//" reach the
// handler past a check that only knows "/admin-access" (security review,
// 2026-11-02: a "manage" sign-in gave itself "full" that way). Repeated
// slashes are folded as well, so no spelling the router accepts is taken
// for another path.
func adminSubPath(raw string) string {
	p := strings.ToLower(raw)
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	p = strings.TrimPrefix(p, "/api/v1/admin")
	return strings.TrimRight(p, "/")
}

// ForAdminDevices runs h only for a request a phone's admin sign-in
// authenticated; the browser's session cookie and the environment token
// pass straight on. It goes after RequireAdmin, which is what marks the
// phone. It is how a route that can ask a phone for a fresh code puts
// the sign-in rate limiter in front of phones alone.
func ForAdminDevices(h fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if AdminDevice(c) == "" {
			return c.Next()
		}
		return h(c)
	}
}

// RequireAdminToken gates the enrolment routes on the environment token
// alone. It is the only thing that still trusts that token outright, and
// it exists so someone holding the .env can always recover the box.
func RequireAdminToken(cfg *config.Config) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token, ok := bearerToken(c)
		if !ok {
			return unauthorized(c, "missing or malformed authorization header")
		}
		if !isAdminToken(cfg, token) {
			return unauthorized(c, "admin token required")
		}
		return c.Next()
	}
}

// RequireProfile gates a person's own data.
//
// The ADMIN TOKEN IS REJECTED HERE ON PURPOSE. The admin manages
// profiles and storage; the admin does not get an API path into
// somebody's listening history. That refusal is the privacy boundary in
// code — see the note in docs/COMPANION_DESIGN.md about what it does and
// does not protect against.
func RequireProfile(profiles *repository.ProfileRepository) fiber.Handler {
	return func(c *fiber.Ctx) error {
		token, ok := bearerToken(c)
		if !ok {
			return unauthorized(c, "missing or malformed authorization header")
		}

		caller, err := profiles.FindByToken(token)
		if errors.Is(err, repository.ErrProfileNotFound) && repository.LooksLikeDeviceToken(token) {
			// A device token that is not (or no longer) one of this
			// server's: signed out from the devices page, its person
			// removed, or never issued. The code is what a signed-in app
			// acts on — it signs itself out of its Alexandria Account and
			// every login the account gave it — so it is sent ONLY here,
			// never for an admin credential sent to the wrong route.
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "a profile token is required for this route",
				"code":  "token-revoked",
			})
		}
		if errors.Is(err, repository.ErrProfileNotFound) {
			return unauthorized(c, "a profile token is required for this route")
		}
		if err != nil {
			// The database could not say. Never a 401: a device must not
			// read "signed out" into a hiccup here.
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "could not check the device token",
			})
		}

		profiles.TouchLastSeen(caller.TokenID)
		c.Locals(profileLocalsKey, caller.ProfileID)
		c.Locals(profileNameLocalsKey, caller.ProfileName)
		c.Locals(deviceIDLocalsKey, caller.TokenID)
		c.Locals(deviceLabelLocalsKey, caller.TokenLabel)
		return c.Next()
	}
}

const (
	deviceIDLocalsKey    = "deviceTokenID"
	deviceLabelLocalsKey = "deviceLabel"
)

// Device returns the calling device — the token id and the label it was
// linked with. Empty for an admin caller. The party line keys the
// leader seat on this, so a device's identity is whatever it proved by
// authenticating, never something it claimed in a body.
func Device(c *fiber.Ctx) (id, label string) {
	id, _ = c.Locals(deviceIDLocalsKey).(string)
	label, _ = c.Locals(deviceLabelLocalsKey).(string)
	return id, label
}

// RequireAnyToken gates shared, non-personal data — the library index.
// It describes the household's servers, not any individual, so both
// token kinds may read it.
//
// A signed-in admin counts too, and must: the admin page reads
// /catalog/summary on every poll, and once the login replaced the token
// box that page has no bearer token left to send. Without this branch
// the page authenticates fine, draws itself, and then throws its own
// user back to the login screen on the one call that still wanted a
// header — which is exactly what it did.
//
// Safe to accept here because the session cookie is SameSite=Strict, so
// it is not sent on cross-site requests at all, and because the cookie
// counts only for the admin page's own requests (sameOriginGuard): a
// page on another port of the same machine cannot borrow it to POST a
// sync either.
func RequireAnyToken(
	cfg *config.Config,
	profiles *repository.ProfileRepository,
	auth *repository.AdminAuthRepository,
) fiber.Handler {
	publicHost := publicHostOf(cfg.PublicURL)
	return func(c *fiber.Ctx) error {
		if raw := c.Cookies(AdminSessionCookie); raw != "" {
			ok, err := auth.SessionValid(adminauth.HashToken(raw))
			if err == nil && ok {
				if !sameOriginGuard(c, publicHost) {
					return refuseCrossOrigin(c)
				}
				return c.Next()
			}
		}

		token, ok := bearerToken(c)
		if !ok {
			return unauthorized(c, "missing or malformed authorization header")
		}
		if isAdminToken(cfg, token) {
			return c.Next()
		}
		caller, err := profiles.FindByToken(token)
		if err != nil {
			return unauthorized(c, "invalid token")
		}
		profiles.TouchLastSeen(caller.TokenID)
		c.Locals(profileLocalsKey, caller.ProfileID)
		return c.Next()
	}
}

// ProfileID returns the calling profile, or "" for an admin caller.
func ProfileID(c *fiber.Ctx) string {
	if v, ok := c.Locals(profileLocalsKey).(string); ok {
		return v
	}
	return ""
}

// ProfileName returns the calling profile's name, as RequireProfile
// found it; "" anywhere else.
func ProfileName(c *fiber.Ctx) string {
	name, _ := c.Locals(profileNameLocalsKey).(string)
	return name
}
