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
	// DriftMs is what the follower last measured: its audible position
	// minus the leader's extrapolated one, after its own output offset.
	// Positive = ahead. Nil until it has reported one.
	DriftMs           *int64 `json:"driftMs,omitempty"`
	DriftMeasuredAtMs int64  `json:"driftMeasuredAtMs,omitempty"`
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
	// request. Both zero when no queue was published.
	QueueIndex int `json:"queueIndex,omitempty"`
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
	// the same moment instead of starting when it can and settling. A
	// StartItemID other than the current item is the NEXT track
	// announced ahead (followers preload it); the current item is
	// anchored as usual. Cleared by the next report that carries none
	// and by any intent. Clients that predate it see position +
	// isPlaying and settle as before, at worst a lead time early.
	StartAtMs       int64  `json:"startAtMs,omitempty"`
	StartPositionMs int64  `json:"startPositionMs,omitempty"`
	StartItemID     string `json:"startItemId,omitempty"`
	// HandedOverAtMs: the seat was passed by its holder to a follower
	// (POST /handover) at this server time. The new holder's first
	// report must be based on at least this revision.
	HandedOverAtMs int64 `json:"handedOverAtMs,omitempty"`
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
)

type presenceEntry struct {
	label   string
	role    string
	since   time.Time
	last    time.Time
	driftMs *int64
	driftAt time.Time
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

	// anchor[profile] = when the current track started (server ms).
	anchor map[string]int64
	// anchorRefresh[profile] = a follower joined (or asked) since the
	// leader last reported: that report re-anchors unconditionally.
	anchorRefresh map[string]bool
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

	// bootMs seeds every revision counter, so revisions are monotonic
	// across restarts. A follower holding revision 57 across a restart
	// used to be told "nothing new" forever (counter reset to 0).
	bootMs int64
}

