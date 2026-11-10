package handlers

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
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

// SignOutAllDevices signs out every device of one person — each link
// token, the account keys sealed to them, and their phones' Manage-server
// sign-ins (owner, 2026-10-30). A device signed in with an Alexandria
// Account reads `token-revoked` on its next request and signs itself out
// of the account and every login the account gave it.
func (h *AdminHandler) SignOutAllDevices(c *fiber.Ctx) error {
	n, err := h.profiles.RevokeAllTokens(c.Params("id"))
	if err == repository.ErrProfileNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "profile not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to sign out the devices",
		})
	}
	return c.JSON(fiber.Map{"revoked": n})
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
// the admin token accordingly, and keep /admin/backup (GET for the
// browser, POST for a phone) the only route that does this.
//
// A phone signed in with an admin device token is refused here (403
// step-up-required): it takes BackupForDevice instead, which asks for a
// fresh code and leaves the sign-in secrets out. The browser's copy is
// unchanged — the whole database — because restoring it must bring the
// admin login back with it.
func (h *AdminHandler) Backup(c *fiber.Ctx) error {
	if middleware.AdminDevice(c) != "" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":  "backing up from a phone needs a fresh code",
			"reason": "step-up-required",
		})
	}
	data, fail := h.snapshot(false)
	if fail != nil {
		return serverError(c, fail.msg, fail.err)
	}
	return sendBackup(c, data)
}

// BackupForDevice is the phone's backup: POST /admin/backup, behind
// RequireAdmin, the sign-in rate limiter and RequireFreshCode.
//
// The copy leaves the admin login out — the password hash, the TOTP
// seed (stored in plaintext), recovery-code hashes, browser sessions and
// every phone's sign-in — so a file that travels through a share sheet
// carries no way back in. Restoring it means setting the admin login up
// again with the environment token. Everyone's history is still in it;
// that is what a backup is for.
func (h *AdminHandler) BackupForDevice(c *fiber.Ctx) error {
	data, fail := h.snapshot(true)
	if fail != nil {
		return serverError(c, fail.msg, fail.err)
	}
	log.Printf("admin backup (sign-in secrets removed) sent%s", callerForLog(c))
	return sendBackup(c, data)
}

func sendBackup(c *fiber.Ctx, data []byte) error {
	name := fmt.Sprintf("alexandria-%s.db",
		time.Now().UTC().Format("2006-01-02-150405"))
	c.Set("Content-Type", "application/octet-stream")
	c.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	return c.Send(data)
}

// backupFailure is a snapshot step that failed: msg answers the caller,
// err goes to the log.
type backupFailure struct {
	msg string
	err error
}

// snapshot copies the database with VACUUM INTO, optionally scrubs the
// copy, and returns its bytes. The temporary copy never outlives the
// call.
func (h *AdminHandler) snapshot(scrub bool) ([]byte, *backupFailure) {
	tmpDir, err := os.MkdirTemp("", "alexandria-backup")
	if err != nil {
		return nil, &backupFailure{"failed to prepare backup", err}
	}
	defer os.RemoveAll(tmpDir)

	path := filepath.Join(tmpDir, "alexandria-backup.db")
	if _, err := database.DB.Exec(`VACUUM INTO ?`, path); err != nil {
		return nil, &backupFailure{"failed to create backup snapshot", err}
	}
	if scrub {
		if err := scrubSignInSecrets(path); err != nil {
			return nil, &backupFailure{
				"failed to remove sign-in secrets from the backup", err}
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &backupFailure{"failed to read backup snapshot", err}
	}
	return data, nil
}

// signInTables hold the admin login. They are emptied in a phone's
// backup copy. So do Server Admin's sign-ins to the household's servers:
// a copy that leaves this machine carries no admin password of any server,
// sealed or not (they are typed again after a restore from a phone's copy).
var signInTables = []string{
	"admin_credentials",
	"admin_recovery_codes",
	"admin_sessions",
	"admin_device_tokens",
	"service_admin_logins",
}

// scrubSignInSecrets empties the sign-in tables of a snapshot file so
// that nothing of them survives anywhere in its bytes: deleted with
// secure_delete (freed content zeroed), then VACUUM rebuilds the file so
// no free page is left at all. A rollback journal, not WAL, so the file
// is complete on its own once the connection closes.
func scrubSignInSecrets(path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	// PRAGMAs belong to a connection: keep every statement on one.
	db.SetMaxOpenConns(1)

	stmts := []string{
		`PRAGMA journal_mode = DELETE`,
		`PRAGMA secure_delete = ON`,
	}
	for _, t := range signInTables {
		stmts = append(stmts, `DELETE FROM `+t)
	}
	// And no invite still waiting: each is a way into someone's profile,
	// and a copy on a phone has no use for one (security review,
	// 2026-11-10). Used invites are history and stay.
	stmts = append(stmts, `DELETE FROM account_invites WHERE used_at IS NULL`)
	stmts = append(stmts, `VACUUM`)
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return db.Close()
}
