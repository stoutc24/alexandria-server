// Package nowplaying is the party line: each profile's devices can see
// what that profile is playing RIGHT NOW, so an Apple TV can join a
// book or an album mid-stream at the same position — and, since the
// conductor protocol, so that ANY of the profile's devices can pause,
// skip or seek what is playing and every other device converges on it.
//
// Deliberately IN MEMORY, never the database. This is real-time
// presence, not history — the stats table is where history lives, with
// its own rules. Keeping presence off disk means what someone is
// playing at this moment is never written anywhere, survives no
// restart, and needs no retention policy. A server restart just means
// the playing device re-publishes within seconds.
//
// The privacy boundary matches stats exactly: a profile's presence is
// readable by THAT PROFILE'S devices and nobody else. Not the admin —
// live listening is the most intimate slice of the data this server
// touches.
//
// The server never sends a command. It holds STATE plus who currently
// owns the leader seat; devices act on the state. A remote "pause" is a
// state mutation the leader reconciles to, not an instruction sent to
// it — the same loop every follower already runs.
package nowplaying

import (
	"context"
	"sync"
	"time"
)

// Presence is one device in the party: following (playing the same
// thing) or merely viewing (its Home card is on screen). Labels come
// from the token rows, never from a request body.
type Presence struct {
	Label   string `json:"label"`
	Role    string `json:"role"`
	SinceMs int64  `json:"sinceMs"`
	// DeviceKey is the follower's token id — what a handover names, and
	// how a phone finds its own row on the dashboard.
	DeviceKey string `json:"deviceKey,omitempty"`
	// DriftMs is the follower's CLOCK drift as it last measured it: the
	// party clock's target (the leader's position, extrapolated from the
	// anchor) minus the follower's own PLAYER position. No route or
	// output latency is in it — ear-level figures stay in the device's
	// own status line. Positive = the follower is BEHIND. Nil until it
	// has reported one. Informational: the server never acts on it.
	DriftMs           *int64 `json:"driftMs,omitempty"`
	DriftMeasuredAtMs int64  `json:"driftMeasuredAtMs,omitempty"`
	// Volume is this device's own position on top of the party volume,
	// 0–100 (absent = 100), and Features what its token said it can do
	// (absent = none known). Both read-time, by the row's token
	// (party_audio.go); never stored from a report.
	Volume   *int     `json:"volume,omitempty"`
	Features []string `json:"features,omitempty"`
}

// Intent is the last remote state change applied — kept so a device can
// say "Paused from Living Room TV" and so the leader can tell a change
// it made from one it was asked to make.
type Intent struct {
	Seq      int64  `json:"seq"`
	Type     string `json:"type"`
	ByLabel  string `json:"byLabel,omitempty"`
	AtMs     int64  `json:"atMs"`
	Revision int64  `json:"revision"`
}

// State is one profile's currently-playing item as last reported.
type State struct {
	Source      string `json:"source"`
	ItemID      string `json:"itemId"`
	Title       string `json:"title"`
	Author      string `json:"author,omitempty"`
	CoverURL    string `json:"coverUrl,omitempty"`
	PositionSec int    `json:"positionSec"`
	DurationSec int    `json:"durationSec,omitempty"`
	IsPlaying   bool   `json:"isPlaying"`
	// DeviceLabel names the reporting device ("Chris's iPhone"), so a
	// joiner can say where the audio is coming from.
	DeviceLabel string `json:"deviceLabel,omitempty"`
	// QueueIndex is where the leader currently sits in its published
	// queue; QueueLen tells a joiner a queue exists without a second
	// request. Both zero when no queue was published. QueueIndex is always
	// sent: with omitempty a leader on its FIRST song (index 0) published
	// no index at all, and the Apple TV, which read a missing index as
	// unknown, could not steer that party (2026-10-30). Readers go by
	// QueueLen for whether a queue exists.
	QueueIndex int `json:"queueIndex"`
	QueueLen   int `json:"queueLen,omitempty"`
	// QueueOffset is the leader's local index of the FIRST published
	// entry, for a leader whose real queue is longer than it publishes.
	// Purely informational for the leader itself (a jump intent names an
	// index into the published list; the leader adds the offset back).
	QueueOffset int `json:"queueOffset,omitempty"`

	// PositionMs is the leader's position in milliseconds. PositionSec
	// stays for readers that predate it, but every anchor computation
	// uses this: whole seconds put followers up to a second off before
	// a single byte was lost to the network.
	PositionMs int64 `json:"positionMs"`
	// Rate is the leader's playback rate (1.0 when absent). Position
	// advances by rate × wall clock, so the anchor is meaningless
	// without it — an audiobook at 1.5× re-anchored on every heartbeat
	// before this existed.
	Rate float64 `json:"rate,omitempty"`

	// TrackStartedAtMs is when the CURRENT track began, on the server's
	// clock — derived here, not reported. It is the anchor every device
	// should play from: position is simply (now − trackStartedAt) × rate,
	// which is continuous, drift-free, and identical on every follower,
	// instead of each one extrapolating from whatever sampled position
	// happened to be in the last heartbeat.
	//
	// Re-anchored only when the track changes, playback resumes, the
	// leader acknowledges a remote intent, or the leader's reported
	// position stops matching the anchor (a scrub, a restart). Ordinary
	// heartbeats leave it alone, so it does not wander.
	TrackStartedAtMs int64 `json:"trackStartedAtMs,omitempty"`

	// The leader seat. LeaderTokenID is the device token that owns the
	// session clock; LeaderLabel is that token's label. Both empty when
	// the seat is vacant (the leader left with followers still playing,
	// or went silent long enough to be presumed gone) — a follower may
	// then claim it and the party continues from its own player.
	LeaderTokenID string `json:"leaderTokenId,omitempty"`
	LeaderLabel   string `json:"leaderLabel,omitempty"`
	// LegacyLeader marks a publisher that does not speak the conductor
	// protocol (it sends no basedOnRevision). It cannot be controlled
	// remotely, and saying so honestly beats a flip-flopping party.
	LegacyLeader bool `json:"legacyLeader,omitempty"`
	// LeaderReportedAtMs is the last report from the seat holder.
	// LeaderDormant (read-time) means it has been quiet past
	// leaderLostAfter: a phone paused in a pocket is suspended by iOS
	// and cannot answer a play intent, and the TV should say that
	// rather than spin.
	LeaderReportedAtMs int64 `json:"leaderReportedAtMs,omitempty"`
	LeaderDormant      bool  `json:"leaderDormant,omitempty"`
	SeatVacantSinceMs  int64 `json:"seatVacantSinceMs,omitempty"`
	EndedByLeader      bool  `json:"endedByLeader,omitempty"`

	// AnchorRevision is the revision at which TrackStartedAtMs was last
	// derived. A follower that joined at revision J and later holds a
	// state whose AnchorRevision > J knows the anchor was taken AFTER
	// its arrival — from a settled heartbeat, not from the first report
	// of a track (which the leader sends before its own player has
	// rolled, so followers launched on it ran a constant few hundred
	// milliseconds ahead until a scrub forced a re-anchor).
	AnchorRevision int64 `json:"anchorRevision,omitempty"`
	// AnchorRefreshPending (read-time): a follower joined, or asked,
	// and the leader's next accepted report re-anchors regardless of
	// the 2 s keep-window. A leader that sees this reports at once.
	AnchorRefreshPending bool `json:"anchorRefreshPending,omitempty"`
	// A SCHEDULED START the leader announced: StartItemID (empty = the
	// current item) begins at StartPositionMs when the server clock
	// reads StartAtMs. Until then the position stands at
	// StartPositionMs; from then on it advances from that instant, so
	// every device that knows the server clock starts the same note at
	// the same moment instead of starting when it can and settling.
	//
	// A StartItemID other than the current item is the NEXT track
	// announced ahead (followers prepare it and start it at T); the
	// current item is anchored as usual and is never held by it. The
	// leader announces with 3 s or less of the current item left (T =
	// its predicted boundary + max(its time to audio + 40 ms, 700 ms))
	// and repeats the three fields on EVERY report of the current item
	// while it stands. A report that omits them WITHDRAWS it:
	// nothing is carried forward. The server also refuses one on its
	// own (defence in depth — a correct leader never needs it): on a
	// paused report; when T does not fit the trajectory the report
	// itself describes (see nextStartFitsTrajectory); on a re-send of
	// the (item, T) an intent cancelled, until T + 2 s; and it zeroes
	// one on any intent, a handover, a seat vacated by the leader's
	// DELETE and a stop-all. The leader's first report(s) of the
	// announced item carry StartItemID = that item and StartAtMs = T,
	// and its first ROLLING report the measured audible zero instead;
	// within 2 s of that instant (judged at the instant the position
	// was sampled) the item is anchored AT StartAtMs, whatever position
	// the report carries. A schedule for the current item is cleared by
	// a report that carries none, as before. StartAtMs <= 0 is no
	// schedule. Clients that predate it see position + isPlaying and
	// settle as before, at worst a lead time early.
	StartAtMs       int64  `json:"startAtMs,omitempty"`
	StartPositionMs int64  `json:"startPositionMs,omitempty"`
	StartItemID     string `json:"startItemId,omitempty"`
	// HandedOverAtMs: the seat was passed by its holder to a follower
	// (POST /handover) at this server time. The new holder's first
	// report must be based on at least this revision.
	HandedOverAtMs int64 `json:"handedOverAtMs,omitempty"`
	// HandedOverFrom is the token id that held the seat before that
	// hand-over, and HandedOverByLabel the device that asked for it —
	// the seat holder, or any other device in the party. A leader that
	// did not ask learns from them that its seat was given away: it
	// follows in place and says who did it. Both stay while the device
	// handed the seat holds it.
	HandedOverFrom    string `json:"handedOverFrom,omitempty"`
	HandedOverByLabel string `json:"handedOverByLabel,omitempty"`
	// Ended: the leader stopped FOR EVERYONE (DELETE ?reason=stop-all).
	// The record lingers for endedTTL, paused, so every parked poll
	// wakes to a state that says so — a 204 reads as "leader quiet,
	// keep playing", which is the opposite of what was asked.
	// EndedByLabel names the device; EndedAtMs is when.
	Ended        bool   `json:"ended,omitempty"`
	EndedByLabel string `json:"endedByLabel,omitempty"`
	EndedAtMs    int64  `json:"endedAtMs,omitempty"`

	// Intent bookkeeping. LastIntentRevision is the revision the most
	// recent intent produced; a leader report based on anything older
	// has not seen it yet and is refused (409 stale) so a heartbeat can
	// never overwrite a remote pause. LeaderAckRevision is the revision
	// of the first leader report that HAD seen it — that report always
	// re-anchors, because the leader's clock, not the server's guess at
	// intent time, is the truth followers must converge on. ItemRevision
	// is when the item last changed; an intent based on anything older
	// is stale ("the track already moved") and is not applied.
	IntentSeq          int64   `json:"intentSeq,omitempty"`
	LastIntent         *Intent `json:"lastIntent,omitempty"`
	LastIntentRevision int64   `json:"lastIntentRevision,omitempty"`
	LeaderAckRevision  int64   `json:"leaderAckRevision,omitempty"`
	ItemRevision       int64   `json:"itemRevision,omitempty"`

	// QueueEdits (read-time) are the changes devices asked of the
	// leader's queue that it has not carried out yet, oldest first (owner,
	// 2026-10-28: "Remote and Party Play devices need the ability to add
	// songs to party play queue, and remove songs"). The leader owns its
	// queue: it applies each one, publishes the queue that results and
	// acknowledges the last seq it applied (ReportOpts.QueueEditAck).
	// Never stored from a report.
	QueueEdits []QueueEdit `json:"queueEdits,omitempty"`
	// LeaderEditsQueue: the seat holder carries out QueueEdits, as its
	// reports say. While a leader that predates them holds the seat, an
	// edit is refused rather than left waiting for a device that will
	// never act on it.
	LeaderEditsQueue bool `json:"leaderEditsQueue,omitempty"`

	// FollowerCount is how many OTHER devices are actively following
	// this profile right now. Zero means nobody joined: the party is
	// inactive and every device should just play locally. Non-zero
	// means the session is shared and worth keeping in step.
	FollowerCount int `json:"followerCount"`
	// Followers lists them; Viewers counts devices merely showing the
	// session on a Home card. The leader listens for remote intents
	// only while somebody is following OR viewing — nobody looking
	// means no extra socket on the phone.
	//
	// Controllers is how many of those viewers are REMOTES: a device on
	// the Remote screen, steering the session without playing it.
	// They are counted inside Viewers too, so a leader from before the
	// role existed still listens for their intents; Controllers is
	// only the header's "· 1 remote".
	Followers   []Presence `json:"followers,omitempty"`
	Viewers     int        `json:"viewers,omitempty"`
	Controllers int        `json:"controllers,omitempty"`

	// Revision increments on every change: publish, queue change, intent,
	// presence change, clear. A follower uses it to tell "something new"
	// from "same state, new heartbeat" without diffing every field —
	// and, more importantly, to notice it MISSED updates (a jump of
	// several revisions) so it can reconcile hard instead of assuming
	// continuity. Seeded from the boot clock so a server restart never
	// hands a follower a revision lower than one it already holds.
	Revision int64 `json:"revision"`

	// QueueRevision increments only when the tracklist itself changes,
	// so a follower refetches the queue on a shuffle or a reorder and
	// never on an ordinary heartbeat.
	QueueRevision int64 `json:"queueRevision"`
	// UpdatedAtMs is the SERVER clock when this report landed. A joiner
	// extrapolates: position + (now - updatedAt) while playing. Using
	// the server clock sidesteps device clock skew entirely — both the
	// reporter and the joiner talk to the same clock.
	UpdatedAtMs int64 `json:"updatedAtMs"`

	// ServerNowMs is the server clock AT READ TIME, stamped by the
	// handler on EVERY response and never stored. It lets a joiner
	// compute the report's age entirely in server time — (serverNow −
	// updatedAt) — and then add only its own local elapsed-since-receipt,
	// so the joiner's wall clock never gets compared against the
	// server's. Without it, a few seconds of clock skew between a
	// Windows server and an Apple TV became a few seconds of playback
	// lag (device report).
	ServerNowMs int64 `json:"serverNowMs,omitempty"`
	// YourDeviceKey is the REQUESTER'S own token id, stamped by the
	// handler on every state response and never stored: the key the seat
	// (leaderTokenId) and the followers list use for this very device.
	// It is how a device knows it leads, that a handover named it, and
	// which follower row is its own.
	YourDeviceKey string `json:"yourDeviceKey,omitempty"`

	// Party volume (party_audio.go). Every field below is read-time;
	// never stored from a report: Report clears them next to QueueEdits,
	// so a PUT body that carries them changes nothing, and a leader that
	// predates them cannot reset them by leaving them out.
	//
	// PartyVolume is the party slider's POSITION, 0–100 (read-time; never
	// stored from a report). Absent = 100; 0 is real (silent music), which
	// is why it is a pointer. PartyVolumeByLabel names the device that
	// last set it (read-time; never stored from a report).
	PartyVolume        *int   `json:"partyVolume,omitempty"`
	PartyVolumeByLabel string `json:"partyVolumeByLabel,omitempty"`
	// LeaderVolume is the seat holder's own device position, for the
	// Remote's rows (read-time; never stored from a report). Absent = 100.
	LeaderVolume *int `json:"leaderVolume,omitempty"`
	// YourDeviceVolume is the REQUESTER'S own device position, stamped by
	// the handler per response like YourDeviceKey (read-time; never
	// stored from a report). Absent = 100. A device plays its music at
	// partyVolume × yourDeviceVolume and never applies another row's.
	YourDeviceVolume *int `json:"yourDeviceVolume,omitempty"`
	// LeaderFeatures is what the seat holder's token said it can do, on
	// its reports or while it followed (read-time; never stored from a
	// report body's own field). Absent = none known.
	LeaderFeatures []string `json:"leaderFeatures,omitempty"`
	// deviceVolumes is a fresh per-read copy of every device position by
	// token, for VolumeFor. Unexported, so encoding/json and BodyParser
	// never see it either way.
	deviceVolumes map[string]int
}

