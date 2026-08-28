package handlers

import (
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

	var st nowplaying.State
	if err := c.BodyParser(&st); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
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
	return c.JSON(st)
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
