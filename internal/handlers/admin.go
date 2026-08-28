package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// AdminHandler is the household manager's view.
//
// It can create people, remove them, rotate their tokens, and back up
// the database. It CANNOT read anyone's history: there is no route here
// that returns an event, a title, or an author, and the stats repository
// deliberately offers no method that would let one be written.
//
// The honest limit: whoever runs this server owns the file the data
// sits in and can open it directly. What is enforced here is that the
// API never becomes the tool for it. See docs/COMPANION_DESIGN.md.
type AdminHandler struct {
	profiles *repository.ProfileRepository
	dbPath   string
}

func NewAdminHandler(
	profiles *repository.ProfileRepository,
	dbPath string,
) *AdminHandler {
	return &AdminHandler{profiles: profiles, dbPath: dbPath}
}

type CreateProfileRequest struct {
	Name string `json:"name"`
}

// CreateProfile makes a person with no devices attached.
//
// The normal way someone joins is device linking (see handlers/link.go),
// which creates the profile as a side effect of approving a code. This
// route exists for setting people up ahead of time.
func (h *AdminHandler) CreateProfile(c *fiber.Ctx) error {
	var req CreateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	profile, err := h.profiles.Create(req.Name)
	switch err {
	case nil:
	case repository.ErrProfileExists:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "a profile with that name already exists",
		})
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"profile": profile,
		"note": "No devices are linked yet. Have them open the app and " +
			"read you their link code.",
	})
}

// RevokeDevice unlinks ONE device — a lost phone — leaving that
// person's other devices working.
func (h *AdminHandler) RevokeDevice(c *fiber.Ctx) error {
	err := h.profiles.RevokeToken(c.Params("tokenId"))
	if err == repository.ErrTokenNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "device not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to revoke device",
		})
	}
	return c.JSON(fiber.Map{"revoked": true})
}

// ListProfiles shows who exists and how much space they use.
//
// Counts and timestamps only. No titles, no authors, no events.
func (h *AdminHandler) ListProfiles(c *fiber.Ctx) error {
	profiles, err := h.profiles.List()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list profiles",
		})
	}
	return c.JSON(fiber.Map{
		"profiles": profiles,
		"count":    len(profiles),
	})
}

// DeleteProfile removes a person and everything they own.
//
// This is the one destructive route on the server, and it is scoped to
// the server's own database. It deletes a profile's index of what they
// listened to — never media, on any media server.
func (h *AdminHandler) DeleteProfile(c *fiber.Ctx) error {
	err := h.profiles.Delete(c.Params("id"))
	if err == repository.ErrProfileNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "profile not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete profile",
		})
	}
	return c.JSON(fiber.Map{"deleted": true})
}

// Backup streams a consistent snapshot of the database.
//
// VACUUM INTO takes the snapshot without stopping the server or copying
// a file mid-write.
//
// BE HONEST ABOUT WHAT THIS IS: the snapshot contains every stats row —
// titles, authors, timestamps — so this is the one admin route through
// which history CAN leave the server, and the "admin cannot read
// anyone's history" boundary explicitly does not cover it. It exists
// because backups are the point of the data being here at all, and it
// hands the operator the same bytes sitting on their own disk. What it
// changes is reach: a LEAKED admin token turns this into remote
// exfiltration, where every other admin route yields only counts. Treat
// the admin token accordingly, and keep this the only such route.
func (h *AdminHandler) Backup(c *fiber.Ctx) error {
	tmpDir, err := os.MkdirTemp("", "alexandria-backup")
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to prepare backup",
		})
	}
	defer os.RemoveAll(tmpDir)

	snapshot := filepath.Join(tmpDir, "alexandria-backup.db")
	if _, err := database.DB.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create backup snapshot",
		})
	}

	data, err := os.ReadFile(snapshot)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read backup snapshot",
		})
	}

	name := fmt.Sprintf("alexandria-%s.db",
		time.Now().UTC().Format("2006-01-02-150405"))
	c.Set("Content-Type", "application/octet-stream")
	c.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	return c.Send(data)
}