// QueueItem is one entry of the leader's play queue — METADATA ONLY.
// The server never holds a stream, a URL, or a credential; a joiner
// resolves each entry against its own connections, and an entry it
// cannot resolve is skipped, never guessed.
type QueueItem struct {
	Source      string `json:"source"`
	ItemID      string `json:"itemId"`
	Title       string `json:"title"`
	Author      string `json:"author,omitempty"`
	DurationSec int    `json:"durationSec,omitempty"`
}

// staleAfter is how long a report stays credible without a heartbeat.
// The publisher heartbeats every few seconds while playing; a state
// older than this means the device died mid-song, and advertising a
// ghost session is worse than none. With followers still playing the
// seat is vacated instead of the session deleted, so the party can
// carry on from one of them.
const staleAfter = 90 * time.Second

// leaderLostAfter is how long the seat holder may stay silent before it
// is presumed asleep. It must exceed three heartbeats plus one report
// timeout (5s each while followed), or a single slow request would
// read as a dead leader and hand the party off spuriously.
const leaderLostAfter = 30 * time.Second

// followerTTL: a follower that has not checked in this recently is
// assumed gone (app closed, TV asleep), so the party goes quiet on its
// own rather than pretending someone is still listening.
const followerTTL = 30 * time.Second

// endedTTL is how long an ended party's record stays readable. Long
// enough for every follower to wake and read it (a parked poll wakes
// at once; one in its 2 s back-off reads next), short enough that a
// stopped party is not advertised as a paused one for long.
const endedTTL = 30 * time.Second

// Roles a device can register with. A controller is a viewer that has
// opened the Remote: it may send intents like any viewer, is never part
// of the party (it plays nothing), and is counted so the others can see
// a remote is on.
const (
	RoleFollower   = "follower"
	RoleViewer     = "viewer"
	RoleController = "controller"
)

// Outcome is what the store decided about a report or an intent. The
// handler maps it to a status; the store never knows about HTTP.
type Outcome int

const (
	OutcomeOK Outcome = iota
	// OutcomeIgnored: a legacy publisher reported while another device
	// holds the seat. Not applied, not an error — the old client would
	// retry an error at 1Hz forever.
	OutcomeIgnored
	// OutcomeStale: the report/intent is based on a revision that
	// predates the last intent / item change. The caller gets the
	// current state to catch up on.
	OutcomeStale
	// OutcomeSeatHeld: another live device leads. Never applied.
	OutcomeSeatHeld
	OutcomeNoSession
	OutcomeLegacyLeader
	OutcomeLeaderAsleep
	OutcomeNoQueue
	// OutcomeNotLeader: only the seat holder may hand the seat over.
	OutcomeNotLeader
	// OutcomeNoSuchFollower: the named device is not a live follower.
	OutcomeNoSuchFollower
	// OutcomeLeaderCannotEdit: the leader does not carry out queue edits
	// (it predates them).
	OutcomeLeaderCannotEdit
	// OutcomeBusy: too many queue edits are already waiting on the leader.
	OutcomeBusy
	// OutcomeIsCurrent: a removal named the song that is playing.
	OutcomeIsCurrent
	// OutcomeNotInParty: a hand-over asked by a device that neither
	// holds the seat nor takes part in the party.
	OutcomeNotInParty
	// OutcomeNoSuchDevice: a device volume named neither the seat nor a
	// live follower.
	OutcomeNoSuchDevice
	// OutcomeServerBusy: a store-wide or per-party cap is full (device
	// volumes).
	OutcomeServerBusy
)

