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

// staleAfter is how long a report stays credible without a heartbeat.
// The publisher heartbeats every few seconds while playing; a state
// older than this means the device died mid-song, and advertising a
// ghost session is worse than none.
const staleAfter = 90 * time.Second

// Store holds one state per profile.
type Store struct {
	mu sync.RWMutex
	m  map[string]State
}

func NewStore() *Store {
	return &Store{m: make(map[string]State)}
}

// Set records what a profile is playing. Last writer wins — the device
// actually making noise is the one reporting most recently.
func (s *Store) Set(profileID string, st State) {
	st.UpdatedAtMs = time.Now().UnixMilli()
	s.mu.Lock()
	s.m[profileID] = st
	s.mu.Unlock()
}

// Get returns the profile's current state, or ok=false when nothing is
// playing or the last report has gone stale.
func (s *Store) Get(profileID string) (State, bool) {
	s.mu.RLock()
	st, ok := s.m[profileID]
	s.mu.RUnlock()
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
	s.mu.Unlock()
}
