package handlers

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/nowplaying"
)

// NowPlayingHandler is the party line: a profile's devices report and
// read what that profile is playing right now, so a TV can join a book
// or an album mid-stream — and any of them can propose a state change
// (pause, seek, jump) that every device, the leader included, then
// converges on.
//
// Every route sits behind RequireProfile, which REJECTS the admin
// token — live listening is the most intimate data this server touches,
// and the household manager has no route into it, exactly as with
// stats. The profile comes from the token, never the request, so one
// person's devices can never read or write another's session. The
// DEVICE comes from the token too: the leader seat is keyed on the
// token id, so nothing a body says can impersonate another device.
type NowPlayingHandler struct {
	store *nowplaying.Store
}

// longPollHold is how long GET /nowplaying?waitFor= may stay open. It
// must sit well inside the server's WriteTimeout (cmd/server/main.go),
// or a held poll would be cut off before its answer could be written.
const longPollHold = 15 * time.Second

func NewNowPlayingHandler(store *nowplaying.Store) *NowPlayingHandler {
	return &NowPlayingHandler{store: store}
}

func unauthorizedProfile(c *fiber.Ctx) error {
	return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": "a profile token is required",
	})
}

// stamped returns the state with the read-time server clock, on EVERY
// response that carries a state — a TV computes its launch target from
// serverNowMs, and a response without it reintroduced seconds of lag.
func stamped(c *fiber.Ctx, st nowplaying.State) nowplaying.State {
	st.ServerNowMs = time.Now().UnixMilli()
	st.YourDeviceKey, _ = middleware.Device(c)
	// The requester's own device level, per response like its key.
	st.YourDeviceVolume = st.VolumeFor(st.YourDeviceKey)
	return st
}

// refused is a 409 with a machine-readable code and the state to catch
// up on. The codes are the contract: "stale" (apply this state and
// re-report), "seat-held" (another device leads; never apply),
// "legacy-leader" (the playing device predates remote control),
// "leader-asleep" (a suspended phone cannot be resumed from here).
func refused(c *fiber.Ctx, code, message string, st nowplaying.State) error {
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{
		"error": message,
		"code":  code,
		"state": stamped(c, st),
	})
}