// ReportOpts is who is reporting and what they had seen. Device identity
// comes from the authenticated token, never the body.
type ReportOpts struct {
	DeviceID    string
	DeviceLabel string
	// BasedOnRevision is nil for a legacy publisher (pre-conductor
	// clients send none), which also marks it uncontrollable.
	BasedOnRevision *int64
	// ClaimSeat: take a vacant or dormant seat (a follower carrying the
	// party on). TakeOver: take it regardless — the explicit "play here
	// instead" a person pressed.
	ClaimSeat bool
	TakeOver  bool
	// HasPositionMs: the body carried positionMs; otherwise positionSec
	// is promoted.
	HasPositionMs bool
	// SampledAtMs: the SERVER instant at which the reporter measured
	// its position (its own clock plus its measured offset). 0 when the
	// reporter cannot say; then the arrival time stands in and the
	// request's one-way latency lands in the anchor.
	SampledAtMs int64
	// Queue, when set, replaces the profile's queue, and only from a
	// device that leads: it lands with an accepted report, in the same
	// revision, and with a stale one from the seat's own holder, as it
	// always has. Any other refused report leaves the party's queue
	// alone; its queue is held for that device instead (see Store.held).
	// nil keeps the stored queue; an empty list clears it.
	Queue *[]QueueItem
	// EditsQueue: this device carries out QueueEdits while it leads.
	EditsQueue bool
	// QueueEditAck, from the seat holder, is the last QueueEdit seq it
	// has carried out; every edit up to it is dropped. A leader repeats
	// it on every report, so a refused one costs nothing.
	QueueEditAck *int64
	// Features is what the reporting device can do ("volume", …), already
	// cleaned (CleanFeatures); nil = none sent, keep what is stored. Kept
	// for its token only when the report is accepted.
	Features []string
}

// IntentRequest is a proposed state change. Every type is absolute and
// idempotent — there is no "next" or "seek by": a device names the index
// or the position it wants, computed from the state it saw, so two rooms
// pressing skip at once cannot skip twice.
type IntentRequest struct {
	// Type: setPlaying | setPositionMs | setIndex.
	Type            string
	Playing         bool
	PositionMs      int64
	Index           int
	BasedOnRevision int64
	// IfQueueRevision, when set on setIndex, is the queue revision the
	// index was computed against. A reorder or a re-published window
	// bumps the session revision but not the item revision, so without
	// this an index chosen from the old list lands on whatever now sits
	// in that slot. Optional: a device that predates it is unguarded,
	// not refused.
	IfQueueRevision *int64
}

// Intent types.
const (
	IntentSetPlaying    = "setPlaying"
	IntentSetPositionMs = "setPositionMs"
	IntentSetIndex      = "setIndex"
	// Queue edits (EditQueue): carried out by the leader, not the server.
	IntentAddToQueue      = "addToQueue"
	IntentRemoveFromQueue = "removeFromQueue"
)

// QueueEdit is one change a device asked of the leader's queue. An add
// carries Items (metadata only, like the published queue) for after the
// playing song (Next) or the end; a removal names the entry by its index
// in the published queue AND its ItemID, as the device saw them.
type QueueEdit struct {
	Seq     int64       `json:"seq"`
	Type    string      `json:"type"`
	Items   []QueueItem `json:"items,omitempty"`
	Next    bool        `json:"next,omitempty"`
	Index   int         `json:"index"`
	ItemID  string      `json:"itemId,omitempty"`
	ByLabel string      `json:"byLabel,omitempty"`
	AtMs    int64       `json:"atMs"`
}

// QueueEditRequest is a device's ask; the handler has bounded every field.
type QueueEditRequest struct {
	Type  string
	Items []QueueItem
	Next  bool
	// Removal: the entry's index in the published queue, its ItemID, and
	// the queue revision both were read at.
	Index           int
	ItemID          string
	IfQueueRevision int64
}

// Queue-edit bounds. An add is at most an album or a page of a playlist;
// what waits on the leader at once is capped too, so a leader that stops
// acknowledging (asleep, crashed) cannot let the list grow. An edit the
// leader has not carried out after editTTL is dropped: by then it has
// gone, and the person has long since seen nothing happen.
const (
	MaxEditItems     = 100
	maxPendingItems  = 200
	maxPendingEdits  = 32
	editTTL          = 60 * time.Second
	MaxQueueFieldLen = 512
)

type presenceEntry struct {
	label   string
	role    string
	since   time.Time
	last    time.Time
	driftMs *int64
	driftAt time.Time
	// tokenID is the token the device follows with (NoteToken). A newer
	// device's key IS its token id; an Apple TV follows under a UUID it
	// minted, and a seat handed to that key must still name its token —
	// the identity its reports carry — or it never sees the seat is its.
	tokenID string
}

// MaxFollowersPerProfile caps how many devices one profile's party
// lists at once: followers, viewers and remotes together. A household is
// a handful of screens, each holding up to three entries (its player,
// its Home card, its Remote), so 64 turns no real device away, while one
// leaked token can no longer grow the list without end: entries expire
// after followerTTL, but only a read of a live session swept them out.
const MaxFollowersPerProfile = 64

// MaxDeviceKeyLen caps the key a device follows under. A TV's is a UUID
// with a short suffix (":viewer", ":remote"); a newer device's is its
// token id.
const MaxDeviceKeyLen = 128

// heldQueue is a queue refused while another device leads, kept for the
// device that sent it (Store.held).
type heldQueue struct {
	deviceID string
	items    []QueueItem
}

// withdrawnStart is a next-track announcement (StartItemID, StartAtMs)
// that an intent cancelled.
type withdrawnStart struct {
	itemID string
	atMs   int64
}

// withdrawnRefuseMs: how long past its T a cancelled announcement is
// refused when a leader sends it again. A next-track T is spent by
// then, and every follower has long moved on from it.
const withdrawnRefuseMs = 2000

// lapsed: the refusal window (until T + 2 s) is over.
func (w withdrawnStart) lapsed(nowMs int64) bool {
	return nowMs >= w.atMs+withdrawnRefuseMs
}

// refuses: a report re-sends exactly the cancelled (item, T) while the
// refusal stands. A different T for the same item is a NEW announcement
// (the leader re-armed after the change) and is judged on its own.
func (w withdrawnStart) refuses(itemID string, atMs, nowMs int64) bool {
	return !w.lapsed(nowMs) && itemID == w.itemID && atMs == w.atMs
}

// Store holds one state per profile.
type Store struct {
	mu   sync.RWMutex
	m    map[string]State
	rev  map[string]int64
	qrev map[string]int64
	// The leader's queue, alongside its state. Sent only when it
	// CHANGES (a station reshuffle, a smart-queue append) — heartbeats
	// stay tiny. Same lifetime rules as the state: memory only, gone
	// with the state.
	q map[string][]QueueItem
	// held[profile] are the queues carried by reports refused while
	// another device leads (seat-held, a legacy publisher ignored, a
	// take-over or claim refused as stale), one per device that sent one,
	// oldest first. None is the party's: each lands only when its own
	// device's report is accepted without a queue of its own. A TV counts
	// a queue as sent whatever the answer, so the take-over that follows
	// its refusal does not send it again; before this hold, the refused
	// report itself replaced the queue of the party another device was
	// leading. At most maxHeldQueues per profile, gone with the state.
	held map[string][]heldQueue
	// edits[profile] are the queue edits waiting on the leader, oldest
	// first; editSeq numbers them, seeded like the revisions so a seq
	// never goes backwards across a restart (a leader skips any seq it
	// has already carried out). Gone with the state.
	edits   map[string][]QueueEdit
	editSeq map[string]int64

	// anchor[profile] = when the current track started (server ms).
	anchor map[string]int64
	// anchorRefresh[profile] = a follower joined (or asked) since the
	// leader last reported: that report re-anchors unconditionally.
	anchorRefresh map[string]bool
	// withdrawn[profile] = the next-track announcement an intent from
	// the room cancelled. A leader that has not withdrawn it yet would
	// otherwise restore it with its very next report (it repeats the
	// fields on every one); the same (item, T) is refused until T + 2 s.
	withdrawn map[string]withdrawnStart
	// followers[profile][deviceKey] = presence, for the active/inactive
	// party distinction and the who-is-listening list.
	followers map[string]map[string]presenceEntry
	// lastActivity[profile] = the last time ANY device touched the
	// session (report, intent, follow). A vacant seat with followers
	// still heartbeating is a live party, not a ghost.
	lastActivity map[string]time.Time

	// waiters[profile] is closed to wake every long-poll waiting on
	// that profile. Polling has a floor — a follower cannot react
	// faster than its interval, which was two seconds of the leader
	// having already moved on. Holding the request open removes that
	// floor entirely: the answer arrives when the change does.
	waiters map[string]chan struct{}
	// waiting[profile] = how many long-polls are parked right now. A
	// held request is a goroutine and a worker slot, and a token holder
	// could open them without limit; past the cap a poll is answered
	// immediately instead of parked, which costs the surplus client its
	// zero-latency wake and costs the server nothing.
	waiting map[string]int

	// audio[profile] is the party's volume and what its devices can do
	// (party_audio.go). Memory only, never the database, disk or logs;
	// gone with the state, and with a stop-all.
	audio map[string]*partyAudio

	// bootMs seeds every revision counter, so revisions are monotonic
	// across restarts. A follower holding revision 57 across a restart
	// used to be told "nothing new" forever (counter reset to 0).
	bootMs int64
}

func NewStore() *Store {
	return &Store{
		m:             make(map[string]State),
		q:             make(map[string][]QueueItem),
		held:          make(map[string][]heldQueue),
		edits:         make(map[string][]QueueEdit),
		editSeq:       make(map[string]int64),
		rev:           make(map[string]int64),
		qrev:          make(map[string]int64),
		anchor:        make(map[string]int64),
		anchorRefresh: make(map[string]bool),
		withdrawn:     make(map[string]withdrawnStart),
		followers:     make(map[string]map[string]presenceEntry),
		lastActivity:  make(map[string]time.Time),
		waiters:       make(map[string]chan struct{}),
		waiting:       make(map[string]int),
		audio:         make(map[string]*partyAudio),
		bootMs:        time.Now().UnixMilli(),
	}
}

