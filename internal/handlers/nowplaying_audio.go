package handlers

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

// Party audio: the party's volume and each device's own level on top of
// it, and announcements — a recorded voice message every speaking device
// plays at one instant. Every route needs a live session (404
// no-session); who is asking — the token id and its label — comes from
// the token, never a body; and every refusal is {"error": <English
// diagnostic, never UI copy>, "code": <code>}, a 409 also carrying the
// state. Clients branch on the status and the code only.
//
// Voice is held in memory only, never logged (cmd/server skips the
// id-bearing routes in its access log) and served only to the profile's
// own party, with the Bearer header — never a token in a URL.

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

func notFound(c *fiber.Ctx) error {
	return audioRefusal(c, fiber.StatusNotFound, "not-found", "no such announcement")
}

// copyBody is the request body as the caller's own bytes. Fiber reuses
// the request buffer once the handler returns (Immutable is off), so
// anything the store keeps must be a copy — taken from the RAW body,
// never a decompressed one.
func copyBody(c *fiber.Ctx) []byte {
	return append([]byte(nil), c.Request().Body()...)
}

// SetAnnouncePolicy: the seat holder's {"leaderOnly": true|false}. True
// lets only the seat holder start an announcement — and only while it
// holds the seat.
func (h *NowPlayingHandler) SetAnnouncePolicy(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, _ := middleware.Device(c)
	var body struct {
		LeaderOnly *bool `json:"leaderOnly"`
	}
	if err := partyAudioJSON(c, &body); err != nil || body.LeaderOnly == nil {
		return audioRefusal(c, fiber.StatusBadRequest, "bad-request", `the body must be {"leaderOnly": true or false}`)
	}
	st, outcome := h.store.SetAnnouncePolicy(profileID, deviceID, *body.LeaderOnly)
	switch outcome {
	case nowplaying.OutcomeNoSession:
		return noSession(c)
	case nowplaying.OutcomeNotLeader:
		return refused(c, "not-leader", "only the device leading the party can change who may announce", st)
	}
	return c.JSON(fiber.Map{"ok": true, "state": stamped(c, st)})
}

// wavType: audio/wav, audio/x-wav or audio/wave, in any case, any
// parameters ignored.
func wavType(contentType string) bool {
	if i := strings.IndexByte(contentType, ';'); i >= 0 {
		contentType = contentType[:i]
	}
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return true
	}
	return false
}

// PostClip starts a recorded announcement. The body is the clip itself:
// a canonical 16 kHz mono 16-bit PCM WAV (nowplaying.ParseClipWAV),
// Content-Type audio/wav. Everything about the body is checked before the
// store is asked, in the contract's order; the server then picks the
// chime and voice instants and every parked poll wakes to them.
func (h *NowPlayingHandler) PostClip(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, deviceLabel := middleware.Device(c)
	if !wavType(string(c.Request().Header.ContentType())) {
		return audioRefusal(c, fiber.StatusUnsupportedMediaType, "unsupported-type",
			"send the clip as Content-Type: audio/wav")
	}
	raw := c.Request().Body()
	switch {
	case len(raw) == 0:
		return audioRefusal(c, fiber.StatusBadRequest, "bad-audio", "the clip is empty")
	case len(raw) > nowplaying.MaxClipBytes:
		return audioRefusal(c, fiber.StatusRequestEntityTooLarge, "too-large", "a clip is at most 2 MiB")
	}
	durationMs, code := nowplaying.ParseClipWAV(raw)
	switch code {
	case "bad-audio":
		return audioRefusal(c, fiber.StatusBadRequest, "bad-audio",
			"a clip must be a 16 kHz mono 16-bit PCM WAV with a 44-byte header")
	case "too-short":
		return audioRefusal(c, fiber.StatusBadRequest, "too-short", "a clip is at least 300 ms")
	case "too-large":
		return audioRefusal(c, fiber.StatusRequestEntityTooLarge, "too-large", "a clip is at most 60.5 s")
	}
	st, ann, outcome := h.store.StartClip(profileID, deviceID, deviceLabel, copyBody(c), durationMs)
	if outcome != nowplaying.OutcomeOK {
		return startRefused(c, outcome, st)
	}
	return c.JSON(fiber.Map{"ok": true, "announcement": ann, "state": stamped(c, st)})
}

// startRefused answers an announcement the store would not start.
func startRefused(c *fiber.Ctx, outcome nowplaying.Outcome, st nowplaying.State) error {
	switch outcome {
	case nowplaying.OutcomeNoSession:
		return noSession(c)
	case nowplaying.OutcomeNotInParty:
		return refused(c, "not-in-party", "only a device in the party can make an announcement", st)
	case nowplaying.OutcomeLeaderOnly:
		return refused(c, "leader-only", "only the device leading the party can make announcements right now", st)
	case nowplaying.OutcomeAnnounceBusy:
		return refused(c, "busy", "another announcement is playing", st)
	case nowplaying.OutcomeRateLimited:
		return audioRefusal(c, fiber.StatusTooManyRequests, "rate-limited", "too many announcements this minute")
	case nowplaying.OutcomeServerBusy:
		return audioRefusal(c, fiber.StatusServiceUnavailable, "server-busy",
			"the server is holding as many announcements as it can")
	}
	return audioRefusal(c, fiber.StatusInternalServerError, "internal", "the announcement could not be started")
}

// GetClip hands a device in the party the clip's exact bytes. Anyone else
// — and an unknown, expired or live id — gets 404 not-found, which never
// says whether the clip exists.
func (h *NowPlayingHandler) GetClip(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, _ := middleware.Device(c)
	wav, outcome := h.store.Clip(profileID, deviceID, c.Params("id"))
	switch outcome {
	case nowplaying.OutcomeOK:
	case nowplaying.OutcomeNoSession:
		return noSession(c)
	default:
		return notFound(c)
	}
	c.Set(fiber.HeaderContentType, "audio/wav")
	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.Send(wav)
}

// StopAnnouncement stops the party's announcement now. The device leading
// the party or the one that sent it may; a repeat stop changes nothing.
func (h *NowPlayingHandler) StopAnnouncement(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, deviceLabel := middleware.Device(c)
	st, outcome := h.store.StopAnnouncement(profileID, deviceID, deviceLabel, c.Params("id"))
	switch outcome {
	case nowplaying.OutcomeNoSession:
		return noSession(c)
	case nowplaying.OutcomeNotFound:
		return notFound(c)
	case nowplaying.OutcomeNotAllowed:
		return refused(c, "not-allowed",
			"only the device leading the party or the one that sent it can stop an announcement", st)
	}
	return c.JSON(fiber.Map{"ok": true, "state": stamped(c, st)})
}
