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
	s.lastActivity["p1"] = time.Now().Add(-2 * time.Minute)
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

// Long polling is what removes the reaction floor: a follower must hear
// about a change when it happens, not up to a poll interval later.
func TestWaitForChangeWakesOnPublish(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	start, _ := s.Get("p1")

	woke := make(chan time.Duration, 1)
	go func() {
		began := time.Now()
		s.WaitForChange("p1", start.Revision, 5*time.Second)
		woke <- time.Since(began)
	}()

	// Let the waiter park, then publish.
	time.Sleep(50 * time.Millisecond)
	s.Set("p1", State{ItemID: "t2", IsPlaying: true})

	select {
	case took := <-woke:
		if took > time.Second {
			t.Errorf("waiter took %v to notice a publish", took)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never woke on a publish")
	}
}

// A follower that is BEHIND must not park at all — it has catching up
// to do, and parking would delay it by a whole timeout.
func TestWaitForChangeReturnsImmediatelyWhenBehind(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	s.Set("p1", State{ItemID: "t2", IsPlaying: true})

	began := time.Now()
	s.WaitForChange("p1", 0, 5*time.Second) // asking from revision 0
	if elapsed := time.Since(began); elapsed > 200*time.Millisecond {
		t.Errorf("a behind follower parked for %v", elapsed)
	}
}

// Nothing to report is a normal outcome: the wait expires quietly.
func TestWaitForChangeTimesOutQuietly(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	cur, _ := s.Get("p1")

	began := time.Now()
	s.WaitForChange("p1", cur.Revision, 150*time.Millisecond)
	elapsed := time.Since(began)
	if elapsed < 100*time.Millisecond {
		t.Errorf("returned too early: %v", elapsed)
	}
	if elapsed > time.Second {
		t.Errorf("overshot the timeout: %v", elapsed)
	}
}

// Several followers park on one profile; a single publish must wake
// them all, not just whichever arrived first.
func TestOnePublishWakesEveryWaiter(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	cur, _ := s.Get("p1")

	const followers = 4
	done := make(chan struct{}, followers)
	for i := 0; i < followers; i++ {
		go func() {
			s.WaitForChange("p1", cur.Revision, 3*time.Second)
			done <- struct{}{}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	s.Set("p1", State{ItemID: "t2", IsPlaying: true})

	for i := 0; i < followers; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d waiters woke", i, followers)
		}
	}
}

// ---- conductor protocol ------------------------------------------------

func rev(v int64) *int64 { return &v }

// The seat: the device actually making noise owns the clock. A second
// device reporting while the seat is held is refused, never flapped.
func TestSeatIsHeldByTheFirstLiveReporter(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", DeviceLabel: "iPhone", BasedOnRevision: rev(0)}
	st, out := s.Report("p1", State{ItemID: "t1", IsPlaying: true}, phone)
	if out != OutcomeOK || st.LeaderTokenID != "tok-phone" || st.LeaderLabel != "iPhone" {
		t.Fatalf("first reporter did not take the seat: %v %+v", out, st)
	}

	tv := ReportOpts{DeviceID: "tok-tv", DeviceLabel: "Apple TV", BasedOnRevision: rev(st.Revision)}
	got, out := s.Report("p1", State{ItemID: "t9", IsPlaying: true}, tv)
	if out != OutcomeSeatHeld {
		t.Fatalf("second device took a held seat: %v", out)
	}
	if got.ItemID != "t1" || got.LeaderLabel != "iPhone" {
		t.Fatalf("refusal did not return the leader's state: %+v", got)
	}

	// A legacy publisher (no basedOnRevision) is swallowed, not errored.
	if _, out := s.Report("p1", State{ItemID: "t9"}, ReportOpts{DeviceID: "tok-old"}); out != OutcomeIgnored {
		t.Fatalf("legacy second publisher was not ignored: %v", out)
	}

	// Explicit take-over wins.
	tv.TakeOver = true
	if got, out := s.Report("p1", State{ItemID: "t9", IsPlaying: true}, tv); out != OutcomeOK || got.LeaderTokenID != "tok-tv" {
		t.Fatalf("take-over refused: %v %+v", out, got)
	}
}

// A dormant leader protects nothing; a claim on it succeeds.
func TestDormantSeatCanBeClaimed(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)}
	st, _ := s.Report("p1", State{ItemID: "t1", IsPlaying: true}, phone)

	s.mu.Lock()
	cur := s.m["p1"]
	cur.LeaderReportedAtMs = time.Now().Add(-45 * time.Second).UnixMilli()
	s.m["p1"] = cur
	s.mu.Unlock()

	read, _ := s.Get("p1")
	if !read.LeaderDormant {
		t.Fatal("a 45s-silent leader is not reported dormant")
	}
	tv := ReportOpts{DeviceID: "tok-tv", BasedOnRevision: rev(read.Revision), ClaimSeat: true}
	got, out := s.Report("p1", State{ItemID: "t1", IsPlaying: true}, tv)
	if out != OutcomeOK || got.LeaderTokenID != "tok-tv" {
		t.Fatalf("claim on a dormant seat refused: %v %+v (was rev %d)", out, got, st.Revision)
	}
}

