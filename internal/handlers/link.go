package handlers

import (
	"log"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// LinkHandler implements device linking in the Plex style.
//
// The app asks for a code and shows it to the person holding the phone.
// They read it to the admin, who approves it. The direction is the point:
// the code travels TOWARD the admin, so nothing sensitive is ever
// transmitted between people. The code alone cannot be approved (that
// needs the admin token) and cannot be claimed (that needs the claim
// secret, which only the requesting app holds).
type LinkHandler struct {
	links    *repository.LinkRepository
	profiles *repository.ProfileRepository
	// stepUp checks the fresh code a phone's admin sign-in must add to
	// every approval (see ApproveLink).
	stepUp StepUp
}

// NewLinkHandler takes the admin login's fresh-code check
// (AdminAuthHandler.FreshCode) for approvals from a phone.
func NewLinkHandler(
	links *repository.LinkRepository,
	profiles *repository.ProfileRepository,
	stepUp StepUp,
) *LinkHandler {
	return &LinkHandler{links: links, profiles: profiles, stepUp: stepUp}
}

type LinkRequestBody struct {
	DeviceLabel string `json:"deviceLabel"`
}

type LinkRequestResponse struct {
	Code        string `json:"code"`
	ClaimSecret string `json:"claimSecret"`
	ExpiresAt   string `json:"expiresAt"`
	PollAfterMs int    `json:"pollAfterMs"`
	Note        string `json:"note"`
}

// RequestLink starts a link. Unauthenticated by necessity — the device
// has no token yet, which is the entire problem being solved.
//
// It hands back nothing useful to a stranger: a code that does nothing
// without the admin, and a claim secret that does nothing until the
// admin approves that specific code.
func (h *LinkHandler) RequestLink(c *fiber.Ctx) error {
	var req LinkRequestBody
	// A body is optional; a device label is a courtesy to the admin.
	_ = c.BodyParser(&req)

	if len(req.DeviceLabel) > 80 {
		req.DeviceLabel = req.DeviceLabel[:80]
	}

	code, claimSecret, expiresAt, err := h.links.CreateRequest(req.DeviceLabel)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create link request",
		})
	}

	return c.JSON(LinkRequestResponse{
		Code:        code,
		ClaimSecret: claimSecret,
		ExpiresAt:   expiresAt.Format("2006-01-02T15:04:05Z07:00"),
		PollAfterMs: 2000,
		Note: "Show the code to whoever runs the server. Keep the claim " +
			"secret private — it is how this device collects its token.",
	})
}

type ClaimBody struct {
	ClaimSecret string `json:"claimSecret"`
	DeviceLabel string `json:"deviceLabel"`
}

// Claim is the app polling for its token.
//
// 202 means "not approved yet, keep waiting" — deliberately not an
// error, because waiting is the normal case.
func (h *LinkHandler) Claim(c *fiber.Ctx) error {
	var body ClaimBody
	if err := c.BodyParser(&body); err != nil || body.ClaimSecret == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "claimSecret is required",
		})
	}

	profileID, label, err := h.links.Claim(body.ClaimSecret)
	switch err {
	case nil:
	case repository.ErrLinkNotFound:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "unknown link request",
		})
	case repository.ErrLinkExpired:
		return c.Status(fiber.StatusGone).JSON(fiber.Map{
			"error": "this link request expired; start a new one",
		})
	case repository.ErrLinkUsed:
		return c.Status(fiber.StatusGone).JSON(fiber.Map{
			"error": "this link request was already used",
		})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to claim link",
		})
	}

	if profileID == "" {
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"status":      "pending",
			"pollAfterMs": 2000,
		})
	}

	profile, err := h.profiles.GetByID(profileID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to load profile",
		})
	}

	if strings.TrimSpace(body.DeviceLabel) != "" {
		label = body.DeviceLabel
		// The same 80 as every other way a label arrives: it is listed on
		// the admin page and carried in every party state.
		if len(label) > 80 {
			label = label[:80]
		}
	}

	// Mint a token for THIS device only. Any other device this person
	// has linked keeps working.
	token, err := h.profiles.MintToken(profileID, label)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to issue token",
		})
	}

	return c.JSON(fiber.Map{
		"status":      "linked",
		"token":       token,
		"profileId":   profile.ID,
		"profileName": profile.Name,
	})
}