// nextRevLocked advances and returns the profile's revision. Caller
// holds the write lock.
//
// A revision is never less than the clock: it starts at the boot
// millisecond and each bump takes max(previous+1, now). Changes arrive
// seconds apart in practice, so a revision tracks wall time and a
// restart — which seeds from the NEW clock — can only ever move it
// forward. A plain counter seeded at boot drifted ahead of the clock
// by one per change and rewound on restart.
func (s *Store) nextRevLocked(profileID string) int64 {
	return bump(s.rev, profileID, s.bootMs)
}

func (s *Store) nextQueueRevLocked(profileID string) int64 {
	return bump(s.qrev, profileID, s.bootMs)
}

func bump(m map[string]int64, profileID string, seed int64) int64 {
	next := time.Now().UnixMilli()
	prev := m[profileID]
	if prev == 0 {
		prev = seed
	}
	if next <= prev {
		next = prev + 1
	}
	m[profileID] = next
	return next
}

// Set records what a profile is playing with no device identity — the
// legacy path, kept for callers (and tests) that predate the seat.
func (s *Store) Set(profileID string, st State) {
	s.Report(profileID, st, ReportOpts{})
}

// Report records what a device says it is playing, subject to the seat
// rules, and returns the resulting state (or, on refusal, the state the
// caller should catch up on).
func (s *Store) Report(profileID string, st State, opts ReportOpts) (State, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	legacy := opts.BasedOnRevision == nil

	s.mu.Lock()
	defer s.mu.Unlock()

	prev, had := s.m[profileID]
	if had && (prev.Ended || s.staleLocked(prev, profileID, now)) {
		// Expiry is lazy (Get) and this path never checks staleness, so
		// without this a party volume would carry into an unrelated
		// party started hours later. The session itself goes on as ever.
		s.dropPartyAudioLocked(profileID)
	}
	if had && prev.Ended {
		// An ended party is a tombstone, not a session: whoever reports
		// next starts fresh — seat, anchor and intent bookkeeping alike.
		had = false
		prev = State{}
	}
	// Whether this device holds the seat as stored, awake or not: the
	// only device whose queue may land with a refused (stale) report.
	holder := had && opts.DeviceID != "" && prev.LeaderTokenID == opts.DeviceID
	if had && !s.seatLiveLocked(prev, nowMs) {
		// A dormant or vacant seat protects nobody: whoever is actually
		// making noise now takes it.
		prev.LeaderTokenID = ""
	}
	otherHolds := had && prev.LeaderTokenID != "" && prev.LeaderTokenID != opts.DeviceID
	switch {
	case otherHolds && opts.TakeOver:
		// Explicit "play here instead": the person chose this device.
	case otherHolds && legacy:
		s.holdQueueLocked(profileID, opts)
		return s.readLocked(profileID, prev, nowMs), OutcomeIgnored
	case otherHolds:
		s.holdQueueLocked(profileID, opts)
		return s.readLocked(profileID, prev, nowMs), OutcomeSeatHeld
	}

	if had && !legacy && *opts.BasedOnRevision < prev.LastIntentRevision {
		// The reporter has not seen the last intent yet — a heartbeat
		// must not overwrite a remote pause, and a returning or claiming
		// device must catch up before it may lead. It gets the state.
		switch {
		case opts.Queue == nil:
		case holder:
			// The seat's own holder: its queue stands, as it always has.
			// Only the queue revision moves, so a follower still learns
			// the tracklist changed.
			prev.QueueRevision, prev.Revision = s.setQueueLocked(profileID, *opts.Queue)
			s.dropHeldLocked(profileID, opts.DeviceID)
		default:
			// A take-over or a claim that has not happened yet: held,
			// like a seat-held one, until this device's report is
			// accepted.
			s.holdQueueLocked(profileID, opts)
		}
		return s.readLocked(profileID, prev, nowMs), OutcomeStale
	}

	// Position and rate in one unit.
	posMs := int64(st.PositionSec) * 1000
	if opts.HasPositionMs {
		posMs = st.PositionMs
	}
	if posMs < 0 {
		posMs = 0
	}
	rate := st.Rate
	if rate <= 0 {
		rate = 1
	}
	st.PositionMs = posMs
	st.PositionSec = int(posMs / 1000)
	st.Rate = rate
	st.UpdatedAtMs = nowMs

	// Derive the track-start anchor. Keep the existing one while it
	// still explains where the leader says it is (within 2s); re-anchor
	// on a track change, a scrub, a pause, a RESUME, or the first report
	// after a remote intent — anything that breaks the "position
	// advances with the clock from this anchor" assumption. A resume
	// used to be missed: the paused-era anchor survived when the resume
	// landed inside the 2s window, and followers ran ahead all track.
	// The position was measured at sampledAt (server time, via the
	// reporter's clock offset) when the reporter can say so; otherwise
	// at arrival, which bakes the request's one-way latency into every
	// anchor.
	base := nowMs
	if opts.SampledAtMs > 0 && abs64(nowMs-opts.SampledAtMs) < 5000 {
		base = opts.SampledAtMs
	}
	implied := base - int64(float64(posMs)/rate)
	// A schedule needs an instant: a startAtMs of zero or below is no
	// schedule, and its two sibling fields go with it.
	if st.StartAtMs <= 0 {
		st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
	}
	// A NEXT-track announcement stands exactly as this report sends it.
	// The leader repeats it on every report of the current item, so a
	// report that omits it has WITHDRAWN it: nothing is carried forward
	// from the stored state. One that is sent is still refused here —
	// defence in depth; the leader withdraws on every one of these
	// itself — when the report says paused (a paused leader reaches no
	// boundary), when its T does not fit the trajectory this very
	// report describes (a restart, a scrub, a rate change it did not
	// withdraw for), or when it re-sends the (item, T) an intent from
	// the room cancelled, until T + 2 s. Refused, it is simply absent:
	// the report itself is applied as an ordinary one.
	if w, ok := s.withdrawn[profileID]; ok && w.lapsed(nowMs) {
		delete(s.withdrawn, profileID)
	}
	if isNextItemSchedule(st) {
		w, cancelled := s.withdrawn[profileID]
		if !st.IsPlaying ||
			(cancelled && w.refuses(st.StartItemID, st.StartAtMs, nowMs)) ||
			!nextStartFitsTrajectory(st.DurationSec, posMs, rate, base, st.StartAtMs) {
			st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
		}
	}
	// A scheduled start of the CURRENT item pins the anchor to the
	// announced instant: the item's zero is StartAtMs minus the start
	// position, whatever the reporter's position reads right now (it
	// is holding at the start position until T; or — the first report
	// of a track announced ahead — has just started at T; or, its
	// first rolling report, carries its measured audible zero). A
	// next-track announcement (above) leaves the current anchor alone.
	// A schedule more than 2 s in the past is spent: dropped, and the
	// report anchors as an ordinary one. "In the past" is judged at
	// base — the instant the reporter sampled its position when it can
	// say so, else arrival — so a slow first PUT of the announced track
	// still pins its anchor at T instead of forfeiting it to transit.
	// Without sampledAtServerMs base is the arrival time and nothing
	// differs.
	scheduledCurrent := false
	if st.StartAtMs > 0 {
		forCurrent := st.StartItemID == "" || st.StartItemID == st.ItemID
		switch {
		case forCurrent && st.StartAtMs > base-2000:
			scheduledCurrent = true
			if st.StartPositionMs < 0 {
				st.StartPositionMs = 0
			}
			implied = st.StartAtMs - int64(float64(st.StartPositionMs)/rate)
		case forCurrent:
			st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
		}
	}
	ack := had && !legacy && prev.LeaderAckRevision < prev.LastIntentRevision
	// A follower joined (or asked) since the last report: re-anchor
	// from THIS report even inside the keep-window. The anchor a track
	// starts with comes from the leader's first report, sent before its
	// own player has rolled, and the window then holds that for the
	// whole track — every follower launched on it sat a constant few
	// hundred milliseconds ahead until a scrub on the phone forced a
	// fresh one. A join now asks for exactly that, without the scrub.
	refresh := s.anchorRefresh[profileID]
	reanchored := true
	switch {
	case scheduledCurrent, !had, prev.ItemID != st.ItemID, !st.IsPlaying, !prev.IsPlaying && st.IsPlaying, ack, refresh:
		s.anchor[profileID] = implied
	default:
		if existing, ok := s.anchor[profileID]; !ok ||
			abs64(existing-implied) > 2000 {
			s.anchor[profileID] = implied
		} else {
			reanchored = false
		}
	}
	delete(s.anchorRefresh, profileID)
	st.TrackStartedAtMs = s.anchor[profileID]

	// Seat and bookkeeping the reporter cannot know.
	label := opts.DeviceLabel
	if label == "" {
		label = st.DeviceLabel
	}
	st.DeviceLabel = label
	st.LeaderTokenID = opts.DeviceID
	st.LeaderLabel = label
	st.LegacyLeader = legacy
	st.LeaderReportedAtMs = nowMs
	st.SeatVacantSinceMs = 0
	st.EndedByLeader = false
	// Queue edits never come from the body: the seat holder says only
	// whether it carries them out, and how far it has.
	st.QueueEdits = nil
	// Nor does party audio: a body that carries it changes nothing.
	clearReadTimeAudio(&st)
	st.LeaderEditsQueue = opts.EditsQueue
	if opts.QueueEditAck != nil {
		s.ackEditsLocked(profileID, *opts.QueueEditAck)
	}
	if had {
		st.IntentSeq = prev.IntentSeq
		st.LastIntent = prev.LastIntent
		st.LastIntentRevision = prev.LastIntentRevision
		st.LeaderAckRevision = prev.LeaderAckRevision
		st.ItemRevision = prev.ItemRevision
		if prev.ItemID == st.ItemID {
			st.HandedOverAtMs = prev.HandedOverAtMs
		}
		// Who gave the seat away stays while the device handed it
		// leads, so the old holder reads it whenever it next looks.
		if prev.LeaderTokenID == opts.DeviceID {
			st.HandedOverFrom = prev.HandedOverFrom
			st.HandedOverByLabel = prev.HandedOverByLabel
		}
	}

	// The queue lands with the report, in the same revision, so nobody
	// reads the new state beside the old tracklist: the one this report
	// carries, else the one this device was refused with while another
	// led (held), which it counts as sent.
	if opts.Queue != nil {
		s.replaceQueueLocked(profileID, *opts.Queue)
		s.dropHeldLocked(profileID, opts.DeviceID)
	} else if items, ok := s.takeHeldLocked(profileID, opts.DeviceID); ok {
		s.replaceQueueLocked(profileID, items)
	}

	rev := s.nextRevLocked(profileID)
	st.Revision = rev
	st.QueueRevision = s.qrevOrSeedLocked(profileID)
	if !had || prev.ItemID != st.ItemID {
		st.ItemRevision = rev
	}
	if ack {
		st.LeaderAckRevision = rev
	}
	if reanchored {
		st.AnchorRevision = rev
	} else {
		st.AnchorRevision = prev.AnchorRevision
	}
	s.m[profileID] = st
	s.lastActivity[profileID] = now
	if opts.Features != nil && opts.DeviceID != "" {
		// What this device can do, kept for its token: it reads as
		// leaderFeatures while it leads. The report bumps anyway.
		s.setFeaturesLocked(profileID, opts.DeviceID, opts.Features)
	}
	s.notifyLocked(profileID)
	return s.readLocked(profileID, st, nowMs), OutcomeOK
}

