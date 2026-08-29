package handlers

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

// NowPlayingHandler is the party line: a profile's devices report and
// read what that profile is playing right now, so a TV can join a book
// or an album mid-stream.
//
// Every route sits behind RequireProfile, which REJECTS the admin
// token — live listening is the most intimate data this server touches,
// and the household manager has no route into it, exactly as with
// stats. The profile comes from the token, never the request, so one
// person's devices can never read or write another's session.
type NowPlayingHandler struct {
	store *nowplaying.Store
}

func NewNowPlayingHandler(store *nowplaying.Store) *NowPlayingHandler {
	return &NowPlayingHandler{store: store}
}

// Report is a device publishing its current playback. Heartbeat this
// every few seconds while playing; the state goes stale (and stops
// being advertised) 90 seconds after the last report.
func (h *NowPlayingHandler) Report(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}

	// The queue rides the same PUT, optionally: present = replace,
	// absent = keep what is stored. Heartbeats stay tiny; the queue
	// travels only when it changes.
	var req struct {
		nowplaying.State
		Queue *[]nowplaying.QueueItem `json:"queue"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	st := req.State
	if req.Queue != nil {
		if len(*req.Queue) > 500 {
			return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
				"error": "queue too large; send at most 500 entries",
			})
		}
		h.store.SetQueue(profileID, *req.Queue)
	}
	if st.Title == "" && st.ItemID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "nothing identifiable is playing; DELETE to clear instead",
		})
	}

	h.store.Set(profileID, st)
	return c.JSON(fiber.Map{"ok": true})
}

// Get returns what this profile is playing, for a device that wants to
// join. 204 when nothing is playing — absence is a normal answer, not
// an error, and a poller should treat it as "show nothing".
func (h *NowPlayingHandler) Get(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}

	st, ok := h.store.Get(profileID)
	if !ok {
		return c.SendStatus(fiber.StatusNoContent)
	}
	// Read-time stamp so the joiner can do its age math in server time
	// and stay immune to clock skew. Never stored.
	st.ServerNowMs = time.Now().UnixMilli()
	return c.JSON(st)
}

// GetQueue hands a joiner the leader's queue, so it can keep playing
// past the current track instead of going silent when the song ends.
// 204 when no queue is published — a single-track join still works.
func (h *NowPlayingHandler) GetQueue(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}
	items, ok := h.store.Queue(profileID)
	if !ok {
		return c.SendStatus(fiber.StatusNoContent)
	}
	return c.JSON(fiber.Map{"items": items, "count": len(items)})
}

// Follow registers this device as actively following, which is what
// makes a party ACTIVE. With no followers the leader is just a device
// playing music and everything stays local; with followers, staying in
// step is worth the effort. Heartbeat it while following; it expires on
// its own if a TV sleeps.
func (h *NowPlayingHandler) Follow(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}
	var body struct {
		DeviceKey string `json:"deviceKey"`
		Stop      bool   `json:"stop"`
	}
	_ = c.BodyParser(&body)
	if body.DeviceKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "deviceKey is required",
		})
	}
	if body.Stop {
		h.store.StopFollowing(profileID, body.DeviceKey)
	} else {
		h.store.Following(profileID, body.DeviceKey)
	}
	return c.JSON(fiber.Map{"ok": true})
}

// Clear is the explicit stop — playback ended, drop the advertisement
// now rather than letting it age out.
func (h *NowPlayingHandler) Clear(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}
	h.store.Clear(profileID)
	return c.JSON(fiber.Map{"ok": true})
}
