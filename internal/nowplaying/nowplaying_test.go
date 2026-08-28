package nowplaying

import (
	"testing"
	"time"
)

func TestRoundTripAndClear(t *testing.T) {
	s := NewStore()

	if _, ok := s.Get("p1"); ok {
		t.Fatal("empty store returned a state")
	}

	s.Set("p1", State{Source: "plex", Title: "The Man", PositionSec: 42, IsPlaying: true})
	st, ok := s.Get("p1")
	if !ok || st.Title != "The Man" || st.PositionSec != 42 {
		t.Fatalf("round trip lost data: %+v ok=%v", st, ok)
	}
	if st.UpdatedAtMs == 0 {
		t.Fatal("server did not stamp the report time")
	}

	s.Clear("p1")
	if _, ok := s.Get("p1"); ok {
		t.Fatal("cleared state still readable")
	}
}

// The whole point of per-profile presence: one person's devices see
// their session, another person's see nothing.
func TestProfilesAreIsolated(t *testing.T) {
	s := NewStore()
	s.Set("sarah", State{Title: "Unsouled", IsPlaying: true})

	if _, ok := s.Get("chris"); ok {
		t.Fatal("chris can see sarah's session")
	}
	if st, ok := s.Get("sarah"); !ok || st.Title != "Unsouled" {
		t.Fatal("sarah cannot see her own session")
	}
}

// A device that died mid-song must stop advertising: a ghost session a
// TV joins into silence is worse than no session.
func TestStaleReportsExpire(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{Title: "Ghost", IsPlaying: true})

	// Age the report past the heartbeat tolerance.
	s.mu.Lock()
	st := s.m["p1"]
	st.UpdatedAtMs = time.Now().Add(-2 * time.Minute).UnixMilli()
	s.m["p1"] = st
	s.mu.Unlock()

	if _, ok := s.Get("p1"); ok {
		t.Fatal("a two-minute-old report is still advertised")
	}
	// And the ghost is actually gone, not just hidden.
	s.mu.RLock()
	_, still := s.m["p1"]
	s.mu.RUnlock()
	if still {
		t.Fatal("expired state was hidden but not deleted")
	}
}

func TestLastWriterWins(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{Title: "From the phone", DeviceLabel: "iPhone"})
	s.Set("p1", State{Title: "From the TV", DeviceLabel: "Apple TV"})
	st, _ := s.Get("p1")
	if st.DeviceLabel != "Apple TV" {
		t.Fatalf("expected the most recent reporter, got %+v", st)
	}
}
