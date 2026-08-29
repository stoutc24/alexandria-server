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

// The queue is only as alive as the session: a joiner must never get a
// tracklist for a session that has gone stale or been cleared.
func TestQueueLifetimeIsTiedToTheState(t *testing.T) {
	s := NewStore()

	s.SetQueue("p1", []QueueItem{{Source: "plex", ItemID: "plex:1", Title: "A"}})
	if _, ok := s.Queue("p1"); ok {
		t.Fatal("a queue with no live state was handed out")
	}

	s.Set("p1", State{Title: "A", IsPlaying: true})
	items, ok := s.Queue("p1")
	if !ok || len(items) != 1 {
		t.Fatalf("queue missing alongside a live state: ok=%v n=%d", ok, len(items))
	}

	s.Clear("p1")
	if _, ok := s.Queue("p1"); ok {
		t.Fatal("Clear left the queue behind")
	}
}

// Heartbeats without a queue attached must not clear the stored queue —
// the queue travels only when it changes.
func TestHeartbeatsLeaveTheQueueAlone(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{Title: "A", IsPlaying: true})
	s.SetQueue("p1", []QueueItem{{Source: "plex", ItemID: "plex:1", Title: "A"}})

	// Ten heartbeats later...
	for i := 0; i < 10; i++ {
		s.Set("p1", State{Title: "A", IsPlaying: true, PositionSec: i * 20})
	}
	if _, ok := s.Queue("p1"); !ok {
		t.Fatal("heartbeats without a queue wiped the stored queue")
	}

	// And an explicit empty replace clears it.
	s.SetQueue("p1", nil)
	if _, ok := s.Queue("p1"); ok {
		t.Fatal("an empty queue replace did not clear")
	}
}

func TestQueuesAreIsolatedPerProfile(t *testing.T) {
	s := NewStore()
	s.Set("sarah", State{Title: "A", IsPlaying: true})
	s.SetQueue("sarah", []QueueItem{{Source: "plex", ItemID: "plex:1", Title: "A"}})

	s.Set("chris", State{Title: "B", IsPlaying: true})
	if _, ok := s.Queue("chris"); ok {
		t.Fatal("chris got sarah's queue")
	}
}

// A follower distinguishes "the leader did something" from "another
// heartbeat" by the revision, and notices MISSED updates by a jump.
func TestRevisionsAdvanceAndSurviveClear(t *testing.T) {
	s := NewStore()

	s.Set("p1", State{Title: "A", IsPlaying: true})
	first, _ := s.Get("p1")
	s.Set("p1", State{Title: "A", IsPlaying: true})
	second, _ := s.Get("p1")
	if second.Revision <= first.Revision {
		t.Fatalf("revision did not advance: %d -> %d",
			first.Revision, second.Revision)
	}

	// A stop-and-restart must never rewind the revision: a follower
	// would read that as "nothing new" and sit on stale playback.
	s.Clear("p1")
	s.Set("p1", State{Title: "B", IsPlaying: true})
	third, _ := s.Get("p1")
	if third.Revision <= second.Revision {
		t.Errorf("revision rewound across a clear: %d then %d",
			second.Revision, third.Revision)
	}
}

// Shuffling changes the tracklist but not necessarily the current
// track — the queue revision is what tells a follower to refetch.
func TestQueueRevisionMovesOnlyOnQueueChange(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{Title: "A", IsPlaying: true})
	s.SetQueue("p1", []QueueItem{{ItemID: "1"}, {ItemID: "2"}})

	afterQueue, _ := s.Get("p1")
	if afterQueue.QueueRevision == 0 {
		t.Fatal("queue revision did not move when the queue was set")
	}

	// Heartbeats must not move it.
	s.Set("p1", State{Title: "A", IsPlaying: true, PositionSec: 30})
	afterBeat, _ := s.Get("p1")
	if afterBeat.QueueRevision != afterQueue.QueueRevision {
		t.Errorf("a heartbeat moved the queue revision: %d -> %d",
			afterQueue.QueueRevision, afterBeat.QueueRevision)
	}

	// A reshuffle must.
	s.SetQueue("p1", []QueueItem{{ItemID: "2"}, {ItemID: "1"}})
	afterShuffle, _ := s.Get("p1")
	if afterShuffle.QueueRevision <= afterBeat.QueueRevision {
		t.Errorf("a reshuffle did not move the queue revision")
	}
}

// The anchor is what every device plays from, so it must be STABLE
// while a track plays and must move when the leader actually moves.
func TestTrackAnchorHoldsThenReanchors(t *testing.T) {
	s := NewStore()

	s.Set("p1", State{ItemID: "t1", PositionSec: 10, IsPlaying: true})
	first, _ := s.Get("p1")
	if first.TrackStartedAtMs == 0 {
		t.Fatal("no anchor derived")
	}

	// An ordinary heartbeat a moment later must NOT move it — a
	// wandering anchor is a wandering playback position on every
	// follower at once.
	s.Set("p1", State{ItemID: "t1", PositionSec: 10, IsPlaying: true})
	second, _ := s.Get("p1")
	if second.TrackStartedAtMs != first.TrackStartedAtMs {
		t.Errorf("anchor drifted on a heartbeat: %d -> %d",
			first.TrackStartedAtMs, second.TrackStartedAtMs)
	}

	// A scrub (position jumps far from what the anchor implies) must
	// re-anchor, or followers would keep playing the old spot.
	s.Set("p1", State{ItemID: "t1", PositionSec: 120, IsPlaying: true})
	scrubbed, _ := s.Get("p1")
	if scrubbed.TrackStartedAtMs == first.TrackStartedAtMs {
		t.Error("a scrub did not re-anchor")
	}

	// A new track always re-anchors.
	s.Set("p1", State{ItemID: "t2", PositionSec: 0, IsPlaying: true})
	next, _ := s.Get("p1")
	if next.TrackStartedAtMs == scrubbed.TrackStartedAtMs {
		t.Error("a track change did not re-anchor")
	}
}

// Party active/inactive: with nobody following, a leader is just a
// device playing music.
func TestFollowerCountGatesTheParty(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})

	alone, _ := s.Get("p1")
	if alone.FollowerCount != 0 {
		t.Fatalf("FollowerCount = %d with nobody following", alone.FollowerCount)
	}

	s.Following("p1", "appletv-livingroom")
	s.Following("p1", "appletv-bedroom")
	shared, _ := s.Get("p1")
	if shared.FollowerCount != 2 {
		t.Errorf("FollowerCount = %d, want 2", shared.FollowerCount)
	}

	// Leaving is immediate, not TTL-delayed.
	s.StopFollowing("p1", "appletv-bedroom")
	after, _ := s.Get("p1")
	if after.FollowerCount != 1 {
		t.Errorf("FollowerCount = %d after one left, want 1", after.FollowerCount)
	}

	// And one profile's followers are not another's.
	s.Set("p2", State{ItemID: "x", IsPlaying: true})
	other, _ := s.Get("p2")
	if other.FollowerCount != 0 {
		t.Errorf("follower leaked across profiles: %d", other.FollowerCount)
	}
}