func NewStore() *Store {
	return &Store{
		m:             make(map[string]State),
		q:             make(map[string][]QueueItem),
		rev:           make(map[string]int64),
		qrev:          make(map[string]int64),
		anchor:        make(map[string]int64),
		anchorRefresh: make(map[string]bool),
		followers:     make(map[string]map[string]presenceEntry),
		lastActivity:  make(map[string]time.Time),
		waiters:       make(map[string]chan struct{}),
		waiting:       make(map[string]int),
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
	if had && prev.Ended {
		// An ended party is a tombstone, not a session: whoever reports
		// next starts fresh — seat, anchor and intent bookkeeping alike.
		had = false
		prev = State{}
	}
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
		return s.readLocked(profileID, prev, nowMs), OutcomeIgnored
	case otherHolds:
		return s.readLocked(profileID, prev, nowMs), OutcomeSeatHeld
	}

	if had && !legacy && *opts.BasedOnRevision < prev.LastIntentRevision {
		// The reporter has not seen the last intent yet — a heartbeat
		// must not overwrite a remote pause, and a returning or claiming
		// device must catch up before it may lead. It gets the state.
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
	// A scheduled start of the CURRENT item pins the anchor to the
	// announced instant: the item's zero is StartAtMs minus the start
	// position, whatever the reporter's position reads right now (it
	// is holding at the start position until T). An announcement for
	// another item (the next track) rides along untouched. A schedule
	// more than 2 s in the past is spent: dropped, and the report
	// anchors as an ordinary one.
	scheduledCurrent := false
	if st.StartAtMs > 0 {
		forCurrent := st.StartItemID == "" || st.StartItemID == st.ItemID
		switch {
		case forCurrent && st.StartAtMs > nowMs-2000:
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
	if had {
		st.IntentSeq = prev.IntentSeq
		st.LastIntent = prev.LastIntent
		st.LastIntentRevision = prev.LastIntentRevision
		st.LeaderAckRevision = prev.LeaderAckRevision
		st.ItemRevision = prev.ItemRevision
		if prev.ItemID == st.ItemID {
			st.HandedOverAtMs = prev.HandedOverAtMs
		}
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
	s.notifyLocked(profileID)
	return s.readLocked(profileID, st, nowMs), OutcomeOK
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
		// A press from the room overrides an announced start.
		st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
		s.m[profileID] = st
		s.lastActivity[profileID] = now
		s.notifyLocked(profileID)
	}
	return s.readLocked(profileID, st, nowMs), OutcomeOK
}

// HandOver passes the seat from its holder to a live follower: the
// explicit "make this device the leader". The position stays
// continuous (the anchor is kept), the new holder's label goes on the
// seat, and the revision moves so the new leader's first report must
// be based on it — it reads the state before it leads. The old leader
// is not a follower here; its client joins the party on its own.
func (s *Store) HandOver(profileID, fromDeviceID, toDeviceKey string) (State, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[profileID]
	if !ok || st.Ended {
		return State{}, OutcomeNoSession
	}
	if st.LeaderTokenID == "" || st.LeaderTokenID != fromDeviceID {
		return s.readLocked(profileID, st, nowMs), OutcomeNotLeader
	}
	e, present := s.followers[profileID][toDeviceKey]
	if !present || now.Sub(e.last) > followerTTL || e.role != RoleFollower {
		return s.readLocked(profileID, st, nowMs), OutcomeNoSuchFollower
	}
	pos := s.extrapolatedLocked(profileID, st, nowMs)
	st.PositionMs = pos
	st.PositionSec = int(pos / 1000)
	st.LeaderTokenID = toDeviceKey
	st.LeaderLabel = e.label
	st.DeviceLabel = e.label
	st.LegacyLeader = false
	st.LeaderReportedAtMs = nowMs
	st.SeatVacantSinceMs = 0
	st.HandedOverAtMs = nowMs
	st.StartAtMs, st.StartPositionMs, st.StartItemID = 0, 0, ""
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
	return s.JoinWithDrift(profileID, deviceKey, label, role, nil, 0)
}

// JoinWithDrift is Join carrying the follower's latest drift reading
// (its audible position minus the leader's, ms, positive = ahead),
// measured at driftMeasuredAtMs server time. A heartbeat with a new
// reading does not bump the revision — the dashboard reads it on its
// next poll; waking every device for a number would be churn.
func (s *Store) JoinWithDrift(profileID, deviceKey, label, role string, driftMs *int64, driftMeasuredAtMs int64) int64 {
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
	if !existed {
		e = presenceEntry{since: now}
	}
	changed := !existed || e.role != role
	e.label = label
	e.role = role
	e.last = now
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
		return 0
	}
	if changed {
		if role == RoleFollower && !st.Ended {
			s.anchorRefresh[profileID] = true
		}
		s.nextRevLocked(profileID)
		s.notifyLocked(profileID)
	}
	return s.rev[profileID]
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
		delete(s.anchorRefresh, profileID)
		return State{}, false
	}
	if st.Ended {
		// The tombstone of a party the leader ended for everyone: read
		// as-is until every follower has had time to see it, then gone.
		if now.Sub(time.UnixMilli(st.EndedAtMs)) > endedTTL {
			delete(s.m, profileID)
			delete(s.q, profileID)
			delete(s.lastActivity, profileID)
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
		delete(s.anchorRefresh, profileID)
		s.lastActivity[profileID] = now
		s.notifyLocked(profileID)
		return EndAll
	}
	if ok && reason == "stopped" && s.followerCountLocked(profileID) > 0 {
		st.LeaderTokenID = ""
		st.LeaderLabel = ""
		st.SeatVacantSinceMs = now.UnixMilli()
		st.EndedByLeader = true
		st.Revision = s.nextRevLocked(profileID)
		s.m[profileID] = st
		s.lastActivity[profileID] = now
		s.notifyLocked(profileID)
		return EndVacated
	}
	delete(s.m, profileID)
	delete(s.q, profileID)
	delete(s.lastActivity, profileID)
	delete(s.anchorRefresh, profileID)
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
	qrev := s.nextQueueRevLocked(profileID)
	if len(items) == 0 {
		delete(s.q, profileID)
	} else {
		s.q[profileID] = items
	}
	// Keep a already-stored state's queue revision current, so a
	// follower polling between a queue change and the next heartbeat
	// still learns the tracklist moved.
	if st, ok := s.m[profileID]; ok {
		st.QueueRevision = qrev
		st.Revision = s.nextRevLocked(profileID)
		s.m[profileID] = st
	}
	s.notifyLocked(profileID)
	s.mu.Unlock()
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
