package handlers

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// AdminDevicesHandler lists and ends phones' admin sign-ins: the web
// page's "Signed-in phones", and a phone's own sign-out. Every route is
// behind RequireAdmin and none is rate limited (the page polls).
type AdminDevicesHandler struct {
	auth *repository.AdminAuthRepository
}

func NewAdminDevicesHandler(auth *repository.AdminAuthRepository) *AdminDevicesHandler {
	return &AdminDevicesHandler{auth: auth}
}

// List is GET /admin/admin-devices: every live sign-in, most recently
// used first. current marks the one that made this request — always
// false for the browser.
func (h *AdminDevicesHandler) List(c *fiber.Ctx) error {
	devices, err := h.auth.ListDeviceTokens(clock())
	if err != nil {
		return serverError(c, "failed to list admin devices", err)
	}
	self := middleware.AdminDevice(c)
	out := make([]adminDeviceJSON, 0, len(devices))
	for i := range devices {
		v := adminDeviceView(&devices[i])
		current := self != "" && devices[i].ID == self
		v.Current = &current
		out = append(out, v)
	}
	return c.JSON(fiber.Map{"devices": out, "count": len(out)})
}

// Revoke is DELETE /admin/admin-devices/:id. It ends that sign-in at
// once; a phone signing out sends its own id.
func (h *AdminDevicesHandler) Revoke(c *fiber.Ctx) error {
	id := c.Params("id")
	dev, err := h.auth.RevokeDeviceToken(id)
	if errors.Is(err, repository.ErrAdminDeviceNotFound) {
		return c.Status(fiber.StatusNotFound).
			JSON(fiber.Map{"error": "admin device not found"})
	}
	if err != nil {
		return serverError(c, "failed to revoke admin device", err)
	}
	if id == middleware.AdminDevice(c) {
		log.Printf("admin device signed out: %q (%s)", dev.Label, dev.ID)
	} else {
		log.Printf("admin device revoked: %q (%s)", dev.Label, dev.ID)
	}
	return c.JSON(fiber.Map{"revoked": true})
}

// RevokeAll is DELETE /admin/admin-devices: "Sign out all phones".
func (h *AdminDevicesHandler) RevokeAll(c *fiber.Ctx) error {
	// Strict routing is off, so Fiber hands this route
	// /admin-devices/ too — which is Revoke with its id missing. A
	// client that lost a phone's id must get "not found", not sign out
	// every phone in the house.
	if strings.HasSuffix(c.Path(), "/") {
		return c.Status(fiber.StatusNotFound).
			JSON(fiber.Map{"error": "admin device not found"})
	}
	gone, err := h.auth.RevokeAllDeviceTokens()
	if err != nil {
		return serverError(c, "failed to revoke admin devices", err)
	}
	for _, d := range gone {
		log.Printf("admin device revoked: %q (%s)", d.Label, d.ID)
	}
	log.Printf("every admin device signed out (%d)", len(gone))
	return c.JSON(fiber.Map{"revoked": len(gone)})
}

// adminDeviceJSON is the ADMIN_DEVICE object of the shared contract
// (plan §1.3, plus amendment A1's schemeBasis). It has no field that
// could carry a token or a hash, and the repository type it is built
// from has none either.
type adminDeviceJSON struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt"`
	// ExpiresAt slides with use for a persistent sign-in (180 days
	// after the last use); it is fixed for a 12-hour session.
	ExpiresAt  string `json:"expiresAt"`
	Persistent bool   `json:"persistent"`
	CreatedVia string `json:"createdVia"`
	// SchemeBasis: tls, proxy, forwarded, cloudflare, config, client,
	// or "" — see schemebasis.go.
	SchemeBasis string `json:"schemeBasis"`
	// Current is set only in the list (R3): true for the sign-in that
	// authenticated the request, false for every row when the caller is
	// the browser.
	Current *bool `json:"current,omitempty"`
	// ProfileID and Access are set for a sign-in made with a person's own
	// account: whose, and the admin access it carries
	// (repository.AdminAccess…). Both absent for the admin login's.
	ProfileID string `json:"profileId,omitempty"`
	Access    string `json:"access,omitempty"`
}

// rfc3339 is the contract's time format for new fields: UTC, to the
// whole second ("2026-10-02T18:40:00Z").
func rfc3339(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func adminDeviceView(d *repository.AdminDevice) adminDeviceJSON {
	return adminDeviceJSON{
		ID:          d.ID,
		Label:       d.Label,
		CreatedAt:   rfc3339(d.CreatedAt),
		LastUsedAt:  rfc3339(d.LastUsedAt),
		ExpiresAt:   rfc3339(d.ExpiresAt),
		Persistent:  d.Persistent,
		CreatedVia:  d.CreatedVia,
		SchemeBasis: d.SchemeBasis,
		ProfileID:   d.ProfileID,
		Access:      d.Scope,
	}
}