// A remote pause must survive the leader's next heartbeat: the report
// based on a pre-intent revision is refused with the state to apply,
// and the first report that has seen it re-anchors from the leader.
func TestIntentBeatsAStaleHeartbeatAndLeaderAcks(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), HasPositionMs: true}
	st, _ := s.Report("p1", State{ItemID: "t1", IsPlaying: true, PositionMs: 10_000, DurationSec: 300}, phone)

	paused, out := s.ApplyIntent("p1", "Living Room TV", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: st.Revision})
	if out != OutcomeOK || paused.IsPlaying || paused.LastIntent == nil || paused.LastIntent.ByLabel != "Living Room TV" {
		t.Fatalf("pause intent not applied: %v %+v", out, paused)
	}
	if paused.PositionMs < 10_000 || paused.PositionMs > 11_000 {
		t.Fatalf("pause did not freeze at the extrapolated position: %d", paused.PositionMs)
	}

	// Heartbeat based on the OLD revision: refused, not applied.
	phone.BasedOnRevision = rev(st.Revision)
	got, out := s.Report("p1", State{ItemID: "t1", IsPlaying: true, PositionMs: 12_000}, phone)
	if out != OutcomeStale || got.IsPlaying {
		t.Fatalf("stale heartbeat overwrote the intent: %v %+v", out, got)
	}
	if cur, _ := s.Get("p1"); cur.IsPlaying {
		t.Fatal("stale heartbeat resumed playback")
	}

	// Report that HAS seen it: accepted, acked, anchored from the leader.
	phone.BasedOnRevision = rev(paused.Revision)
	acked, out := s.Report("p1", State{ItemID: "t1", IsPlaying: false, PositionMs: 10_400}, phone)
	if out != OutcomeOK || acked.LeaderAckRevision != acked.Revision || acked.LeaderAckRevision <= paused.LastIntentRevision {
		t.Fatalf("leader ack not recorded: %v %+v", out, acked)
	}

	// Already satisfied → no-op, no revision bump.
	again, out := s.ApplyIntent("p1", "Living Room TV", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: acked.Revision})
	if out != OutcomeOK || again.Revision != acked.Revision {
		t.Fatalf("satisfied intent bumped the revision: %v %d vs %d", out, again.Revision, acked.Revision)
	}
}