// staleLocked is Get's expiry rule: nothing — no report, intent or
// follow — has touched the session for staleAfter. Caller holds the lock.
func (s *Store) staleLocked(st State, profileID string, now time.Time) bool {
	last := time.UnixMilli(st.UpdatedAtMs)
	if a, has := s.lastActivity[profileID]; has && a.After(last) {
		last = a
	}
	return now.Sub(last) > staleAfter
}

func (s *Store) qrevOrSeedLocked(profileID string) int64 {
	if s.qrev[profileID] == 0 {
		s.qrev[profileID] = s.bootMs
	}
	return s.qrev[profileID]
}

// seatLiveLocked: the seat holder has reported recently enough to be
// trusted with it.
func (s *Store) seatLiveLocked(st State, nowMs int64) bool {
	if st.LeaderTokenID == "" || st.Ended {
		return false
	}
	return nowMs-st.LeaderReportedAtMs <= leaderLostAfter.Milliseconds()
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// isNextItemSchedule: the state names ANOTHER item to start at
// StartAtMs — the next track, announced ahead of the boundary.
func isNextItemSchedule(st State) bool {
	return st.StartAtMs > 0 && st.StartItemID != "" && st.StartItemID != st.ItemID
}

// The window a next-track T must fall in, measured from the boundary
// the announcing report itself implies. A legitimate T sits 700–2040 ms
// past the true boundary (the leader's time to audio, clamped to
// 100–2000 ms, plus a 40 ms margin, never under a 700 ms floor), and the
// true boundary sits up to a second (÷ rate) past the one computed from
// a whole-second durationSec.
// The early side allows for clock and sampling error. Outside it, the
// trajectory the T was predicted from is not the one being played.
const (
	nextStartEarliestMs = -1000
	nextStartLatestMs   = 4000
)

// nextStartOffsetMs is how far T sits past the end of the current item
// as the report describes it: the position sampled at base (server
// time) and the whole-second duration, at rate. Negative = T comes
// before that end.
func nextStartOffsetMs(durationSec int, posMs int64, rate float64, base, startAtMs int64) int64 {
	boundary := base + int64(float64(int64(durationSec)*1000-posMs)/rate)
	return startAtMs - boundary
}

// nextStartFitsTrajectory: a next-track T is consistent with the report
// that carries it — nextStartEarliestMs <= T − boundary <=
// nextStartLatestMs, both ends included. Without a duration there is no
// boundary to judge against, and the announcement stands.
func nextStartFitsTrajectory(durationSec int, posMs int64, rate float64, base, startAtMs int64) bool {
	if durationSec <= 0 {
		return true
	}
	if rate <= 0 {
		rate = 1
	}
	off := nextStartOffsetMs(durationSec, posMs, rate, base, startAtMs)
	return off >= nextStartEarliestMs && off <= nextStartLatestMs
}

// extrapolatedLocked is where the leader is NOW according to the anchor.
func (s *Store) extrapolatedLocked(profileID string, st State, nowMs int64) int64 {
	if !st.IsPlaying {
		return st.PositionMs
	}
	if st.StartAtMs > 0 && (st.StartItemID == "" || st.StartItemID == st.ItemID) && nowMs < st.StartAtMs {
		return st.StartPositionMs
	}
	anchor, ok := s.anchor[profileID]
	if !ok {
		return st.PositionMs
	}
	pos := int64(float64(nowMs-anchor) * st.Rate)
	if st.DurationSec > 0 && pos > int64(st.DurationSec)*1000 {
		pos = int64(st.DurationSec) * 1000
	}
	if pos < 0 {
		pos = 0
	}
	return pos
}

// EditQueue records a device's change to the party's queue for the leader
// to carry out: the leader owns its queue (a phone's holds tracks a
// joiner never sees, beyond the published window), so the server checks
// the ask against what it knows, wakes the leader, and keeps the edit
// until the leader acknowledges it. Everyone sees the result when the
// leader's next queue lands.
func (s *Store) EditQueue(profileID, byLabel string, in QueueEditRequest) (State, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.m[profileID]
	if !ok || st.Ended {
		return State{}, OutcomeNoSession
	}
	switch {
	case !s.seatLiveLocked(st, nowMs):
		// Nobody leads right now to carry it out.
		return s.readLocked(profileID, st, nowMs), OutcomeLeaderAsleep
	case st.LegacyLeader:
		return s.readLocked(profileID, st, nowMs), OutcomeLegacyLeader
	case !st.LeaderEditsQueue:
		return s.readLocked(profileID, st, nowMs), OutcomeLeaderCannotEdit
	case st.Source == "abs":
		// A book has no queue.
		return s.readLocked(profileID, st, nowMs), OutcomeNoQueue
	}
	s.sweepEditsLocked(profileID, nowMs)
	pending := s.edits[profileID]

	edit := QueueEdit{Type: in.Type, ByLabel: byLabel, AtMs: nowMs}
	switch in.Type {
	case IntentAddToQueue:
		waiting := 0
		for _, e := range pending {
			waiting += len(e.Items)
		}
		if len(in.Items) == 0 || len(in.Items) > MaxEditItems {
			return s.readLocked(profileID, st, nowMs), OutcomeStale
		}
		if len(pending) >= maxPendingEdits || waiting+len(in.Items) > maxPendingItems {
			return s.readLocked(profileID, st, nowMs), OutcomeBusy
		}
		edit.Items = append([]QueueItem(nil), in.Items...)
		edit.Next = in.Next

	case IntentRemoveFromQueue:
		items := s.q[profileID]
		if len(items) == 0 {
			return s.readLocked(profileID, st, nowMs), OutcomeNoQueue
		}
		// The entry the device saw, where it saw it — or nothing: a list
		// that moved since gets the new state, and the device looks again.
		if in.IfQueueRevision != st.QueueRevision ||
			in.Index < 0 || in.Index >= len(items) ||
			items[in.Index].ItemID != in.ItemID {
			return s.readLocked(profileID, st, nowMs), OutcomeStale
		}
		if in.Index == st.QueueIndex {
			return s.readLocked(profileID, st, nowMs), OutcomeIsCurrent
		}
		for _, e := range pending {
			if e.Type == IntentRemoveFromQueue && e.Index == in.Index && e.ItemID == in.ItemID {
				// Two rooms removing the same song: one removal.
				return s.readLocked(profileID, st, nowMs), OutcomeOK
			}
		}
		if len(pending) >= maxPendingEdits {
			return s.readLocked(profileID, st, nowMs), OutcomeBusy
		}
		edit.Index = in.Index
		edit.ItemID = in.ItemID

	default:
		return s.readLocked(profileID, st, nowMs), OutcomeStale
	}

	edit.Seq = bump(s.editSeq, profileID, s.bootMs)
	s.edits[profileID] = append(pending, edit)
	st.Revision = s.nextRevLocked(profileID)
	s.m[profileID] = st
	s.lastActivity[profileID] = now
	s.notifyLocked(profileID)
	return s.readLocked(profileID, st, nowMs), OutcomeOK
}

// ackEditsLocked drops every queue edit up to seq: the leader has carried
// them out. Caller holds the write lock.
func (s *Store) ackEditsLocked(profileID string, seq int64) {
	pending := s.edits[profileID]
	keep := pending[:0:0]
	for _, e := range pending {
		if e.Seq > seq {
			keep = append(keep, e)
		}
	}
	if len(keep) == 0 {
		delete(s.edits, profileID)
	} else {
		s.edits[profileID] = keep
	}
}

// sweepEditsLocked drops queue edits older than editTTL. Caller holds the
// write lock.
func (s *Store) sweepEditsLocked(profileID string, nowMs int64) {
	pending := s.edits[profileID]
	keep := pending[:0:0]
	for _, e := range pending {
		if nowMs-e.AtMs <= editTTL.Milliseconds() {
			keep = append(keep, e)
		}
	}
	if len(keep) == 0 {
		delete(s.edits, profileID)
	} else {
		s.edits[profileID] = keep
	}
}

// ApplyIntent mutates the session the way a device asked, if the seat
// allows it. Pure state: the leader learns of it through the same
// long-poll every follower uses and converges like everyone else.
func (s *Store) ApplyIntent(profileID, byLabel string, in IntentRequest) (State, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	st, ok := s.m[profileID]
	if !ok || st.Ended {
		// An ended party takes no intents: nothing is playing anywhere,
		// and "play" from a room must not resurrect it for the others.
		return State{}, OutcomeNoSession
	}
	seatLive := s.seatLiveLocked(st, nowMs)
	if seatLive && st.LegacyLeader {
		return s.readLocked(profileID, st, nowMs), OutcomeLegacyLeader
	}
	// Taken before the switch moves the item: the next-track
	// announcement standing when the intent lands, if any.
	hadNext := isNextItemSchedule(st)
	cancelled := withdrawnStart{itemID: st.StartItemID, atMs: st.StartAtMs}

	changed := false
	switch in.Type {
	case IntentSetPlaying:
		if st.IsPlaying == in.Playing {
			return s.readLocked(profileID, st, nowMs), OutcomeOK
		}
		if in.Playing && st.LeaderTokenID != "" && !seatLive {
			// Nobody can make a suspended phone play. Say so; the TV
			// can offer to play here instead.
			return s.readLocked(profileID, st, nowMs), OutcomeLeaderAsleep
		}
		pos := s.extrapolatedLocked(profileID, st, nowMs)
		st.IsPlaying = in.Playing
		st.PositionMs = pos
		st.PositionSec = int(pos / 1000)
		s.anchor[profileID] = nowMs - int64(float64(pos)/st.Rate)
		changed = true

	case IntentSetPositionMs:
		if in.BasedOnRevision < st.ItemRevision {
			return s.readLocked(profileID, st, nowMs), OutcomeStale
		}
		pos := in.PositionMs
		if pos < 0 {
			pos = 0
		}
		if st.DurationSec > 0 && pos > int64(st.DurationSec)*1000 {
			pos = int64(st.DurationSec) * 1000
		}
		st.PositionMs = pos
		st.PositionSec = int(pos / 1000)
		s.anchor[profileID] = nowMs - int64(float64(pos)/st.Rate)
		changed = true

	case IntentSetIndex:
		items := s.q[profileID]
		if len(items) == 0 {
			return s.readLocked(profileID, st, nowMs), OutcomeNoQueue
		}
		idx := in.Index
		if idx < 0 {
			idx = 0
		}
		if idx >= len(items) {
			idx = len(items) - 1
		}
		if idx == st.QueueIndex {
			return s.readLocked(profileID, st, nowMs), OutcomeOK
		}
		if in.BasedOnRevision < st.ItemRevision {
			// The track already moved since this device looked. Never
			// apply — that is how two rooms skip twice.
			return s.readLocked(profileID, st, nowMs), OutcomeStale
		}
		if in.IfQueueRevision != nil && *in.IfQueueRevision != st.QueueRevision {
			// The list this index points into is not the list the
			// device saw. The answer carries the new queue revision;
			// the device re-reads the queue and chooses again.
			return s.readLocked(profileID, st, nowMs), OutcomeStale
		}
		entry := items[idx]
		st.QueueIndex = idx
		st.Source = entry.Source
		st.ItemID = entry.ItemID
		st.Title = entry.Title
		st.Author = entry.Author
		st.DurationSec = entry.DurationSec
		st.CoverURL = ""
		st.PositionMs = 0
		st.PositionSec = 0
		s.anchor[profileID] = nowMs
		changed = true

	default:
		return s.readLocked(profileID, st, nowMs), OutcomeStale
	}

	if changed {
		rev := s.nextRevLocked(profileID)
		st.Revision = rev
		st.IntentSeq++
		st.LastIntentRevision = rev
		st.LastIntent = &Intent{
			Seq:      st.IntentSeq,
			Type:     in.Type,
			ByLabel:  byLabel,
			AtMs:     nowMs,
			Revision: rev,
		}
		if in.Type == IntentSetIndex {
			st.ItemRevision = rev
		}
		// Every intent above re-anchored.
		st.AnchorRevision = rev
		st.TrackStartedAtMs = s.anchor[profileID]
		st.UpdatedAtMs = nowMs
		// A press from the room overrides an announced start. A
		// next-track announcement it cancels is remembered: the leader
		// repeats the fields on every report until it withdraws, so its
		// acknowledging report could otherwise restore the very (item,
		// T) the room just cancelled. That pair is refused until T + 2 s.
		st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
		if hadNext {
			s.withdrawn[profileID] = cancelled
		}
		s.m[profileID] = st
		s.lastActivity[profileID] = now
		s.notifyLocked(profileID)
	}
	return s.readLocked(profileID, st, nowMs), OutcomeOK
}

// HandOver passes the seat to a live follower: the explicit "make this
// device the leader". The seat holder may ask, and so may any device
// taking part in the party — a follower (for itself or another), a
// Remote, a watching screen (owner, 2026-10-30: "promote any follower
// device to leader, from any party play connected device"). A device of
// the same profile that is in no party may not. The position stays
// continuous (the anchor is kept), the new holder's label goes on the
// seat, and the revision moves so the new leader's first report must
// be based on it — it reads the state before it leads. The old leader
// is not a follower here; its client joins the party on its own, told
// by HandedOverFrom when it did not ask. fromLabel names the asker for
// the old leader's notice.
func (s *Store) HandOver(profileID, fromDeviceID, fromLabel, toDeviceKey string) (State, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[profileID]
	if !ok || st.Ended {
		return State{}, OutcomeNoSession
	}
	if fromDeviceID == "" {
		return s.readLocked(profileID, st, nowMs), OutcomeNotInParty
	}
	holder := st.LeaderTokenID != "" && st.LeaderTokenID == fromDeviceID
	if !holder {
		label, member := s.memberLocked(profileID, fromDeviceID, now)
		if !member {
			return s.readLocked(profileID, st, nowMs), OutcomeNotInParty
		}
		if fromLabel == "" {
			fromLabel = label
		}
	}
	e, present := s.followers[profileID][toDeviceKey]
	if !present || now.Sub(e.last) > followerTTL || e.role != RoleFollower {
		return s.readLocked(profileID, st, nowMs), OutcomeNoSuchFollower
	}
	// The seat names the target's TOKEN — what its reports carry and what
	// `yourDeviceKey` tells it — not the UUID an Apple TV follows under;
	// handed to that UUID, a TV never saw the seat was its own.
	newHolder := e.tokenID
	if newHolder == "" {
		newHolder = toDeviceKey
	}
	if fromLabel == "" {
		fromLabel = st.LeaderLabel
	}
	st.HandedOverFrom = st.LeaderTokenID
	st.HandedOverByLabel = fromLabel
	pos := s.extrapolatedLocked(profileID, st, nowMs)
	st.PositionMs = pos
	st.PositionSec = int(pos / 1000)
	st.LeaderTokenID = newHolder
	st.LeaderLabel = e.label
	st.DeviceLabel = e.label
	st.LegacyLeader = false
	st.LeaderReportedAtMs = nowMs
	st.SeatVacantSinceMs = 0
	st.HandedOverAtMs = nowMs
	st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
	// The new leader is nobody's follower: left in place, it would be
	// counted among its own followers (and the party would look one
	// device bigger than it is) until its heartbeat aged out.
	delete(s.followers[profileID], toDeviceKey)
	rev := s.nextRevLocked(profileID)
	st.Revision = rev
	st.LastIntentRevision = rev
	st.UpdatedAtMs = nowMs
	s.m[profileID] = st
	s.lastActivity[profileID] = now
	s.notifyLocked(profileID)
	return s.readLocked(profileID, st, nowMs), OutcomeOK
}

// Following records that a device is actively following this profile
// (legacy signature: no label, follower role).
func (s *Store) Following(profileID, deviceKey string) {
	s.Join(profileID, deviceKey, "", RoleFollower)
}

// Join registers (or refreshes) a device's presence. A NEW member or a
// role change bumps the revision so the leader's long-poll learns of it
// at once; a heartbeat refresh must not, or the follower's own
// heartbeat-then-wait would never park.
//
// A new FOLLOWER also asks the leader for a fresh anchor (see
// AnchorRevision): it is about to launch on the anchor as it stands,
// and a first-report anchor is the leader's optimistic position.
// Returns the profile's revision after the join (0 without a session),
// so the joiner knows which anchor revisions postdate its arrival.
func (s *Store) Join(profileID, deviceKey, label, role string) int64 {
	rev, _ := s.JoinWithDrift(profileID, deviceKey, label, role, nil, 0)
	return rev
}

// JoinWithDrift is Join carrying the follower's latest drift reading
// (its clock drift: the party clock's target position minus its own
// player position, ms, no route latency; positive = the follower is
// BEHIND), measured at driftMeasuredAtMs server time. A heartbeat with
// a new reading does not bump the revision — the dashboard reads it on
// its next poll; waking every device for a number would be churn.
//
// ok is false, and nothing is recorded, when deviceKey is new and the
// profile already lists MaxFollowersPerProfile live devices.
func (s *Store) JoinWithDrift(profileID, deviceKey, label, role string, driftMs *int64, driftMeasuredAtMs int64) (rev int64, ok bool) {
	return s.JoinFollow(profileID, deviceKey, "", label, role, driftMs, driftMeasuredAtMs, nil)
}

// JoinFollow is JoinWithDrift from an authenticated device: tokenID is
// noted on the entry (as NoteToken does), and a FOLLOWER's features
// (already cleaned; nil = none sent, keep what is stored) are kept for
// its token while a session is live. A changed list bumps the revision
// once, so the Remote learns at once who can play an announcement; it
// never asks the leader for a fresh anchor, which only a join does.
func (s *Store) JoinFollow(profileID, deviceKey, tokenID, label, role string, driftMs *int64, driftMeasuredAtMs int64, features []string) (rev int64, ok bool) {
	if role != RoleViewer && role != RoleController {
		role = RoleFollower
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.followers[profileID] == nil {
		s.followers[profileID] = make(map[string]presenceEntry)
	}
	e, existed := s.followers[profileID][deviceKey]
	if existed && now.Sub(e.last) > followerTTL {
		existed = false
	}
	if !existed && len(s.followers[profileID]) >= MaxFollowersPerProfile {
		// Full: sweep out whoever has gone quiet, and turn the newcomer
		// away only if every place is still taken by a live device.
		for key, f := range s.followers[profileID] {
			if now.Sub(f.last) > followerTTL {
				delete(s.followers[profileID], key)
			}
		}
		if len(s.followers[profileID]) >= MaxFollowersPerProfile {
			return 0, false
		}
	}
	if !existed {
		e = presenceEntry{since: now}
	}
	changed := !existed || e.role != role
	e.label = label
	e.role = role
	e.last = now
	if tokenID != "" {
		e.tokenID = tokenID
	}
	if driftMs != nil {
		v := *driftMs
		e.driftMs = &v
		if driftMeasuredAtMs > 0 {
			e.driftAt = time.UnixMilli(driftMeasuredAtMs)
		} else {
			e.driftAt = now
		}
	}
	s.followers[profileID][deviceKey] = e
	s.lastActivity[profileID] = now
	st, has := s.m[profileID]
	if !has {
		return 0, true
	}
	featuresChanged := role == RoleFollower && features != nil && tokenID != "" && !st.Ended &&
		s.setFeaturesLocked(profileID, tokenID, features)
	if changed || featuresChanged {
		if changed && role == RoleFollower && !st.Ended {
			s.anchorRefresh[profileID] = true
		}
		s.nextRevLocked(profileID)
		s.notifyLocked(profileID)
	}
	return s.rev[profileID], true
}

// RequestAnchorRefresh: a follower about to launch again (the leader
// pulled it to another track, a rejoin) asks for a settled anchor
// without re-registering. The revision bump wakes the leader's poll,
// which reports at once; the answer is the revision the request
// landed at, so an anchor newer than it is the one asked for. 0 when
// nothing is playing.
func (s *Store) RequestAnchorRefresh(profileID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, has := s.m[profileID]
	if !has || st.Ended {
		return 0
	}
	s.anchorRefresh[profileID] = true
	s.nextRevLocked(profileID)
	s.notifyLocked(profileID)
	return s.rev[profileID]
}

// NoteToken records which token a presence entry belongs to: the
// authenticated device behind the key it follows under. Hand-overs read
// it — the seat goes to the target's TOKEN, and a device asking for one
// is in the party when any live entry is its. A no-op for an unknown key.
func (s *Store) NoteToken(profileID, deviceKey, tokenID string) {
	if tokenID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.followers[profileID][deviceKey]; ok && e.tokenID != tokenID {
		e.tokenID = tokenID
		s.followers[profileID][deviceKey] = e
	}
}

// memberLocked reports whether tokenID has a live presence in the
// profile's party, under its own key or a minted one, and its label.
func (s *Store) memberLocked(profileID, tokenID string, now time.Time) (string, bool) {
	for key, e := range s.followers[profileID] {
		if now.Sub(e.last) > followerTTL {
			continue
		}
		if key == tokenID || e.tokenID == tokenID {
			return e.label, true
		}
	}
	return "", false
}

// StopFollowing removes a device immediately, rather than waiting out
// its TTL — leaving a player should quiet the party at once.
func (s *Store) StopFollowing(profileID, deviceKey string) {
	s.mu.Lock()
	if _, existed := s.followers[profileID][deviceKey]; existed {
		delete(s.followers[profileID], deviceKey)
		if _, has := s.m[profileID]; has {
			s.nextRevLocked(profileID)
			s.notifyLocked(profileID)
		}
	}
	s.mu.Unlock()
}

// presenceLocked returns live followers, the viewer count (remotes
// included) and the remote count, pruning expired entries. Caller
// holds the write lock.
func (s *Store) presenceLocked(profileID string) (followers []Presence, viewers, controllers int) {
	a := s.audio[profileID]
	if st, ok := s.m[profileID]; ok && st.Ended {
		a = nil // never on an ended tombstone
	}
	for key, e := range s.followers[profileID] {
		if time.Since(e.last) > followerTTL {
			delete(s.followers[profileID], key)
			continue
		}
		if e.role == RoleViewer || e.role == RoleController {
			viewers++
			if e.role == RoleController {
				controllers++
			}
			continue
		}
		p := Presence{
			Label:     e.label,
			Role:      e.role,
			SinceMs:   e.since.UnixMilli(),
			DeviceKey: key,
		}
		if e.driftMs != nil {
			v := *e.driftMs
			p.DriftMs = &v
			p.DriftMeasuredAtMs = e.driftAt.UnixMilli()
		}
		// Volume and features are kept by token: an Apple TV follows
		// under a UUID it minted but leads under its token.
		tok := e.tokenID
		if tok == "" {
			tok = key
		}
		p.Volume = a.deviceVolume(tok)
		p.Features = a.featuresOf(tok)
		followers = append(followers, p)
	}
	return followers, viewers, controllers
}

// followerCountLocked keeps the old name for the tests and the
// read path. Caller holds the write lock.
func (s *Store) followerCountLocked(profileID string) int {
	f, _, _ := s.presenceLocked(profileID)
	return len(f)
}

// readLocked decorates a stored state with everything computed at read
// time. Caller holds the write lock.
func (s *Store) readLocked(profileID string, st State, nowMs int64) State {
	st.Followers, st.Viewers, st.Controllers = s.presenceLocked(profileID)
	st.FollowerCount = len(st.Followers)
	st.LeaderDormant = st.LeaderTokenID != "" && !s.seatLiveLocked(st, nowMs)
	st.AnchorRefreshPending = !st.Ended && s.anchorRefresh[profileID]
	st.QueueEdits = nil
	if !st.Ended {
		for _, e := range s.edits[profileID] {
			if nowMs-e.AtMs <= editTTL.Milliseconds() {
				st.QueueEdits = append(st.QueueEdits, e)
			}
		}
	}
	// Party audio is never present on an ended tombstone.
	clearReadTimeAudio(&st)
	if !st.Ended {
		s.decorateAudioLocked(profileID, &st, nowMs)
	}
	// The counter is the truth: presence changes bump it without
	// rewriting the stored state, and a reader must see that bump or
	// its next long-poll parks on a revision that has already passed.
	if r := s.rev[profileID]; r > st.Revision {
		st.Revision = r
	}
	return st
}

// notifyLocked wakes everything waiting on this profile. Caller holds
// the write lock.
func (s *Store) notifyLocked(profileID string) {
	if ch, ok := s.waiters[profileID]; ok {
		close(ch)
		delete(s.waiters, profileID)
	}
}

// MaxWaitersPerProfile is how many long-polls one profile may hold open
// at once. A household is a phone, a TV, a watch and a spare or two —
// and a reconnecting device can briefly double up while its old request
// times out — so 16 is generous for real use while keeping what one
// leaked token can pin down to a handful of goroutines.
const MaxWaitersPerProfile = 16

// WaitForChange blocks until this profile's revision passes sinceRev,
// the timeout expires, or ctx is done. Returns immediately when the
// revision already has — which is what makes a reconnecting follower
// catch up instantly instead of waiting out a cycle — and immediately
// when the profile already holds MaxWaitersPerProfile requests open,
// so a flood of polls degrades to plain polling rather than pinning
// goroutines.
//
// ctx is the request's: it ends the wait on server shutdown, so a
// graceful stop is not held for a full poll cycle. (Under fasthttp a
// client that hangs up is NOT reflected in ctx — the wait runs to its
// timeout and the write to the dead socket fails quietly. That is why
// the cap and the timeout both exist.)
func (s *Store) WaitForChange(ctx context.Context, profileID string, sinceRev int64, timeout time.Duration) {
	s.mu.Lock()
	if s.rev[profileID] > sinceRev || s.waiting[profileID] >= MaxWaitersPerProfile {
		s.mu.Unlock()
		return
	}
	ch, ok := s.waiters[profileID]
	if !ok {
		ch = make(chan struct{})
		s.waiters[profileID] = ch
	}
	s.waiting[profileID]++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		if s.waiting[profileID] <= 1 {
			delete(s.waiting, profileID)
		} else {
			s.waiting[profileID]--
		}
		s.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	case <-ctx.Done():
	}
}

// Waiting reports how many long-polls are parked on a profile. Test and
// diagnostics only.
func (s *Store) Waiting(profileID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.waiting[profileID]
}

// Get returns the profile's current state, or ok=false when nothing is
// playing or the last report has gone stale.
func (s *Store) Get(profileID string) (State, bool) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[profileID]
	if !ok {
		return State{}, false
	}
	last := time.UnixMilli(st.UpdatedAtMs)
	if a, has := s.lastActivity[profileID]; has && a.After(last) {
		last = a
	}
	if now.Sub(last) > staleAfter {
		// Expired: drop it so the map does not accumulate ghosts.
		delete(s.m, profileID)
		delete(s.q, profileID)
		delete(s.held, profileID)
		delete(s.edits, profileID)
		delete(s.anchorRefresh, profileID)
		delete(s.withdrawn, profileID)
		s.dropPartyAudioLocked(profileID)
		return State{}, false
	}
	if st.Ended {
		// The tombstone of a party the leader ended for everyone: read
		// as-is until every follower has had time to see it, then gone.
		if now.Sub(time.UnixMilli(st.EndedAtMs)) > endedTTL {
			delete(s.m, profileID)
			delete(s.q, profileID)
			delete(s.held, profileID)
			delete(s.edits, profileID)
			delete(s.lastActivity, profileID)
			delete(s.withdrawn, profileID)
			s.dropPartyAudioLocked(profileID)
			return State{}, false
		}
		return s.readLocked(profileID, st, nowMs), true
	}
	if st.LeaderTokenID != "" && now.Sub(time.UnixMilli(st.LeaderReportedAtMs)) > staleAfter {
		// The leader is long gone but followers keep the session alive:
		// vacate the seat so one of them can claim it.
		st.LeaderTokenID = ""
		st.LeaderLabel = ""
		st.SeatVacantSinceMs = nowMs
		st.Revision = s.nextRevLocked(profileID)
		s.m[profileID] = st
		s.notifyLocked(profileID)
	}
	return s.readLocked(profileID, st, nowMs), true
}