// Report is a device publishing its current playback. Heartbeat this
// every few seconds while playing; the state goes stale (and stops
// being advertised) 90 seconds after the last report. The response is
// the resulting state — the reporter learns its revision, who follows
// and whether it still holds the seat without a second request.
func (h *NowPlayingHandler) Report(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, deviceLabel := middleware.Device(c)

	// The queue rides the same PUT, optionally: present = replace,
	// absent = keep what is stored. Heartbeats stay tiny; the queue
	// travels only when it changes.
	var req struct {
		nowplaying.State
		Queue           *[]nowplaying.QueueItem `json:"queue"`
		PositionMs      *int64                  `json:"positionMs"`
		BasedOnRevision *int64                  `json:"basedOnRevision"`
		ClaimSeat       bool                    `json:"claimSeat"`
		TakeOver        bool                    `json:"takeOver"`
		// SampledAtServerMs: when the position was measured, on the
		// server's clock (the reporter's clock plus its offset from
		// GET /time). Optional; without it arrival time stands in.
		SampledAtServerMs *int64 `json:"sampledAtServerMs"`
		// EditsQueue: this device carries out queue edits while it leads;
		// QueueEditAck: the last edit seq it carried out.
		EditsQueue   bool   `json:"editsQueue"`
		QueueEditAck *int64 `json:"queueEditAck"`
		// Features: what this device can do ("volume", …). A request
		// field, not a state field; kept only when the report is
		// accepted, and an invalid list is ignored rather than refused.
		Features []string `json:"features"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	st := req.State
	if st.Title == "" && st.ItemID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "nothing identifiable is playing; DELETE to clear instead",
		})
	}
	if req.Queue != nil && len(*req.Queue) > 500 {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": "queue too large; send at most 500 entries",
		})
	}
	opts := nowplaying.ReportOpts{
		DeviceID:        deviceID,
		DeviceLabel:     deviceLabel,
		BasedOnRevision: req.BasedOnRevision,
		ClaimSeat:       req.ClaimSeat,
		TakeOver:        req.TakeOver,
		// The store decides whether the queue lands, with the report and
		// under the same lock: a device that another device's seat
		// refuses cannot replace the party's queue.
		Queue:        req.Queue,
		EditsQueue:   req.EditsQueue,
		QueueEditAck: req.QueueEditAck,
	}
	opts.Features, _ = nowplaying.CleanFeatures(req.Features)
	if req.PositionMs != nil {
		st.PositionMs = *req.PositionMs
		opts.HasPositionMs = true
	}
	if req.SampledAtServerMs != nil {
		opts.SampledAtMs = *req.SampledAtServerMs
	}

	out, outcome := h.store.Report(profileID, st, opts)
	switch outcome {
	case nowplaying.OutcomeIgnored:
		// A legacy publisher while another device leads: swallowed, so
		// the old client's status line does not turn into a retry storm.
		return c.JSON(fiber.Map{"ok": true, "ignored": true, "state": stamped(c, out)})
	case nowplaying.OutcomeStale:
		return refused(c, "stale", "the session changed since this report was based; apply the state and report again", out)
	case nowplaying.OutcomeSeatHeld:
		label := out.LeaderLabel
		if label == "" {
			label = "another device"
		}
		return refused(c, "seat-held", label+" is leading this session", out)
	}
	return c.JSON(fiber.Map{"ok": true, "state": stamped(c, out)})
}

// Get returns what this profile is playing, for a device that wants to
// join. 204 when nothing is playing — absence is a normal answer, not
// an error, and a poller should treat it as "show nothing".
func (h *NowPlayingHandler) Get(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}

	// LONG POLL: with ?waitFor=<revision> the request is held until
	// something actually changes, so a follower learns about a track
	// change when it happens rather than up to a poll interval later.
	// Without the parameter this stays an ordinary immediate read.
	if raw := c.Query("waitFor"); raw != "" {
		if since, err := strconv.ParseInt(raw, 10, 64); err == nil {
			// Comfortably inside the follower TTL, so a held request
			// still counts as the device being alive — and inside the
			// server's write timeout, so the answer can still be sent.
			// c.Context() ends the hold on shutdown; see WaitForChange
			// for why a hung-up client does not.
			h.store.WaitForChange(c.Context(), profileID, since, longPollHold)
		}
	}

	st, ok := h.store.Get(profileID)
	if !ok {
		return c.SendStatus(fiber.StatusNoContent)
	}
	return c.JSON(stamped(c, st))
}

// GetQueue hands a joiner the leader's queue, so it can keep playing
// past the current track instead of going silent when the song ends.
// 204 when no queue is published — a single-track join still works.
func (h *NowPlayingHandler) GetQueue(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
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
// its own if a TV sleeps. role "viewer" is a device merely showing the
// session on screen — enough for the leader to listen for its intents,
// not enough to count as part of the party. role "controller" is a
// viewer on the Remote screen: same standing, counted separately.
func (h *NowPlayingHandler) Follow(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, deviceLabel := middleware.Device(c)
	var body struct {
		DeviceKey string `json:"deviceKey"`
		Role      string `json:"role"`
		Stop      bool   `json:"stop"`
		// RefreshAnchor: a follower about to launch again (the leader
		// pulled it to another track) asks for a settled anchor. A new
		// follower gets one without asking.
		RefreshAnchor bool `json:"refreshAnchor"`
		// DriftMs: the follower's latest CLOCK drift — the party
		// clock's target position minus its own player position, with
		// no route or output latency in it; positive = the follower is
		// BEHIND. DriftMeasuredAtMs is server time. Shown per device on
		// the dashboard; never acted on by the server.
		DriftMs           *int64 `json:"driftMs"`
		DriftMeasuredAtMs int64  `json:"driftMeasuredAtMs"`
		// Features: what this device can do ("volume", "announce", …),
		// kept for its token when it follows. Absent keeps what is kept;
		// an invalid list is ignored, never refused.
		Features []string `json:"features"`
	}
	_ = c.BodyParser(&body)
	// Older TVs mint their own key; anything newer is keyed on its
	// token, which is what the leader seat uses too.
	key := body.DeviceKey
	if key == "" {
		key = deviceID
	}
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "deviceKey is required",
		})
	}
	if len(key) > nowplaying.MaxDeviceKeyLen {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "deviceKey is too long",
		})
	}
	if body.Stop {
		h.store.StopFollowing(profileID, key)
		return c.JSON(fiber.Map{"ok": true})
	}
	// The revision the join (or the refresh request) landed at: an
	// anchor revision past it was taken after this device arrived.
	features, _ := nowplaying.CleanFeatures(body.Features)
	rev, ok := h.store.JoinFollow(profileID, key, deviceID, deviceLabel, body.Role,
		body.DriftMs, body.DriftMeasuredAtMs, features)
	if !ok {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "too many devices are following this person's playback",
		})
	}
	// Whose entry this is, for hand-overs (an Apple TV's key is a UUID).
	h.store.NoteToken(profileID, key, deviceID)
	if body.RefreshAnchor {
		if r := h.store.RequestAnchorRefresh(profileID); r > rev {
			rev = r
		}
	}
	return c.JSON(fiber.Map{"ok": true, "revision": rev})
}

// Intent is a device proposing a state change: setPlaying, setPositionMs
// or setIndex, each absolute, each based on the revision the device
// last saw. The server mutates the state and answers with it; the
// leader converges through its own long-poll like every follower.
func (h *NowPlayingHandler) Intent(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	_, deviceLabel := middleware.Device(c)
	var body struct {
		Type            string `json:"type"`
		Playing         *bool  `json:"playing"`
		PositionMs      *int64 `json:"positionMs"`
		Index           *int   `json:"index"`
		BasedOnRevision int64  `json:"basedOnRevision"`
		IfQueueRevision *int64 `json:"ifQueueRevision"`
		// Queue edits: addToQueue carries items (and next); removeFromQueue
		// an index, an itemId and ifQueueRevision.
		Items  []nowplaying.QueueItem `json:"items"`
		Next   bool                   `json:"next"`
		ItemID string                 `json:"itemId"`
	}
	if err := c.BodyParser(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if body.Type == nowplaying.IntentAddToQueue || body.Type == nowplaying.IntentRemoveFromQueue {
		return h.editQueue(c, profileID, deviceLabel, body.Type, body.Items, body.Next,
			body.Index, body.ItemID, body.IfQueueRevision)
	}
	in := nowplaying.IntentRequest{Type: body.Type, BasedOnRevision: body.BasedOnRevision}
	switch body.Type {
	case nowplaying.IntentSetPlaying:
		if body.Playing == nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "playing is required"})
		}
		in.Playing = *body.Playing
	case nowplaying.IntentSetPositionMs:
		if body.PositionMs == nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "positionMs is required"})
		}
		in.PositionMs = *body.PositionMs
	case nowplaying.IntentSetIndex:
		if body.Index == nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "index is required"})
		}
		in.Index = *body.Index
		in.IfQueueRevision = body.IfQueueRevision
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "type must be setPlaying, setPositionMs, setIndex, addToQueue or removeFromQueue",
		})
	}

	st, outcome := h.store.ApplyIntent(profileID, deviceLabel, in)
	switch outcome {
	case nowplaying.OutcomeNoSession:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "nothing is playing",
			"code":  "no-session",
		})
	case nowplaying.OutcomeLegacyLeader:
		return refused(c, "legacy-leader", "the playing device cannot be controlled remotely; update Alexandria on it", st)
	case nowplaying.OutcomeLeaderAsleep:
		label := st.LeaderLabel
		if label == "" {
			label = "the playing device"
		}
		return refused(c, "leader-asleep", label+" is asleep and cannot resume from here", st)
	case nowplaying.OutcomeStale:
		return refused(c, "stale", "the session changed since this device looked", st)
	case nowplaying.OutcomeNoQueue:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "no queue is published for this session",
			"code":  "no-queue",
			"state": stamped(c, st),
		})
	}
	return c.JSON(fiber.Map{"ok": true, "state": stamped(c, st)})
}

// editQueue is the Intent route's addToQueue / removeFromQueue: a change
// to the party's queue that the LEADER carries out (Store.EditQueue).
// Every field is bounded here: an entry is metadata only — a source, an
// id, a title — never a URL or a credential, and nothing in it is read
// by the server beyond its length.
func (h *NowPlayingHandler) editQueue(c *fiber.Ctx, profileID, deviceLabel, kind string,
	items []nowplaying.QueueItem, next bool, index *int, itemID string, ifQueueRevision *int64) error {
	bad := func(msg string) error {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": msg})
	}
	in := nowplaying.QueueEditRequest{Type: kind}
	switch kind {
	case nowplaying.IntentAddToQueue:
		if len(items) == 0 || len(items) > nowplaying.MaxEditItems {
			return bad("items: send 1 to " + strconv.Itoa(nowplaying.MaxEditItems) + " entries")
		}
		for _, it := range items {
			if !validQueueSource(it.Source) || it.ItemID == "" ||
				len(it.ItemID) > nowplaying.MaxQueueFieldLen ||
				len(it.Title) > nowplaying.MaxQueueFieldLen ||
				len(it.Author) > nowplaying.MaxQueueFieldLen ||
				it.DurationSec < 0 || it.DurationSec > 86400 {
				return bad("each item needs a source, an itemId and fields within their limits")
			}
		}
		in.Items = items
		in.Next = next
	case nowplaying.IntentRemoveFromQueue:
		if index == nil || *index < 0 || ifQueueRevision == nil ||
			itemID == "" || len(itemID) > nowplaying.MaxQueueFieldLen {
			return bad("index, itemId and ifQueueRevision are required")
		}
		in.Index = *index
		in.ItemID = itemID
		in.IfQueueRevision = *ifQueueRevision
	}

	st, outcome := h.store.EditQueue(profileID, deviceLabel, in)
	switch outcome {
	case nowplaying.OutcomeNoSession:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "nothing is playing",
			"code":  "no-session",
		})
	case nowplaying.OutcomeLegacyLeader:
		return refused(c, "legacy-leader", "the playing device cannot be controlled remotely; update Alexandria on it", st)
	case nowplaying.OutcomeLeaderAsleep:
		return refused(c, "leader-asleep", "nobody is leading the party right now", st)
	case nowplaying.OutcomeLeaderCannotEdit:
		label := st.LeaderLabel
		if label == "" {
			label = "the playing device"
		}
		return refused(c, "leader-cannot-edit", label+" can't take queue changes yet; update Alexandria on it", st)
	case nowplaying.OutcomeNoQueue:
		return refused(c, "no-queue", "this session has no queue to change", st)
	case nowplaying.OutcomeIsCurrent:
		return refused(c, "is-current", "that song is playing; skip it instead", st)
	case nowplaying.OutcomeStale:
		return refused(c, "stale", "the queue changed since this device looked", st)
	case nowplaying.OutcomeBusy:
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
			"error": "too many queue changes are waiting on the leader",
			"code":  "busy",
			"state": stamped(c, st),
		})
	}
	return c.JSON(fiber.Map{"ok": true, "state": stamped(c, st)})
}

// validQueueSource: a short lowercase word ("plex", "jellyfin",
// "subsonic"), never anything a client could smuggle more into.
func validQueueSource(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// Clear is the explicit stop. ?reason=stopped from a leader with
// followers vacates the seat and keeps the party going; ?reason=stop-all
// from the leader ends it for every device; anything else (the
// sharing toggle, playback ending with nobody listening) drops the
// advertisement now rather than letting it age out. The answer says
// which of those actually happened — a phone that asked to stop
// everywhere must not report success when it had merely left.
func (h *NowPlayingHandler) Clear(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, deviceLabel := middleware.Device(c)
	outcome := h.store.EndBy(profileID, deviceID, deviceLabel, c.Query("reason"))
	return c.JSON(fiber.Map{"ok": true, "outcome": outcome.String()})
}

// HandOver: a device in the party — the seat holder or any other —
// names a follower as the new leader ("make this device the leader").
// The server moves the seat and bumps the revision; the target sees
// leaderTokenId == its own on its next poll and starts publishing from
// its own position, the old leader's next report is refused seat-held
// and its client joins as a follower (handedOverFrom names it, and
// handedOverByLabel who asked).
func (h *NowPlayingHandler) HandOver(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return unauthorizedProfile(c)
	}
	deviceID, deviceLabel := middleware.Device(c)
	var body struct {
		DeviceKey string `json:"deviceKey"`
	}
	if err := c.BodyParser(&body); err != nil || body.DeviceKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "deviceKey is required",
		})
	}
	st, outcome := h.store.HandOver(profileID, deviceID, deviceLabel, body.DeviceKey)
	switch outcome {
	case nowplaying.OutcomeNoSession:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "nothing is playing",
			"code":  "no-session",
		})
	case nowplaying.OutcomeNotInParty:
		return refused(c, "not-in-party", "only a device in the party can choose its leader", st)
	case nowplaying.OutcomeNoSuchFollower:
		return refused(c, "no-such-follower", "that device is not following right now", st)
	}
	return c.JSON(fiber.Map{"ok": true, "state": stamped(c, st)})
}

// Time is the party's clock: the same server instant every state
// carries as serverNowMs, on its own, so a device can measure its
// offset NTP-style (a few round trips, median of serverNowMs − (sent +
// rtt/2)). A few hundred bytes; the apps ask a handful of times at a
// party's start and once a minute after.
func Time(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"serverNowMs": time.Now().UnixMilli()})
}