// Skip is absolute and based on what the presser saw: once the item has
// moved, a second press is stale and dropped — no double skip.
func TestSetIndexIsAbsoluteAndNeverDoubleSkips(t *testing.T) {
	s := NewStore()
	s.SetQueue("p1", []QueueItem{{ItemID: "a"}, {ItemID: "b"}, {ItemID: "c"}})
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)}
	st, _ := s.Report("p1", State{ItemID: "a", QueueIndex: 0, IsPlaying: true}, phone)

	next, out := s.ApplyIntent("p1", "TV 1", IntentRequest{Type: IntentSetIndex, Index: 1, BasedOnRevision: st.Revision})
	if out != OutcomeOK || next.ItemID != "b" || next.QueueIndex != 1 || next.PositionMs != 0 {
		t.Fatalf("setIndex not applied: %v %+v", out, next)
	}
	// Second room pressed "next" from the same state → index 1 again → satisfied.
	if _, out := s.ApplyIntent("p1", "TV 2", IntentRequest{Type: IntentSetIndex, Index: 1, BasedOnRevision: st.Revision}); out != OutcomeOK {
		t.Fatalf("satisfied setIndex refused: %v", out)
	}
	// Second room computed index 2 from a state that predates the move → stale.
	if _, out := s.ApplyIntent("p1", "TV 2", IntentRequest{Type: IntentSetIndex, Index: 2, BasedOnRevision: st.Revision}); out != OutcomeStale {
		t.Fatalf("stale setIndex was applied (double skip): %v", out)
	}
	if cur, _ := s.Get("p1"); cur.ItemID != "b" {
		t.Fatalf("queue moved past b: %+v", cur)
	}
}

// Legacy leaders cannot be controlled; asleep leaders cannot be resumed.
func TestIntentRefusalsAreHonest(t *testing.T) {
	s := NewStore()
	if _, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPlaying, Playing: false}); out != OutcomeNoSession {
		t.Fatalf("intent on nothing: %v", out)
	}
	s.Report("p1", State{ItemID: "t1", IsPlaying: true}, ReportOpts{DeviceID: "tok-old"})
	if _, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPlaying, Playing: false}); out != OutcomeLegacyLeader {
		t.Fatalf("legacy leader accepted an intent: %v", out)
	}

	s2 := NewStore()
	s2.Report("p1", State{ItemID: "t1", IsPlaying: false}, ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)})
	s2.mu.Lock()
	cur := s2.m["p1"]
	cur.LeaderReportedAtMs = time.Now().Add(-45 * time.Second).UnixMilli()
	s2.m["p1"] = cur
	s2.mu.Unlock()
	if _, out := s2.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPlaying, Playing: true}); out != OutcomeLeaderAsleep {
		t.Fatalf("asleep leader 'resumed': %v", out)
	}
	// Pausing an asleep leader is fine — it is already silent.
	if _, out := s2.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPositionMs, PositionMs: 5000, BasedOnRevision: cur.Revision}); out != OutcomeOK {
		t.Fatalf("seek on an asleep leader refused: %v", out)
	}
}

// Resume must re-anchor even inside the 2s keep-window, or followers
// run ahead by the length of the pause for the rest of the track.
func TestResumeAlwaysReanchors(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), HasPositionMs: true}
	s.Report("p1", State{ItemID: "t1", IsPlaying: true, PositionMs: 60_000}, phone)
	s.mu.Lock()
	s.anchor["p1"] -= 1500 // pretend 1.5s passed while paused: anchor now explains 61.5s
	s.mu.Unlock()
	s.Report("p1", State{ItemID: "t1", IsPlaying: false, PositionMs: 60_000}, phone)
	got, _ := s.Report("p1", State{ItemID: "t1", IsPlaying: true, PositionMs: 60_000}, phone)
	implied := time.Now().UnixMilli() - 60_000
	if abs64(got.TrackStartedAtMs-implied) > 100 {
		t.Fatalf("resume kept a stale anchor: off by %dms", got.TrackStartedAtMs-implied)
	}
}

// At 1.5× the anchor must be scaled, or every heartbeat re-anchors.
func TestRateScalesTheAnchor(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), HasPositionMs: true}
	first, _ := s.Report("p1", State{ItemID: "b1", IsPlaying: true, PositionMs: 90_000, Rate: 1.5}, phone)
	// 20s later at 1.5× the leader is 30s further on: same anchor.
	s.mu.Lock()
	s.anchor["p1"] -= 20_000
	s.m["p1"] = func(st State) State { st.UpdatedAtMs -= 20_000; return st }(s.m["p1"])
	s.mu.Unlock()
	second, _ := s.Report("p1", State{ItemID: "b1", IsPlaying: true, PositionMs: 120_000, Rate: 1.5}, phone)
	if abs64(second.TrackStartedAtMs-(first.TrackStartedAtMs-20_000)) > 100 {
		t.Fatalf("1.5x heartbeat re-anchored: %d vs %d", second.TrackStartedAtMs, first.TrackStartedAtMs-20_000)
	}
}