// Clear removes a profile's state — the explicit "stopped playing"
// (legacy signature: caller is treated as the leader).
func (s *Store) Clear(profileID string) {
	s.End(profileID, "", "")
}

// EndOutcome is what a DELETE actually did, for the caller's screen:
// "stopped everywhere" and "you merely left" must not look alike.
type EndOutcome int

const (
	// EndNoSession: nothing was playing.
	EndNoSession EndOutcome = iota
	// EndLeft: a member of the party left; the others play on.
	EndLeft
	// EndVacated: the leader left the seat; the party plays on and a
	// follower may claim it.
	EndVacated
	// EndDeleted: the advertisement is gone (sharing off, or nobody
	// was listening).
	EndDeleted
	// EndAll: the leader ended the party for every device.
	EndAll
)

func (o EndOutcome) String() string {
	switch o {
	case EndLeft:
		return "left"
	case EndVacated:
		return "vacated"
	case EndDeleted:
		return "deleted"
	case EndAll:
		return "ended-all"
	default:
		return "no-session"
	}
}

// End is a device saying it stopped (no label; see EndBy).
func (s *Store) End(profileID, deviceID, reason string) EndOutcome {
	return s.EndBy(profileID, deviceID, "", reason)
}

// EndBy is a device saying it stopped. From the leader with followers
// and reason "stopped", the seat is vacated and the state kept — the
// party keeps playing and a follower may claim it. Reason "stop-all"
// from the leader ends the party for EVERY device: the record stays
// for endedTTL, paused and marked Ended, so each follower wakes to a
// state that tells it to stop (a deletion would wake them with a 204,
// which a follower rightly reads as "leader quiet, keep playing").
// Any other leader reason ("stop-sharing", or nothing) deletes the
// session outright: the sharing toggle means "stop advertising NOW".
// From a member of the party that does not hold the seat — a TV that
// was following, whether or not the seat is still held — it is merely
// leaving: one room switching off must not silence the others.
func (s *Store) EndBy(profileID, deviceID, deviceLabel, reason string) EndOutcome {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[profileID]
	_, isMember := s.followers[profileID][deviceID]
	if ok && st.Ended {
		// A tombstone: there is nothing left to end. A member leaving
		// is noted; anyone's DELETE otherwise leaves it to expire, so a
		// follower that has not read it yet still will.
		if isMember {
			delete(s.followers[profileID], deviceID)
			s.nextRevLocked(profileID)
			s.notifyLocked(profileID)
		}
		if reason == "stop-all" {
			return EndAll
		}
		return EndLeft
	}
	holdsSeat := st.LeaderTokenID == deviceID
	if ok && deviceID != "" && !holdsSeat && (st.LeaderTokenID != "" || isMember) {
		if isMember {
			delete(s.followers[profileID], deviceID)
			s.nextRevLocked(profileID)
			s.notifyLocked(profileID)
		}
		return EndLeft
	}
	if ok && reason == "stop-all" {
		pos := s.extrapolatedLocked(profileID, st, now.UnixMilli())
		st.IsPlaying = false
		st.PositionMs = pos
		st.PositionSec = int(pos / 1000)
		// Nothing starts after the party ended: a follower that reads
		// startAtMs before ended must not find a schedule to act on.
		// (The position above was taken first, so a start still holding
		// at its start position stops there.)
		st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
		st.Ended = true
		st.EndedByLabel = deviceLabel
		if st.EndedByLabel == "" {
			st.EndedByLabel = st.LeaderLabel
		}
		st.EndedAtMs = now.UnixMilli()
		st.UpdatedAtMs = now.UnixMilli()
		st.Revision = s.nextRevLocked(profileID)
		s.m[profileID] = st
		delete(s.q, profileID)
		delete(s.held, profileID)
		delete(s.edits, profileID)
		delete(s.anchorRefresh, profileID)
		delete(s.withdrawn, profileID)
		// The tombstone carries no party audio: the volumes and what the
		// devices can do go now.
		s.dropPartyAudioLocked(profileID)
		s.lastActivity[profileID] = now
		s.notifyLocked(profileID)
		return EndAll
	}
	if ok && reason == "stopped" && s.followerCountLocked(profileID) > 0 {
		st.LeaderTokenID = ""
		st.LeaderLabel = ""
		st.SeatVacantSinceMs = now.UnixMilli()
		st.EndedByLeader = true
		// A next-track announcement is the leaving leader's word about
		// its own boundary: it goes with the seat, so nobody arriving
		// from here on arms on it (a follower that already armed keeps
		// its own latch). A start of the CURRENT item stays, as before.
		if isNextItemSchedule(st) {
			st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
		}
		st.Revision = s.nextRevLocked(profileID)
		s.m[profileID] = st
		s.lastActivity[profileID] = now
		s.notifyLocked(profileID)
		return EndVacated
	}
	delete(s.m, profileID)
	delete(s.q, profileID)
	delete(s.held, profileID)
	delete(s.edits, profileID)
	delete(s.lastActivity, profileID)
	delete(s.anchorRefresh, profileID)
	delete(s.withdrawn, profileID)
	s.dropPartyAudioLocked(profileID)
	// Revisions deliberately SURVIVE a clear: a follower that reconnects
	// must never see a revision go backwards, which it would read as
	// "nothing changed" and sit on stale playback.
	s.nextRevLocked(profileID)
	s.notifyLocked(profileID)
	if !ok {
		return EndNoSession
	}
	return EndDeleted
}