type ApproveLinkBody struct {
	Code string `json:"code"`
	// Bind to an existing profile by id, or name a person to create or
	// reuse. Naming is the common path: linking IS the moment the admin
	// decides who this is.
	ProfileID string `json:"profileId"`
	Name      string `json:"name"`
	// A fresh second factor: a 6-digit code or a recovery code. Asked
	// of every approval a phone's admin sign-in makes, and of nothing
	// else (see ApproveLink). Not "code": that is the link code.
	TOTPCode     string `json:"totpCode"`
	RecoveryCode string `json:"recoveryCode"`
}

// ListPendingLinks shows the admin which devices are waiting.
func (h *LinkHandler) ListPendingLinks(c *fiber.Ctx) error {
	pending, err := h.links.ListPending()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to list pending links",
		})
	}
	return c.JSON(fiber.Map{"pending": pending, "count": len(pending)})
}

// ApproveLink binds a waiting code to a person. Admin only — this is
// what makes the spoken code harmless.
//
// Linking a device to someone hands that device their history and their
// party line: it reads their stats and joins what they are playing like
// any device of theirs. So a phone's admin sign-in alone cannot approve
// anything. Without a fresh code, a phone that is lost, or whose token
// is copied, could approve a code it requested itself into anyone's
// profile and claim it, and the backup's step-up would guard nothing.
// Someone NEW is no exception: a name is reused by every later approval
// (the web page approves by name), so a phone could create "Mike" before
// Mike is here, link its own device to him, and read his history from
// the day his devices are approved into that name. A phone sends a
// fresh code with every approval, as it does for the backup:
//
//   - none: 403 step-up-required, and nothing changes;
//   - a wrong, spent or raced one: 401 step-up-failed (the phone stays
//     signed in), the link is still waiting, and nobody is created.
//
// The code is spent through the same atomic claim as a sign-in, after
// every other check, so a stale link code or a missing person spends
// nothing. The browser's session asks for no code, as before: it was
// signed in with the password and a code, and lasts 12 hours. A phone's
// approvals share the sign-in rate limit (see buildApp), so its codes
// cannot be ground here.
func (h *LinkHandler) ApproveLink(c *fiber.Ctx) error {
	var body ApproveLinkBody
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if strings.TrimSpace(body.Code) == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "code is required",
		})
	}

	// Validate the code BEFORE resolving a profile. Creating the person
	// first means a stale or mistyped code leaves an orphan profile
	// behind on every failed attempt.
	if err := h.links.CheckPending(body.Code); err != nil {
		return linkError(c, err)
	}

	// Resolve who this is: someone already here (by id, or by a name
	// that matches), or a name to create.
	var existing *repository.Profile
	name := ""
	if body.ProfileID != "" {
		// An id picked from a list can be gone by the time it is used —
		// the person removed on the web page meanwhile. Say so, rather
		// than letting the foreign key fail the approval as a 500.
		profile, err := h.profiles.GetByID(body.ProfileID)
		if err == repository.ErrProfileNotFound {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error":  "profile not found",
				"reason": "profile-not-found",
			})
		} else if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to resolve profile",
			})
		}
		existing = profile
	} else {
		name = strings.TrimSpace(body.Name)
		if name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "provide either profileId or name",
			})
		}
		// Reuse the person if they already exist — linking a second
		// device must not create "Sarah (2)".
		profile, err := h.profiles.GetByName(name)
		switch err {
		case nil:
			existing = profile
		case repository.ErrProfileNotFound:
			// Someone new: created below, after every refusal above.
		default:
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to resolve profile",
			})
		}
	}

	// Every approval from a phone: a fresh code.
	byPhone := middleware.AdminDevice(c) != ""
	if byPhone {
		// Blank is no code. Trimmed, a blank recovery code sent beside a
		// 6-digit code cannot take its place: a non-empty recovery code
		// wins, as at sign-in.
		totp := strings.TrimSpace(body.TOTPCode)
		recovery := strings.TrimSpace(body.RecoveryCode)
		if totp == "" && recovery == "" {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":  "approving a device from a phone needs a fresh code",
				"reason": "step-up-required",
			})
		}
		if h.stepUp == nil {
			// Wiring error: refuse rather than approve unchecked.
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "could not check the code",
			})
		}
		ok, err := h.stepUp(c, totp, recovery, "link approval")
		if !ok {
			return err
		}
	}

	profileID := ""
	if existing != nil {
		profileID = existing.ID
	} else {
		profile, err := h.profiles.Create(name)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to resolve profile",
			})
		}
		profileID = profile.ID
	}

	if err := h.links.Approve(body.Code, profileID); err != nil {
		return linkError(c, err)
	}

	if byPhone {
		// The audit line for the admin act that reaches a person's
		// history: who, and which phone did it.
		who, whether := name, "someone new"
		if existing != nil {
			who, whether = existing.Name, "already here"
		}
		log.Printf("link approved for %q (%s, %s)%s",
			who, profileID, whether, callerForLog(c))
	}
	return c.JSON(fiber.Map{"approved": true, "profileId": profileID})
}

