package handlers

import (
	"encoding/json"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

// Party audio: the party's volume and each device's own level on top of
// it. Every route needs a live session (404 no-session); who is asking —
// the token id and its label — comes from the token, never a body; and
// every refusal is {"error": <English diagnostic, never UI copy>,
// "code": <code>}, a 409 also carrying the state. Clients branch on the
// status and the code only.

// audioRefusal answers a party-audio refusal without a state.
func audioRefusal(c *fiber.Ctx, status int, code, message string) error {
	return c.Status(status).JSON(fiber.Map{"error": message, "code": code})
}

func noSession(c *fiber.Ctx) error {
	return audioRefusal(c, fiber.StatusNotFound, "no-session", "nothing is playing")
}

// partyAudioJSON decodes a small JSON body from the RAW request. A party
// audio route never decompresses a body: a Content-Encoding is not
// honoured, so a compressed body is simply unparseable.
func partyAudioJSON(c *fiber.Ctx, out any) error {
	return json.Unmarshal(c.Request().Body(), out)
}

// SetVolume sets the party slider, {"volume": 0-100}, or one device's
// own level on top of it, {"deviceKey": <a followers[].deviceKey or the
// leaderTokenId>, "volume": 0-100}. Any device in the party may. The
// same value answers 200 without waking anyone.
func (h *NowPlayingHandler) SetVolume(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, deviceLabel := middleware.Device(c)
	var body struct {
		DeviceKey *string `json:"deviceKey"`
		// Raw, so only an integer LITERAL passes: 40.5, 4e1, "40" and
		// null are all refused.
		Volume json.RawMessage `json:"volume"`
	}
	if err := partyAudioJSON(c, &body); err != nil {
		return audioRefusal(c, fiber.StatusBadRequest, "bad-request",
			`the body must be {"volume": 0-100}, with "deviceKey" to set one device`)
	}
	if body.DeviceKey != nil && len(*body.DeviceKey) > nowplaying.MaxDeviceKeyLen {
		return audioRefusal(c, fiber.StatusBadRequest, "bad-request", "deviceKey is too long")
	}
	v, ok := volumeLiteral(body.Volume)
	if !ok {
		return audioRefusal(c, fiber.StatusBadRequest, "bad-volume",
			"volume must be a whole number from 0 to 100")
	}

	var st nowplaying.State
	var outcome nowplaying.Outcome
	if body.DeviceKey == nil {
		st, outcome = h.store.SetPartyVolume(profileID, deviceID, deviceLabel, v)
	} else {
		st, outcome = h.store.SetDeviceVolume(profileID, deviceID, *body.DeviceKey, v)
	}
	switch outcome {
	case nowplaying.OutcomeNoSession:
		return noSession(c)
	case nowplaying.OutcomeNotInParty:
		return refused(c, "not-in-party", "only a device in the party can change its volume", st)
	case nowplaying.OutcomeNoSuchDevice:
		return refused(c, "no-such-device", "that device is not in the party right now", st)
	case nowplaying.OutcomeServerBusy:
		return audioRefusal(c, fiber.StatusServiceUnavailable, "server-busy",
			"this party already keeps as many device levels as it can")
	}
	return c.JSON(fiber.Map{"ok": true, "state": stamped(c, st)})
}

// volumeLiteral reads a JSON integer literal from 0 to 100.
func volumeLiteral(raw json.RawMessage) (int, bool) {
	v, err := strconv.Atoi(string(raw))
	if err != nil || v < 0 || v > 100 {
		return 0, false
	}
	return v, true
}