// Revisions never go backwards across a restart. Revisions track the
// clock, so this holds whenever changes arrive slower than one per
// millisecond — which is every real session (heartbeats are seconds
// apart); a burst tighter than that is the one case not covered.
func TestRevisionsAreMonotonicAcrossRestart(t *testing.T) {
	a := NewStore()
	a.Set("p1", State{ItemID: "t1", IsPlaying: true})
	for i := 0; i < 5; i++ {
		a.Set("p1", State{ItemID: "t1", IsPlaying: true, PositionSec: i})
	}
	before, _ := a.Get("p1")
	time.Sleep(20 * time.Millisecond)
	b := NewStore() // "restart"
	b.Set("p1", State{ItemID: "t1", IsPlaying: true})
	after, _ := b.Get("p1")
	if after.Revision <= before.Revision {
		t.Fatalf("revision went backwards across restart: %d then %d", before.Revision, after.Revision)
	}
}

// A queue-only change must wake a parked follower.
func TestQueueChangeBumpsRevision(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	before, _ := s.Get("p1")
	s.SetQueue("p1", []QueueItem{{ItemID: "t1"}, {ItemID: "t2"}})
	after, _ := s.Get("p1")
	if after.Revision <= before.Revision || after.QueueRevision <= before.QueueRevision {
		t.Fatalf("queue change did not bump revisions: %+v → %+v", before, after)
	}
}

// Presence: a join wakes the leader; a heartbeat does not.
func TestPresenceChangesWakeButHeartbeatsDoNot(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	r0, _ := s.Get("p1")
	s.Join("p1", "tv-1", "Living Room TV", RoleFollower)
	r1, _ := s.Get("p1")
	if r1.Revision <= r0.Revision || len(r1.Followers) != 1 || r1.Followers[0].Label != "Living Room TV" {
		t.Fatalf("join did not register: %+v", r1)
	}
	s.Join("p1", "tv-1", "Living Room TV", RoleFollower)
	r2, _ := s.Get("p1")
	if r2.Revision != r1.Revision {
		t.Fatal("a presence heartbeat bumped the revision — the follower would never park")
	}
	s.Join("p1", "tv-2", "Bedroom TV", RoleViewer)
	r3, _ := s.Get("p1")
	if r3.Viewers != 1 || r3.FollowerCount != 1 {
		t.Fatalf("viewer counted wrong: %+v", r3)
	}
}

// The leader leaving with followers vacates the seat and keeps the
// music going; the sharing toggle ends everything.
func TestLeaderLeavingHandsOffOrEnds(t *testing.T) {
	s := NewStore()
	s.Report("p1", State{ItemID: "t1", IsPlaying: true}, ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)})
	s.Join("p1", "tv-1", "TV", RoleFollower)
	s.End("p1", "tok-phone", "stopped")
	st, ok := s.Get("p1")
	if !ok || st.LeaderTokenID != "" || !st.EndedByLeader || !st.IsPlaying {
		t.Fatalf("leader stop did not vacate cleanly: ok=%v %+v", ok, st)
	}
	// A non-leader DELETE is just leaving.
	s.End("p1", "tv-1", "")
	if st, ok := s.Get("p1"); !ok || st.FollowerCount != 0 {
		t.Fatalf("follower DELETE did not merely leave: ok=%v %+v", ok, st)
	}
	s.End("p1", "tok-phone", "stop-sharing")
	if _, ok := s.Get("p1"); ok {
		t.Fatal("stop-sharing left the session advertised")
	}
}
