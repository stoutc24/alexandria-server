// Package nowplaying is the party line: each profile's devices can see
// what that profile is playing RIGHT NOW, so an Apple TV can join a
// book or an album mid-stream at the same position.
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
package nowplaying

import (
	"sync"
	"time"
)

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

	// TrackStartedAtMs is when the CURRENT track began, on the server's
	// clock — derived here, not reported. It is the anchor every device
	// should play from: position is simply (now − trackStartedAt), which
	// is continuous, drift-free, and identical on every follower,
	// instead of each one extrapolating from whatever sampled position
	// happened to be in the last heartbeat.
	//
	// Re-anchored only when the track changes or the leader's reported
	// position stops matching the anchor (a scrub, a restart). Ordinary
	// heartbeats leave it alone, so it does not wander.
	TrackStartedAtMs int64 `json:"trackStartedAtMs,omitempty"`

	// FollowerCount is how many OTHER devices are actively following
	// this profile right now. Zero means nobody joined: the party is
	// inactive and every device should just play locally. Non-zero
	// means the session is shared and worth keeping in step.
	FollowerCount int `json:"followerCount"`

	// Revision increments on every publish. A follower uses it to tell
	// "the leader said something new" from "same state, new heartbeat"
	// without diffing every field — and, more importantly, to notice it
	// MISSED updates (a jump of several revisions) so it can reconcile
	// hard instead of assuming continuity.
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

	// ServerNowMs is the server clock AT READ TIME, stamped by the GET
	// handler and never stored. It lets a joiner compute the report's
	// age entirely in server time — (serverNow − updatedAt) — and then
	// add only its own local elapsed-since-receipt, so the joiner's
	// wall clock never gets compared against the server's. Without it,
	// a few seconds of clock skew between a Windows server and an Apple
	// TV became a few seconds of playback lag (device report).
	ServerNowMs int64 `json:"serverNowMs,omitempty"`
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
// ghost session is worse than none.
const staleAfter = 90 * time.Second

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
	// followers[profile][deviceKey] = last heartbeat, for the
	// active/inactive party distinction.
	followers map[string]map[string]time.Time
}

func NewStore() *Store {
	return &Store{
		m:         make(map[string]State),
		q:         make(map[string][]QueueItem),
		rev:       make(map[string]int64),
		qrev:      make(map[string]int64),
		anchor:    make(map[string]int64),
		followers: make(map[string]map[string]time.Time),
	}
}

// Set records what a profile is playing. Last writer wins — the device
// actually making noise is the one reporting most recently.
func (s *Store) Set(profileID string, st State) {
	now := time.Now()
	st.UpdatedAtMs = now.UnixMilli()
	s.mu.Lock()

	// Derive the track-start anchor. Keep the existing one while it
	// still explains where the leader says it is (within 2s); re-anchor
	// on a track change, a scrub, or a pause — anything that breaks the
	// "position advances with the clock" assumption.
	implied := now.UnixMilli() - int64(st.PositionSec)*1000
	prev, hadPrev := s.m[profileID]
	switch {
	case !hadPrev, prev.ItemID != st.ItemID, !st.IsPlaying:
		s.anchor[profileID] = implied
	default:
		if existing, ok := s.anchor[profileID]; !ok ||
			abs64(existing-implied) > 2000 {
			s.anchor[profileID] = implied
		}
	}
	st.TrackStartedAtMs = s.anchor[profileID]

	s.rev[profileID]++
	st.Revision = s.rev[profileID]
	st.QueueRevision = s.qrev[profileID]
	s.m[profileID] = st
	s.mu.Unlock()
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// followerTTL: a follower that has not checked in this recently is
// assumed gone (app closed, TV asleep), so the party goes quiet on its
// own rather than pretending someone is still listening.
const followerTTL = 30 * time.Second

// Following records that a device is actively following this profile.
func (s *Store) Following(profileID, deviceKey string) {
	s.mu.Lock()
	if s.followers[profileID] == nil {
		s.followers[profileID] = make(map[string]time.Time)
	}
	s.followers[profileID][deviceKey] = time.Now()
	s.mu.Unlock()
}

// StopFollowing removes a device immediately, rather than waiting out
// its TTL — leaving a player should quiet the party at once.
func (s *Store) StopFollowing(profileID, deviceKey string) {
	s.mu.Lock()
	delete(s.followers[profileID], deviceKey)
	s.mu.Unlock()
}

// followerCount counts live followers, pruning expired ones. Caller
// holds the write lock.
func (s *Store) followerCountLocked(profileID string) int {
	live := 0
	for key, at := range s.followers[profileID] {
		if time.Since(at) > followerTTL {
			delete(s.followers[profileID], key)
			continue
		}
		live++
	}
	return live
}

// Get returns the profile's current state, or ok=false when nothing is
// playing or the last report has gone stale.
func (s *Store) Get(profileID string) (State, bool) {
	s.mu.Lock()
	st, ok := s.m[profileID]
	if ok {
		st.FollowerCount = s.followerCountLocked(profileID)
	}
	s.mu.Unlock()
	if !ok {
		return State{}, false
	}
	if time.Since(time.UnixMilli(st.UpdatedAtMs)) > staleAfter {
		// Expired: drop it so the map does not accumulate ghosts.
		s.mu.Lock()
		// Re-check under the write lock — a fresh Set may have raced in.
		if cur, still := s.m[profileID]; still && cur.UpdatedAtMs == st.UpdatedAtMs {
			delete(s.m, profileID)
		}
		s.mu.Unlock()
		return State{}, false
	}
	return st, true
}

// Clear removes a profile's state — the explicit "stopped playing".
func (s *Store) Clear(profileID string) {
	s.mu.Lock()
	delete(s.m, profileID)
	delete(s.q, profileID)
	// Revisions deliberately SURVIVE a clear: a follower that reconnects
	// must never see a revision go backwards, which it would read as
	// "nothing changed" and sit on stale playback.
	s.mu.Unlock()
}

// SetQueue replaces the leader's queue for a profile.
func (s *Store) SetQueue(profileID string, items []QueueItem) {
	s.mu.Lock()
	s.qrev[profileID]++
	if len(items) == 0 {
		delete(s.q, profileID)
	} else {
		s.q[profileID] = items
	}
	// Keep a already-stored state's queue revision current, so a
	// follower polling between a queue change and the next heartbeat
	// still learns the tracklist moved.
	if st, ok := s.m[profileID]; ok {
		st.QueueRevision = s.qrev[profileID]
		s.m[profileID] = st
	}
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
