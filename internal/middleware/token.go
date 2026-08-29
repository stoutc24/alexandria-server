package middleware

import (
	"crypto/subtle"
	"strings"

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

const profileLocalsKey = "profileID"

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
// Two ways in, and which ones are open depends on whether a login has
// been enrolled:
//
//   - BEFORE enrolment, the environment token works. It has to: it is
//     the only credential that exists, and it is what authorises
//     enrolment itself.
//   - AFTER enrolment, the environment token STOPS WORKING HERE. Only a
//     session cookie from a password + TOTP login is accepted.
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
					return c.Next()
				}
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
		if err != nil {
			return unauthorized(c, "a profile token is required for this route")
		}

		profiles.TouchLastSeen(caller.TokenID)
		c.Locals(profileLocalsKey, caller.ProfileID)
		c.Locals("profileName", caller.ProfileName)
		return c.Next()
	}
}

// RequireAnyToken gates shared, non-personal data — the library index.
// It describes the household's servers, not any individual, so both
// token kinds may read it.
func RequireAnyToken(
	cfg *config.Config,
	profiles *repository.ProfileRepository,
) fiber.Handler {
	return func(c *fiber.Ctx) error {
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