// SetQueue replaces the leader's queue for a profile. It bumps the
// revision too: a queue-only change must wake followers, or the one
// that had parked on the old revision learns of the reorder a whole
// heartbeat late.
func (s *Store) SetQueue(profileID string, items []QueueItem) {
	s.mu.Lock()
	s.setQueueLocked(profileID, items)
	s.mu.Unlock()
}

// setQueueLocked is SetQueue under the caller's lock. It returns the new
// queue revision, and the stored state's new revision (zero when no
// state is stored).
func (s *Store) setQueueLocked(profileID string, items []QueueItem) (qrev, rev int64) {
	qrev = s.replaceQueueLocked(profileID, items)
	// Keep a already-stored state's queue revision current, so a
	// follower polling between a queue change and the next heartbeat
	// still learns the tracklist moved.
	if st, ok := s.m[profileID]; ok {
		st.QueueRevision = qrev
		st.Revision = s.nextRevLocked(profileID)
		s.m[profileID] = st
		rev = st.Revision
	}
	s.notifyLocked(profileID)
	return qrev, rev
}

// replaceQueueLocked swaps the profile's queue (an empty one clears it)
// and returns the new queue revision. Caller holds the write lock.
func (s *Store) replaceQueueLocked(profileID string, items []QueueItem) int64 {
	qrev := s.nextQueueRevLocked(profileID)
	if len(items) == 0 {
		delete(s.q, profileID)
	} else {
		s.q[profileID] = items
	}
	return qrev
}

