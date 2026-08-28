package middleware

import (
	"crypto/subtle"
	"strings"

	"github.com/gofiber/fiber/v2"
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

// RequireAdmin gates routes that manage the server: catalog sync,
// creating and deleting profiles, backups.
func RequireAdmin(cfg *config.Config) fiber.Handler {
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