// linkError maps link failures to responses, so the pre-check and the
// approval itself always answer the same way.
func linkError(c *fiber.Ctx, err error) error {
	switch err {
	case repository.ErrLinkNotFound:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "no pending link with that code",
		})
	case repository.ErrLinkExpired:
		return c.Status(fiber.StatusGone).JSON(fiber.Map{
			"error": "that code expired; ask them to start again",
		})
	case repository.ErrLinkUsed:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "that code was already approved",
		})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to approve link",
		})
	}
}

type ProvisionDeviceBody struct {
	Label string `json:"label"`
}

// Self tells the calling device who it is: GET /api/v1/devices/me,
// advertised as "devices.me".
//
//	{"deviceKey": "<token id>", "label": "…", "profileId": "…", "profileName": "…"}
//
// deviceKey is the token id: the id the admin's person list shows for
// this device, the id DELETE /admin/devices/:tokenId revokes, and the
// party line's yourDeviceKey. The party line names a device only while
// something is playing; this answers whenever the token is good, so a
// phone that manages the server can always tell which listed device is
// itself — to mark it, and to warn before revoking its own link. label
// is "" for a device linked without one.
//
// Behind requireProfile: a link token only. No admin credential is a
// device, so none gets an answer.
func (h *LinkHandler) Self(c *fiber.Ctx) error {
	deviceID, label := middleware.Device(c)
	profileID := middleware.ProfileID(c)
	if deviceID == "" || profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a device token is required",
		})
	}
	return c.JSON(fiber.Map{
		"deviceKey":   deviceID,
		"label":       label,
		"profileId":   profileID,
		"profileName": middleware.ProfileName(c),
	})
}

// RenameSelfBody is what a device sends to change its own party name.
type RenameSelfBody struct {
	DeviceLabel string `json:"deviceLabel"`
}

// RenameSelf lets the calling device change the label it was linked
// with — the name the party shows for it ("Living Room" instead of a
// second "Apple TV"). Until now the name was fixed at link time, and a
// TV's Device name field changed only what the TV called itself, so
// two rooms could never be told apart without relinking. The token id
// stays the identity (the seat, the presence entry); only the label
// moves. PATCH /api/v1/devices/me, advertised as "devices.rename".
func (h *LinkHandler) RenameSelf(c *fiber.Ctx) error {
	deviceID, _ := middleware.Device(c)
	if deviceID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a device token is required",
		})
	}

	var body RenameSelfBody
	_ = c.BodyParser(&body)
	label := strings.TrimSpace(body.DeviceLabel)
	if label == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "deviceLabel is required",
		})
	}
	if len(label) > 80 {
		label = label[:80]
	}

	if err := h.profiles.RenameToken(deviceID, label); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to rename device",
		})
	}
	return c.JSON(fiber.Map{"deviceLabel": label})
}

// ProvisionDevice mints an ADDITIONAL token for the caller's own
// profile, so a phone can hand one to a watch over WatchConnectivity
// without anybody typing a code on a 40mm screen.
//
// Authenticated by an existing device token, so no admin approval is
// needed: the caller has already proved it is that person. It is not an
// escalation either — the caller could already do everything the new
// token can.
//
// The honest tradeoff: a stolen token can mint siblings, so revoking one
// device does not necessarily end the intruder's access. The admin sees
// every device in the profile listing and can revoke each, or delete the
// profile. For a household that is the right balance; if it ever is not,
// gate this route behind admin approval.
func (h *LinkHandler) ProvisionDevice(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a device token is required",
		})
	}

	var body ProvisionDeviceBody
	_ = c.BodyParser(&body)

	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "paired device"
	}
	if len(label) > 80 {
		label = label[:80]
	}

	token, err := h.profiles.MintToken(profileID, label)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to issue token",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"token": token,
		"label": label,
		"note": "Hand this to the paired device. It shares this " +
			"profile's stats and can be revoked on its own.",
	})
}