// maxHeldQueues caps the queues one profile holds (Store.held): one per
// device refused while another led, oldest dropped first. A household
// has a handful of screens that could be playing at once.
const maxHeldQueues = 4

// holdQueueLocked keeps the queue a refused report carried, for the
// device that sent it, in place of any it held before. Caller holds the
// write lock.
func (s *Store) holdQueueLocked(profileID string, opts ReportOpts) {
	if opts.Queue == nil || opts.DeviceID == "" {
		return
	}
	s.dropHeldLocked(profileID, opts.DeviceID)
	held := append(s.held[profileID], heldQueue{deviceID: opts.DeviceID, items: *opts.Queue})
	if len(held) > maxHeldQueues {
		// A fresh slice: the dropped queue must not stay reachable
		// behind the kept ones.
		held = append([]heldQueue(nil), held[len(held)-maxHeldQueues:]...)
	}
	s.held[profileID] = held
}

// takeHeldLocked removes and returns the queue held for deviceID. Caller
// holds the write lock.
func (s *Store) takeHeldLocked(profileID, deviceID string) ([]QueueItem, bool) {
	if deviceID == "" {
		return nil, false
	}
	for _, h := range s.held[profileID] {
		if h.deviceID == deviceID {
			s.dropHeldLocked(profileID, deviceID)
			return h.items, true
		}
	}
	return nil, false
}

// dropHeldLocked forgets the queue held for deviceID: a newer one from
// the same device has replaced it. Caller holds the write lock.
func (s *Store) dropHeldLocked(profileID, deviceID string) {
	held := s.held[profileID]
	for i, h := range held {
		if h.deviceID == deviceID {
			held = append(held[:i:i], held[i+1:]...)
			break
		}
	}
	if len(held) == 0 {
		delete(s.held, profileID)
	} else {
		s.held[profileID] = held
	}
}

// Queue returns the leader's queue — valid only while the state itself
// is fresh, so a dead session cannot hand out a stale tracklist.
func (s *Store) Queue(profileID string) ([]QueueItem, bool) {
	if _, ok := s.Get(profileID); !ok {
		return nil, false
	}
	s.mu.RLock()
	items, ok := s.q[profileID]
	s.mu.RUnlock()
	if !ok || len(items) == 0 {
		return nil, false
	}
	out := make([]QueueItem, len(items))
	copy(out, items)
	return out, true
}
