package nowplaying

import (
	"context"
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
		s.WaitForChange(context.Background(), "p1", start.Revision, 5*time.Second)
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
	s.WaitForChange(context.Background(), "p1", 0, 5*time.Second) // asking from revision 0
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
	s.WaitForChange(context.Background(), "p1", cur.Revision, 150*time.Millisecond)
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
			s.WaitForChange(context.Background(), "p1", cur.Revision, 3*time.Second)
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
	if n := s.Waiting("p1"); n != 0 {
		t.Errorf("waiter count leaked: %d parked after every waiter woke", n)
	}
}

// A held request is a goroutine; a token holder must not be able to
// park an unbounded number of them. Past the cap a poll is answered at
// once — plain polling, never a refusal.
func TestWaitForChangeCapsParkedPollsPerProfile(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	cur, _ := s.Get("p1")

	done := make(chan struct{}, MaxWaitersPerProfile)
	for i := 0; i < MaxWaitersPerProfile; i++ {
		go func() {
			s.WaitForChange(context.Background(), "p1", cur.Revision, 3*time.Second)
			done <- struct{}{}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for s.Waiting("p1") < MaxWaitersPerProfile {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d waiters parked", s.Waiting("p1"), MaxWaitersPerProfile)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The next one over the cap does not park at all.
	began := time.Now()
	s.WaitForChange(context.Background(), "p1", cur.Revision, 3*time.Second)
	if elapsed := time.Since(began); elapsed > 200*time.Millisecond {
		t.Errorf("poll over the cap parked for %v", elapsed)
	}
	// Another profile is unaffected by this one's crowd.
	if n := s.Waiting("p2"); n != 0 {
		t.Errorf("cap leaked across profiles: p2 has %d parked", n)
	}

	s.Set("p1", State{ItemID: "t2", IsPlaying: true})
	for i := 0; i < MaxWaitersPerProfile; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d capped waiters woke", i, MaxWaitersPerProfile)
		}
	}
}

// A shutting-down server must not be held for a poll cycle: the
// request context ending releases the wait immediately.
func TestWaitForChangeReleasesOnContextDone(t *testing.T) {
	s := NewStore()
	s.Set("p1", State{ItemID: "t1", IsPlaying: true})
	cur, _ := s.Get("p1")

	ctx, cancel := context.WithCancel(context.Background())
	woke := make(chan time.Duration, 1)
	go func() {
		began := time.Now()
		s.WaitForChange(ctx, "p1", cur.Revision, 5*time.Second)
		woke <- time.Since(began)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case took := <-woke:
		if took > time.Second {
			t.Errorf("waiter took %v to notice the context ending", took)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter never released on context done")
	}
	if n := s.Waiting("p1"); n != 0 {
		t.Errorf("waiter count leaked after context done: %d", n)
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

// A reorder bumps the session revision but not the item revision, so
// an index chosen from the OLD list would land on whatever now sits in
// that slot. ifQueueRevision names the list the index was chosen from;
// a mismatch is stale, and an older device that omits it is unguarded
// rather than refused.
func TestSetIndexRefusedWhenQueueChangedUnderIt(t *testing.T) {
	s := NewStore()
	s.SetQueue("p1", []QueueItem{{ItemID: "a"}, {ItemID: "b"}, {ItemID: "c"}})
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)}
	st, _ := s.Report("p1", State{ItemID: "a", QueueIndex: 0, IsPlaying: true}, phone)
	seen := st.QueueRevision

	// The leader reorders: "c" now sits where "b" was. Same item plays on.
	s.SetQueue("p1", []QueueItem{{ItemID: "a"}, {ItemID: "c"}, {ItemID: "b"}})
	cur, _ := s.Get("p1")
	if cur.QueueRevision == seen {
		t.Fatalf("SetQueue did not bump the queue revision")
	}

	// A TV that chose index 1 meaning "b" from the old list → stale, "a" still plays.
	old := seen
	if _, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetIndex, Index: 1, BasedOnRevision: cur.Revision, IfQueueRevision: &old}); out != OutcomeStale {
		t.Fatalf("index into a replaced queue was applied: %v", out)
	}
	if now, _ := s.Get("p1"); now.ItemID != "a" {
		t.Fatalf("stale index moved the session: %+v", now)
	}
	// Chosen again from the new list → applied, and it is "c".
	fresh := cur.QueueRevision
	next, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetIndex, Index: 1, BasedOnRevision: cur.Revision, IfQueueRevision: &fresh})
	if out != OutcomeOK || next.ItemID != "c" {
		t.Fatalf("fresh index refused or wrong: %v %+v", out, next)
	}
	// An older device sends no guard: still served (index 2 = "b" now).
	if got, out := s.ApplyIntent("p1", "old TV", IntentRequest{Type: IntentSetIndex, Index: 2, BasedOnRevision: next.Revision}); out != OutcomeOK || got.ItemID != "b" {
		t.Fatalf("unguarded setIndex refused: %v %+v", out, got)
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
	if r3.Viewers != 1 || r3.FollowerCount != 1 || r3.Controllers != 0 {
		t.Fatalf("viewer counted wrong: %+v", r3)
	}
	// A remote is a viewer (the leader keeps listening) that is also
	// counted on its own, and never a follower.
	s.Join("p1", "phone-1", "Chris's iPhone", RoleController)
	r4, _ := s.Get("p1")
	if r4.Viewers != 2 || r4.Controllers != 1 || r4.FollowerCount != 1 {
		t.Fatalf("controller counted wrong: %+v", r4)
	}
	if r4.Revision <= r3.Revision {
		t.Fatal("a remote opening did not wake the leader")
	}
	// Opening the Remote from a watching screen is a role change under
	// the same key: still one entry, now a remote.
	s.Join("p1", "tv-2", "Bedroom TV", RoleController)
	r5, _ := s.Get("p1")
	if r5.Viewers != 2 || r5.Controllers != 2 {
		t.Fatalf("viewer→controller role change counted wrong: %+v", r5)
	}
	// An unknown role is a follower, as before.
	s.Join("p1", "tv-3", "Kitchen TV", "dj")
	r6, _ := s.Get("p1")
	if r6.FollowerCount != 2 {
		t.Fatalf("unknown role not coerced to follower: %+v", r6)
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

// A follower launches on the anchor as it stands. A track's first
// report is sent before the leader's own player has rolled, and the 2 s
// keep-window then holds that anchor for the whole track — every
// follower a constant few hundred ms ahead until a scrub on the phone
// forced a fresh one. A join now asks for exactly that re-anchor.
func TestAFollowerJoiningAsksTheLeaderForAFreshAnchor(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", DeviceLabel: "iPhone", BasedOnRevision: rev(0), HasPositionMs: true}

	first, _ := s.Report("p1", State{ItemID: "t1", PositionMs: 10_000, IsPlaying: true}, phone)
	if first.AnchorRevision != first.Revision {
		t.Fatalf("a first report must stamp the anchor revision: %+v", first)
	}
	// The player rolled 300 ms after that report: every heartbeat now
	// reads 300 ms behind the anchor — inside the window, so kept.
	second, _ := s.Report("p1", State{ItemID: "t1", PositionMs: 9_700, IsPlaying: true}, phone)
	if second.TrackStartedAtMs != first.TrackStartedAtMs || second.AnchorRevision != first.AnchorRevision {
		t.Fatalf("a heartbeat inside the window moved the anchor: %+v vs %+v", first, second)
	}

	joinedAt := s.Join("p1", "tv-1", "Living Room TV", RoleFollower)
	pending, _ := s.Get("p1")
	if !pending.AnchorRefreshPending || joinedAt != pending.Revision || joinedAt <= second.Revision {
		t.Fatalf("a follower's join did not ask for a fresh anchor: joinedAt=%d %+v", joinedAt, pending)
	}
	if pending.AnchorRevision != first.AnchorRevision {
		t.Fatal("asking must not itself move the anchor")
	}

	// The leader's next report re-anchors from its settled position.
	fresh, out := s.Report("p1", State{ItemID: "t1", PositionMs: 9_700, IsPlaying: true}, phone)
	if out != OutcomeOK {
		t.Fatalf("report after a join refused: %v", out)
	}
	moved := fresh.TrackStartedAtMs - first.TrackStartedAtMs
	if moved < 250 || moved > 400 {
		t.Fatalf("the re-anchor did not adopt the settled position (moved %d ms)", moved)
	}
	if fresh.AnchorRevision != fresh.Revision || fresh.AnchorRevision <= joinedAt || fresh.AnchorRefreshPending {
		t.Fatalf("the re-anchor was not stamped past the join: joinedAt=%d %+v", joinedAt, fresh)
	}

	// Once. The next heartbeat is back inside the window.
	again, _ := s.Report("p1", State{ItemID: "t1", PositionMs: 9_700, IsPlaying: true}, phone)
	if again.AnchorRevision != fresh.AnchorRevision || again.TrackStartedAtMs != fresh.TrackStartedAtMs {
		t.Fatalf("the refresh was not spent by the first report: %+v", again)
	}

	// A follower's heartbeat and a viewer's arrival ask for nothing.
	s.Join("p1", "tv-1", "Living Room TV", RoleFollower)
	s.Join("p1", "tv-2", "Bedroom TV", RoleViewer)
	if st, _ := s.Get("p1"); st.AnchorRefreshPending {
		t.Fatal("a heartbeat or a viewer asked for a re-anchor")
	}

	// An explicit request (a follower about to launch on a pulled track
	// change) wakes the leader and names the revision it landed at.
	before, _ := s.Get("p1")
	asked := s.RequestAnchorRefresh("p1")
	st, _ := s.Get("p1")
	if !st.AnchorRefreshPending || asked <= before.Revision || asked != st.Revision {
		t.Fatalf("an explicit refresh request did not register: asked=%d %+v", asked, st)
	}
	if s.RequestAnchorRefresh("nobody") != 0 {
		t.Fatal("a refresh request without a session answered a revision")
	}
}

// The leader stopping FOR EVERYONE: the record stays, paused and
// marked ended, so every follower wakes to a state that says stop —
// not to a 204 that reads "leader quiet, keep playing".
func TestStopAllEndsThePartyForEveryDevice(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", DeviceLabel: "iPhone", BasedOnRevision: rev(0), HasPositionMs: true}
	s.Report("p1", State{ItemID: "t1", PositionMs: 5_000, DurationSec: 300, IsPlaying: true}, phone)
	s.SetQueue("p1", []QueueItem{{ItemID: "t1"}, {ItemID: "t2"}})
	s.Join("p1", "tv-1", "Living Room TV", RoleFollower)
	before, _ := s.Get("p1")

	if out := s.EndBy("p1", "tok-phone", "iPhone", "stop-all"); out != EndAll {
		t.Fatalf("stop-all from the leader was not ended-all: %v", out)
	}
	st, ok := s.Get("p1")
	if !ok || !st.Ended || st.IsPlaying || st.EndedByLabel != "iPhone" || st.EndedAtMs == 0 {
		t.Fatalf("no readable tombstone: ok=%v %+v", ok, st)
	}
	if st.Revision <= before.Revision {
		t.Fatal("ending did not wake the parked polls")
	}
	if st.PositionMs < 5_000 || st.PositionMs > 6_000 {
		t.Fatalf("the tombstone lost the position: %d", st.PositionMs)
	}
	if st.LeaderDormant != true {
		t.Fatal("an ended party still reads as a live seat")
	}
	if _, has := s.Queue("p1"); has {
		t.Fatal("an ended party still hands out its queue")
	}
	// Nothing can be done to it from a room.
	if _, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPlaying, Playing: true}); out != OutcomeNoSession {
		t.Fatalf("an intent resurrected an ended party: %v", out)
	}
	// Ending it twice is idempotent.
	if out := s.EndBy("p1", "tok-phone", "iPhone", "stop-all"); out != EndAll {
		t.Fatalf("a second stop-all: %v", out)
	}

	// The phone playing again starts a fresh session over it.
	next, out := s.Report("p1", State{ItemID: "t2", PositionMs: 0, IsPlaying: true}, phone)
	if out != OutcomeOK || next.Ended || !next.IsPlaying || next.LeaderTokenID != "tok-phone" {
		t.Fatalf("a report after stop-all did not start fresh: %v %+v", out, next)
	}
	if next.ItemRevision != next.Revision || next.AnchorRevision != next.Revision {
		t.Fatalf("a fresh session kept the tombstone's bookkeeping: %+v", next)
	}
}

func TestAnEndedPartyIsHandedToWhoeverPlaysNext(t *testing.T) {
	s := NewStore()
	s.Report("p1", State{ItemID: "t1", IsPlaying: true}, ReportOpts{DeviceID: "tok-phone", DeviceLabel: "iPhone", BasedOnRevision: rev(0)})
	s.Join("p1", "tok-tv", "TV", RoleFollower)
	s.EndBy("p1", "tok-phone", "iPhone", "stop-all")
	// The TV starting its own album afterwards is not "seat held by the
	// phone": the tombstone protects nobody.
	st, out := s.Report("p1", State{ItemID: "t9", IsPlaying: true}, ReportOpts{DeviceID: "tok-tv", DeviceLabel: "TV", BasedOnRevision: rev(0)})
	if out != OutcomeOK || st.LeaderTokenID != "tok-tv" || st.Ended {
		t.Fatalf("the TV could not lead after a stop-all: %v %+v", out, st)
	}
}

func TestStopAllFromAMemberIsJustLeaving(t *testing.T) {
	s := NewStore()
	s.Report("p1", State{ItemID: "t1", IsPlaying: true}, ReportOpts{DeviceID: "tok-phone", DeviceLabel: "iPhone", BasedOnRevision: rev(0)})
	s.Join("p1", "tok-tv", "TV", RoleFollower)
	if out := s.EndBy("p1", "tok-tv", "TV", "stop-all"); out != EndLeft {
		t.Fatalf("a follower's stop-all was not a mere leave: %v", out)
	}
	st, ok := s.Get("p1")
	if !ok || st.Ended || !st.IsPlaying || st.FollowerCount != 0 {
		t.Fatalf("a follower's stop-all touched the party: ok=%v %+v", ok, st)
	}
	// And the outcomes of the older reasons are named too.
	s.Join("p1", "tok-tv", "TV", RoleFollower)
	if out := s.EndBy("p1", "tok-phone", "iPhone", "stopped"); out != EndVacated {
		t.Fatalf("stopped with followers: %v", out)
	}
	if out := s.EndBy("p1", "tok-phone", "iPhone", "stop-sharing"); out != EndDeleted {
		t.Fatalf("stop-sharing: %v", out)
	}
	if out := s.EndBy("p1", "tok-phone", "iPhone", ""); out != EndNoSession {
		t.Fatalf("a DELETE with nothing playing: %v", out)
	}
}

func TestAnEndedPartyExpires(t *testing.T) {
	s := NewStore()
	s.Report("p1", State{ItemID: "t1", IsPlaying: true}, ReportOpts{DeviceID: "tok-phone", DeviceLabel: "iPhone", BasedOnRevision: rev(0)})
	s.Join("p1", "tok-tv", "TV", RoleFollower)
	s.EndBy("p1", "tok-phone", "iPhone", "stop-all")
	if _, ok := s.Get("p1"); !ok {
		t.Fatal("a fresh tombstone is not readable")
	}
	s.mu.Lock()
	st := s.m["p1"]
	st.EndedAtMs = time.Now().Add(-time.Minute).UnixMilli()
	s.m["p1"] = st
	s.mu.Unlock()
	if _, ok := s.Get("p1"); ok {
		t.Fatal("a minute-old tombstone is still advertised")
	}
	s.mu.RLock()
	_, still := s.m["p1"]
	s.mu.RUnlock()
	if still {
		t.Fatal("the expired tombstone was hidden but not deleted")
	}
}

// A start announced for a server instant: the anchor is that instant
// (less the start position), the server holds the position there until
// T, and runs from T afterwards. A spent announcement is dropped; one
// for another item rides along without touching the current anchor.
func TestScheduledStartPinsTheAnchorAndHoldsUntilT(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	startAt := now + 1500
	opts := ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true}

	st, out := s.Report("p1", State{ItemID: "a", Title: "A", IsPlaying: true, PositionMs: 0,
		StartAtMs: startAt, StartPositionMs: 0}, opts)
	if out != OutcomeOK {
		t.Fatalf("scheduled report refused: %v", out)
	}
	if st.TrackStartedAtMs != startAt || st.StartAtMs != startAt {
		t.Fatalf("anchor %d / startAt %d, want both %d", st.TrackStartedAtMs, st.StartAtMs, startAt)
	}
	s.mu.Lock()
	before := s.extrapolatedLocked("p1", st, now+500)
	after := s.extrapolatedLocked("p1", st, startAt+2000)
	s.mu.Unlock()
	if before != 0 {
		t.Fatalf("position before T = %d, want 0 (held at the start position)", before)
	}
	if after < 1900 || after > 2100 {
		t.Fatalf("position 2 s after T = %d, want ~2000", after)
	}

	// A start position: the item's zero is T minus it.
	st, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 30000,
		StartAtMs: startAt, StartPositionMs: 30000}, opts)
	if st.TrackStartedAtMs != startAt-30000 {
		t.Fatalf("anchor with a start position: %d, want %d", st.TrackStartedAtMs, startAt-30000)
	}

	// Spent (more than 2 s old): dropped, ordinary anchoring.
	st, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 5000,
		StartAtMs: now - 5000, StartPositionMs: 0}, opts)
	if st.StartAtMs != 0 || st.StartItemID != "" {
		t.Fatalf("a spent schedule survived: %+v", st)
	}

	// The next track, announced ahead: kept on the state, current anchor as usual.
	st, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 6000,
		StartAtMs: now + 4000, StartPositionMs: 0, StartItemID: "b"}, opts)
	if st.StartItemID != "b" || st.StartAtMs != now+4000 {
		t.Fatalf("next-track announcement lost: %+v", st)
	}
	if abs64(st.TrackStartedAtMs-(now-6000)) > 3000 {
		t.Fatalf("a next-track announcement moved the current anchor: %d vs now-6000=%d", st.TrackStartedAtMs, now-6000)
	}
	s.mu.Lock()
	running := s.extrapolatedLocked("p1", st, now+1000)
	s.mu.Unlock()
	if running < 4000 {
		t.Fatalf("a next-track announcement held the current item: %d", running)
	}
}

// The reporter says WHEN it measured its position (server time, via
// its clock offset): the anchor comes out without the request's
// latency. A sample nowhere near now is clock nonsense and is ignored.
func TestSampledAtServerMsAnchorsWithoutLatency(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	sampled := now - 3000
	st, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 10000},
		ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true, SampledAtMs: sampled})
	if out != OutcomeOK {
		t.Fatalf("refused: %v", out)
	}
	if abs64(st.TrackStartedAtMs-(sampled-10000)) > 50 {
		t.Fatalf("anchor %d, want %d (sampledAt − position)", st.TrackStartedAtMs, sampled-10000)
	}
	st, _ = s.Report("p1", State{ItemID: "b", IsPlaying: true, PositionMs: 0},
		ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true, SampledAtMs: now - 60000})
	if abs64(st.TrackStartedAtMs-now) > 1000 {
		t.Fatalf("a sample a minute off was believed: anchor %d vs now %d", st.TrackStartedAtMs, now)
	}
}

// "Make this device the leader": only the seat holder may, only to a
// live follower; afterwards the old leader is refused and the new one
// must read the state before it leads.
func TestHandOverDropsTheNewLeaderFromTheFollowers(t *testing.T) {
	s := NewStore()
	s.Report("p", State{Source: "plex", ItemID: "plex:1", Title: "t", PositionMs: 1000, IsPlaying: true, Rate: 1},
		ReportOpts{DeviceID: "phone", ClaimSeat: true})
	s.Join("p", "tv", "Living Room TV", RoleFollower)
	st, out := s.HandOver("p", "phone", "", "tv")
	if out != OutcomeOK {
		t.Fatalf("handover: %v", out)
	}
	if st.FollowerCount != 0 {
		t.Fatalf("the new leader must not be listed among the followers: %+v", st.Followers)
	}
	if st.LeaderTokenID != "tv" {
		t.Fatalf("seat should be the TV's, got %q", st.LeaderTokenID)
	}
}

func TestHandOverMovesTheSeat(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "phone", DeviceLabel: "Phone", BasedOnRevision: &rev})
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)

	if _, out := s.HandOver("p1", "stranger", "Kitchen iPad", "tv1"); out != OutcomeNotInParty {
		t.Fatalf("a device in no party handed the seat over: %v", out)
	}
	if _, out := s.HandOver("p1", "", "", "tv1"); out != OutcomeNotInParty {
		t.Fatalf("an unnamed device handed the seat over: %v", out)
	}
	if _, out := s.HandOver("p1", "phone", "", "nobody"); out != OutcomeNoSuchFollower {
		t.Fatalf("an unknown target was accepted: %v", out)
	}
	if _, out := s.HandOver("nobody", "x", "", "y"); out != OutcomeNoSession {
		t.Fatalf("handover without a session: %v", out)
	}

	st, out := s.HandOver("p1", "phone", "Phone", "tv1")
	if out != OutcomeOK || st.LeaderTokenID != "tv1" || st.LeaderLabel != "Living Room TV" || st.HandedOverAtMs == 0 {
		t.Fatalf("handover: %v %+v", out, st)
	}
	if st.HandedOverFrom != "phone" || st.HandedOverByLabel != "Phone" {
		t.Fatalf("who gave the seat away: from %q by %q", st.HandedOverFrom, st.HandedOverByLabel)
	}
	based := st.Revision
	if _, out := s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "phone", BasedOnRevision: &based}); out != OutcomeSeatHeld {
		t.Fatalf("the old leader kept the seat: %v", out)
	}
	old := st.Revision - 1
	if _, out := s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tv1", BasedOnRevision: &old}); out != OutcomeStale {
		t.Fatalf("the new leader led without reading the handover: %v", out)
	}
	st2, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 12000},
		ReportOpts{DeviceID: "tv1", DeviceLabel: "Living Room TV", BasedOnRevision: &based, HasPositionMs: true})
	if out != OutcomeOK || st2.LeaderTokenID != "tv1" || st2.HandedOverAtMs != st.HandedOverAtMs {
		t.Fatalf("new leader's first report: %v %+v", out, st2)
	}
}

// Any device in the party may choose the leader (owner, 2026-10-30:
// "promote any follower device to leader, from any party play connected
// device"): a follower for another follower or for itself, a Remote.
// The old leader, which did not ask, reads who gave its seat away on its
// refused report, and keeps reading it while the new leader leads.
func TestAnyDeviceInThePartyChoosesTheLeader(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "pc", DeviceLabel: "Windows PC", BasedOnRevision: &rev})
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	s.Join("p1", "phone", "Chris's iPhone", RoleFollower)
	s.Join("p1", "remote", "Chris's iPad", RoleController)

	// A follower makes another follower the leader.
	st, out := s.HandOver("p1", "phone", "Chris's iPhone", "tv1")
	if out != OutcomeOK || st.LeaderTokenID != "tv1" {
		t.Fatalf("a follower's hand-over: %v %+v", out, st)
	}
	if st.HandedOverFrom != "pc" || st.HandedOverByLabel != "Chris's iPhone" {
		t.Fatalf("who gave the seat away: from %q by %q", st.HandedOverFrom, st.HandedOverByLabel)
	}

	// The new leader leads on; the fields stay with its reports.
	based := st.Revision
	st, out = s.Report("p1", State{ItemID: "b", IsPlaying: true},
		ReportOpts{DeviceID: "tv1", DeviceLabel: "Living Room TV", BasedOnRevision: &based})
	if out != OutcomeOK || st.HandedOverFrom != "pc" || st.HandedOverByLabel != "Chris's iPhone" {
		t.Fatalf("the new leader's report dropped who gave the seat away: %v %+v", out, st)
	}

	// The old leader's next report is refused and says so.
	based = st.Revision
	st, out = s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "pc", BasedOnRevision: &based})
	if out != OutcomeSeatHeld || st.HandedOverFrom != "pc" {
		t.Fatalf("the old leader was not told: %v from %q", out, st.HandedOverFrom)
	}

	// A follower makes ITSELF the leader; the label falls back to its
	// presence entry when the request carries none.
	st, out = s.HandOver("p1", "phone", "", "phone")
	if out != OutcomeOK || st.LeaderTokenID != "phone" || st.HandedOverFrom != "tv1" ||
		st.HandedOverByLabel != "Chris's iPhone" {
		t.Fatalf("a follower taking the lead: %v %+v", out, st)
	}

	// A Remote hands it on again.
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	st, out = s.HandOver("p1", "remote", "Chris's iPad", "tv1")
	if out != OutcomeOK || st.LeaderTokenID != "tv1" || st.HandedOverFrom != "phone" ||
		st.HandedOverByLabel != "Chris's iPad" {
		t.Fatalf("a Remote's hand-over: %v %+v", out, st)
	}

	// A Remote whose presence aged out may not.
	s.followers["p1"]["remote"] = presenceEntry{label: "Chris's iPad", role: RoleController,
		last: time.Now().Add(-2 * followerTTL)}
	if _, out := s.HandOver("p1", "remote", "Chris's iPad", "phone"); out != OutcomeNotInParty {
		t.Fatalf("a device gone from the party handed the seat over: %v", out)
	}

	// Another device taking the seat over clears them.
	based = st.Revision
	st, out = s.Report("p1", State{ItemID: "c", IsPlaying: true},
		ReportOpts{DeviceID: "pc", BasedOnRevision: &based, TakeOver: true})
	if out != OutcomeOK || st.LeaderTokenID != "pc" || st.HandedOverFrom != "" || st.HandedOverByLabel != "" {
		t.Fatalf("a take-over kept the last hand-over's names: %v %+v", out, st)
	}
}

// An Apple TV follows under a UUID it minted, not its token id. A seat
// handed to that key must name the TV's TOKEN — what its reports carry and
// what yourDeviceKey tells it — or the TV never sees the seat is its own
// (2026-10-30, found on the party rig). And a TV following, or on its
// Remote, under a minted key is in the party when it asks for a hand-over.
func TestHandOverToAndFromAMintedKey(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-phone", DeviceLabel: "Kate's iPhone", BasedOnRevision: &rev})
	s.Join("p1", "uuid-tv", "Living Room TV", RoleFollower)
	s.NoteToken("p1", "uuid-tv", "tok-tv")
	s.Join("p1", "uuid-den:remote", "Den TV", RoleController)
	s.NoteToken("p1", "uuid-den:remote", "tok-den")
	s.Join("p1", "tok-pc", "Windows PC", RoleFollower)
	s.NoteToken("p1", "tok-pc", "tok-pc")

	st, out := s.HandOver("p1", "tok-phone", "Kate's iPhone", "uuid-tv")
	if out != OutcomeOK || st.LeaderTokenID != "tok-tv" || st.LeaderLabel != "Living Room TV" {
		t.Fatalf("handed to the TV's key: %v leader %q", out, st.LeaderTokenID)
	}
	// The TV leads with its token: its first report keeps the seat.
	based := st.Revision
	if _, out := s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-tv", DeviceLabel: "Living Room TV", BasedOnRevision: &based}); out != OutcomeOK {
		t.Fatalf("the TV's first report as leader: %v", out)
	}

	// The Den TV's Remote (a minted key) makes the PC the leader.
	st, out = s.HandOver("p1", "tok-den", "", "tok-pc")
	if out != OutcomeOK || st.LeaderTokenID != "tok-pc" || st.HandedOverFrom != "tok-tv" ||
		st.HandedOverByLabel != "Den TV" {
		t.Fatalf("a TV Remote's hand-over: %v %+v", out, st)
	}

	// An old entry with no token noted still hands over to its key.
	s.Join("p1", "legacy-key", "Old TV", RoleFollower)
	st, out = s.HandOver("p1", "tok-pc", "Windows PC", "legacy-key")
	if out != OutcomeOK || st.LeaderTokenID != "legacy-key" {
		t.Fatalf("a key with no token: %v leader %q", out, st.LeaderTokenID)
	}
}

// A follower's drift reading is listed per device, keyed by its token,
// and a heartbeat carrying one does not wake the party.
func TestFollowerDriftIsListedPerDevice(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	s.Report("p1", State{ItemID: "a", IsPlaying: true}, ReportOpts{DeviceID: "phone", BasedOnRevision: &rev})
	d := int64(120)
	s.JoinWithDrift("p1", "tv1", "Living Room TV", RoleFollower, &d, 0)
	after := s.rev["p1"]
	d2 := int64(-40)
	s.JoinWithDrift("p1", "tv1", "Living Room TV", RoleFollower, &d2, 0)
	if s.rev["p1"] != after {
		t.Fatalf("a drift heartbeat bumped the revision: %d → %d", after, s.rev["p1"])
	}
	st, _ := s.Get("p1")
	if len(st.Followers) != 1 || st.Followers[0].DeviceKey != "tv1" {
		t.Fatalf("followers: %+v", st.Followers)
	}
	if st.Followers[0].DriftMs == nil || *st.Followers[0].DriftMs != -40 || st.Followers[0].DriftMeasuredAtMs == 0 {
		t.Fatalf("drift not listed: %+v", st.Followers[0])
	}
}

// A press from the room overrides an announced start.
func TestAnIntentCancelsAScheduledStart(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, StartAtMs: now + 1500},
		ReportOpts{DeviceID: "phone", BasedOnRevision: &rev})
	st2, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: st.Revision})
	if out != OutcomeOK || st2.StartAtMs != 0 {
		t.Fatalf("pause left the schedule standing: %v %+v", out, st2)
	}
}

// ---- the next-track pre-announce (protocol v2) --------------------------

// leaderOpts is the phone leading under the conductor protocol, with
// positions in ms sampled at sampledAtMs on the server clock (0 = the
// arrival time stands in).
func leaderOpts(sampledAtMs int64) ReportOpts {
	return ReportOpts{DeviceID: "phone", DeviceLabel: "iPhone", BasedOnRevision: rev(0),
		HasPositionMs: true, SampledAtMs: sampledAtMs}
}

// noSchedule: none of the three schedule fields is set.
func noSchedule(st State) bool {
	return st.StartAtMs == 0 && st.StartPositionMs == 0 && st.StartItemID == ""
}

// A next-track announcement stands while the leader repeats it: the
// heartbeat's position moves on, the current anchor stands, the current
// item is not held, and the announcement reads exactly as sent.
func TestNextItemScheduleSurvivesAHeartbeatThatCarriesIt(t *testing.T) {
	s := NewStore()
	now := time.Now().UnixMilli()
	T := now + 3000 + 540 // 3 s left, plus the next track's time to audio and the margin
	first, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177000,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"}, leaderOpts(now))
	if out != OutcomeOK || first.StartItemID != "b" || first.StartAtMs != T {
		t.Fatalf("announcement not stored: %v %+v", out, first)
	}
	st, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 178000,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"}, leaderOpts(now+1000))
	if out != OutcomeOK {
		t.Fatalf("heartbeat refused: %v", out)
	}
	if st.StartItemID != "b" || st.StartAtMs != T || st.StartPositionMs != 0 {
		t.Fatalf("announcement changed under a repeating heartbeat: %+v", st)
	}
	if st.TrackStartedAtMs != first.TrackStartedAtMs || st.AnchorRevision != first.AnchorRevision {
		t.Fatalf("a repeated announcement moved the current anchor: %d → %d", first.TrackStartedAtMs, st.TrackStartedAtMs)
	}
	if st.ItemRevision != first.ItemRevision || st.Revision <= first.Revision {
		t.Fatalf("revisions: item %d → %d, session %d → %d", first.ItemRevision, st.ItemRevision, first.Revision, st.Revision)
	}
	s.mu.Lock()
	running := s.extrapolatedLocked("p1", st, now+2000)
	s.mu.Unlock()
	if running != 179000 {
		t.Fatalf("a next-track announcement held the current item: %d, want 179000", running)
	}
}

// Withdrawal is omission. The leader repeats the announcement on every
// report of the current item, so a report of it that leaves the fields
// out has withdrawn it — the queue edit that removed the next track, a
// repeat-mode change — and nothing is carried forward from the stored
// state (an earlier draft carried it across exactly these). The current
// anchor does not move with it, a fresh announcement afterwards stands
// again, and a schedule for the CURRENT item is cleared by omission as
// it always was.
func TestAReportThatOmitsTheFieldsWithdrawsANextItemSchedule(t *testing.T) {
	s := NewStore()
	now := time.Now().UnixMilli()
	T := now + 3500
	first, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177000,
		StartAtMs: T, StartItemID: "b"}, leaderOpts(now))
	if first.StartItemID != "b" || first.StartAtMs != T {
		t.Fatalf("announcement not stored: %+v", first)
	}
	st, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177300}, leaderOpts(now+300))
	if out != OutcomeOK {
		t.Fatalf("withdrawal refused: %v", out)
	}
	if !noSchedule(st) {
		t.Fatalf("a report without the fields did not withdraw the announcement: %+v", st)
	}
	if got, _ := s.Get("p1"); !noSchedule(got) {
		t.Fatalf("followers still read a withdrawn announcement: %+v", got)
	}
	if st.TrackStartedAtMs != first.TrackStartedAtMs || st.AnchorRevision != first.AnchorRevision {
		t.Fatalf("the withdrawal moved the current anchor: %d → %d", first.TrackStartedAtMs, st.TrackStartedAtMs)
	}
	if st.ItemRevision != first.ItemRevision || st.Revision <= first.Revision {
		t.Fatalf("a withdrawal is no track change but must wake the party: item %d → %d, session %d → %d",
			first.ItemRevision, st.ItemRevision, first.Revision, st.Revision)
	}

	// A small scrub back the leader withdrew for: nothing comes back.
	st, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 175700}, leaderOpts(now+400))
	if !noSchedule(st) {
		t.Fatalf("a withdrawn announcement returned: %+v", st)
	}

	// A new trajectory that reaches 3 s again announces afresh, and it
	// stands: 2.3 s left at the sample, plus the time to audio.
	T2 := now + 2400 + 2300 + 540
	st, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177700,
		StartAtMs: T2, StartItemID: "c"}, leaderOpts(now+2400))
	if st.StartItemID != "c" || st.StartAtMs != T2 {
		t.Fatalf("a fresh announcement after a withdrawal was not stored: %+v", st)
	}

	// A schedule for the CURRENT item is cleared by omission, as before.
	s = NewStore()
	s.Report("p1", State{ItemID: "d", IsPlaying: true, PositionMs: 0, StartAtMs: now + 1500, StartItemID: "d"}, leaderOpts(now))
	st, _ = s.Report("p1", State{ItemID: "d", IsPlaying: true, PositionMs: 200}, leaderOpts(now+1700))
	if !noSchedule(st) || st.TrackStartedAtMs != now+1500 {
		t.Fatalf("a current-item schedule after a report without it: %+v", st)
	}
}

// A paused report never carries a next-track announcement — a paused
// leader reaches no boundary. Fields on one are refused (an earlier
// draft stored them, and a resume carried them forward), the resume does
// not bring them back, and the new trajectory after it announces afresh.
func TestAPausedReportNeverCarriesANextItemSchedule(t *testing.T) {
	s := NewStore()
	now := time.Now().UnixMilli()
	T := now + 3000
	s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177500,
		StartAtMs: T, StartItemID: "b"}, leaderOpts(now))
	// The trajectory still fits (2.5 s left at the sample, T 500 ms past
	// it): only the pause refuses it.
	p, out := s.Report("p1", State{ItemID: "a", IsPlaying: false, DurationSec: 180, PositionMs: 177600,
		StartAtMs: T, StartItemID: "b"}, leaderOpts(now+100))
	if out != OutcomeOK || p.IsPlaying {
		t.Fatalf("pause refused: %v %+v", out, p)
	}
	if !noSchedule(p) {
		t.Fatalf("a paused report stored the announcement: %+v", p)
	}
	if got, _ := s.Get("p1"); !noSchedule(got) {
		t.Fatalf("followers read an announcement on a paused state: %+v", got)
	}
	r, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177600}, leaderOpts(now+1100))
	if !noSchedule(r) {
		t.Fatalf("the resume brought the announcement back: %+v", r)
	}
	// Paused for a second: the boundary moved a second later, and so did T.
	T2 := T + 1000
	r, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177700,
		StartAtMs: T2, StartItemID: "b"}, leaderOpts(now+1200))
	if r.StartItemID != "b" || r.StartAtMs != T2 {
		t.Fatalf("the announcement for the new trajectory was not stored: %+v", r)
	}

	// The rule is for the NEXT item only: a paused report that carries a
	// start of the CURRENT item is taken exactly as before.
	s = NewStore()
	Tc := now + 1500
	c, _ := s.Report("p1", State{ItemID: "c", IsPlaying: false, PositionMs: 30000, StartAtMs: Tc,
		StartPositionMs: 30000, StartItemID: "c"}, leaderOpts(now))
	if c.StartAtMs != Tc || c.StartItemID != "c" || c.StartPositionMs != 30000 || c.TrackStartedAtMs != Tc-30000 {
		t.Fatalf("a paused report's start of the current item: %+v", c)
	}
}

// The server's own check of a next-track T against the report that
// carries it: boundary = sampled instant + (durationSec·1000 −
// positionMs) ÷ rate, and T must sit within [−1000, +4000] ms of it,
// both ends included. A restart or a scrub the leader did not withdraw
// for moves the boundary away from T, and the announcement is dropped
// while the report itself applies as usual.
func TestANextItemScheduleOffItsOwnTrajectoryIsDropped(t *testing.T) {
	now := time.Now().UnixMilli()
	announce := func(st State, sampledAtMs int64) State {
		t.Helper()
		got, out := NewStore().Report("p1", st, leaderOpts(sampledAtMs))
		if out != OutcomeOK {
			t.Fatalf("refused: %v", out)
		}
		return got
	}
	at := func(posMs int64, rate float64, startAt int64) State {
		return State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: posMs, Rate: rate,
			StartAtMs: startAt, StartItemID: "b"}
	}
	for _, c := range []struct {
		name     string
		rate     float64
		boundary int64 // now + (180 000 − 177 000) ÷ rate
		off      int64
		keep     bool
	}{
		{"1×, 1 s early: the earliest kept", 1, now + 3000, -1000, true},
		{"1×, 1001 ms early", 1, now + 3000, -1001, false},
		{"1×, on the boundary", 1, now + 3000, 0, true},
		{"1×, a streamed open past it", 1, now + 3000, 740, true},
		{"1×, 4 s late: the latest kept", 1, now + 3000, 4000, true},
		{"1×, 4001 ms late", 1, now + 3000, 4001, false},
		{"1.5×, 1 s early", 1.5, now + 2000, -1000, true},
		{"1.5×, 1001 ms early", 1.5, now + 2000, -1001, false},
		{"1.5×, 4 s late", 1.5, now + 2000, 4000, true},
		{"1.5×, 4001 ms late", 1.5, now + 2000, 4001, false},
	} {
		st := announce(at(177000, c.rate, c.boundary+c.off), now)
		kept := st.StartItemID == "b" && st.StartAtMs == c.boundary+c.off
		if kept != c.keep {
			t.Errorf("%s: kept=%v, want %v (%+v)", c.name, kept, c.keep, st)
		}
		if !kept && !noSchedule(st) {
			t.Errorf("%s: a dropped announcement left fields behind: %+v", c.name, st)
		}
		if st.TrackStartedAtMs != now-int64(177000/c.rate) {
			t.Errorf("%s: the report did not anchor as usual: %d", c.name, st.TrackStartedAtMs)
		}
	}

	// Without sampledAtServerMs the arrival time is the sampled instant.
	if st := announce(at(177000, 1, now+3000+500), 0); st.StartItemID != "b" {
		t.Errorf("a fitting T judged at arrival was dropped: %+v", st)
	}
	if st := announce(at(177000, 1, now+3000+6000), 0); !noSchedule(st) {
		t.Errorf("a T 6 s past the boundary judged at arrival was kept: %+v", st)
	}

	// Whole-second durations: the track is really 180.4 s and the leader
	// sits at 180.2 s, past durationSec·1000. A T 200 ms + 540 ms past
	// the true end still fits.
	if st := announce(at(180200, 1, now+200+540), now); st.StartItemID != "b" {
		t.Errorf("a T past a whole-second duration was dropped: %+v", st)
	}

	// No duration: there is no boundary to judge against.
	if st := announce(State{ItemID: "a", IsPlaying: true, PositionMs: 0, StartAtMs: now + 60000, StartItemID: "b"}, now); st.StartItemID != "b" {
		t.Errorf("an announcement without a duration was judged: %+v", st)
	}

	// ⏮ restarts the track and the old fields are sent again: T is
	// three minutes early for the new trajectory.
	s := NewStore()
	T := now + 3000
	if st, _ := s.Report("p1", at(177500, 1, T), leaderOpts(now)); st.StartItemID != "b" {
		t.Fatalf("the announcement itself was refused: %+v", st)
	}
	st, out := s.Report("p1", at(0, 1, T), leaderOpts(now+500))
	if out != OutcomeOK || !noSchedule(st) {
		t.Fatalf("a restart kept the stale announcement: %v %+v", out, st)
	}
	if st.TrackStartedAtMs != now+500 || st.AnchorRevision != st.Revision {
		t.Fatalf("the restart did not re-anchor: %+v", st)
	}

	// A scrub back of 3 s with the old fields: T is 2.5 s early.
	s = NewStore()
	s.Report("p1", at(177500, 1, T), leaderOpts(now))
	if st, _ := s.Report("p1", at(174600, 1, T), leaderOpts(now+100)); !noSchedule(st) {
		t.Fatalf("a scrub back kept the stale announcement: %+v", st)
	}
}

// The trajectory window on its own, with no clock in it: inclusive at
// both ends, scaled by rate, a missing rate read as 1×, and nothing
// judged without a duration.
func TestNextStartTrajectoryWindowIsInclusive(t *testing.T) {
	const base = int64(1_000_000)
	// 180 s track at 177 s: the boundary is base + 3000 at 1×, + 1500
	// at 2×, + 6000 at 0.5×.
	for _, c := range []struct {
		rate     float64
		startAt  int64
		wantOff  int64
		wantFits bool
	}{
		{1, base + 2000, -1000, true},
		{1, base + 1999, -1001, false},
		{1, base + 7000, 4000, true},
		{1, base + 7001, 4001, false},
		{2, base + 500, -1000, true},
		{2, base + 499, -1001, false},
		{2, base + 5500, 4000, true},
		{2, base + 5501, 4001, false},
		{0.5, base + 5000, -1000, true},
		{0.5, base + 10000, 4000, true},
		{0.5, base + 10001, 4001, false},
	} {
		if off := nextStartOffsetMs(180, 177000, c.rate, base, c.startAt); off != c.wantOff {
			t.Errorf("rate %v, T base%+d: offset %d, want %d", c.rate, c.startAt-base, off, c.wantOff)
		}
		if fits := nextStartFitsTrajectory(180, 177000, c.rate, base, c.startAt); fits != c.wantFits {
			t.Errorf("rate %v, T base%+d: fits=%v, want %v", c.rate, c.startAt-base, fits, c.wantFits)
		}
	}
	if !nextStartFitsTrajectory(180, 177000, 0, base, base+7000) || nextStartFitsTrajectory(180, 177000, 0, base, base+7001) {
		t.Error("a missing rate is not read as 1×")
	}
	if !nextStartFitsTrajectory(0, 177000, 1, base, base+999_999) {
		t.Error("an announcement was judged without a duration")
	}
	// A position past durationSec·1000 puts the boundary before base.
	if off := nextStartOffsetMs(180, 180400, 1, base, base+600); off != 1000 {
		t.Errorf("past a whole-second duration: offset %d, want 1000", off)
	}
}

// An intent from the room cancels a next-track announcement, and the
// server remembers the (item, T) it cancelled: the leader's next report
// — the acknowledging one, which still repeats the fields if the leader
// has not withdrawn yet — cannot restore it, even though its
// trajectory still fits. A NEW T for the item (the leader re-armed) is
// judged on its own; the cancelled pair stays refused until T + 2 s and
// is accepted again after. Cancelling a start of the CURRENT item
// remembers nothing: a re-send of it is honoured, exactly as before.
func TestAnIntentCancelledNextItemScheduleIsNotRestoredByAReSend(t *testing.T) {
	now := time.Now().UnixMilli()
	s := NewStore()
	T := now + 2800
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177500,
		StartAtMs: T, StartItemID: "b"}, leaderOpts(now))
	if st.StartItemID != "b" {
		t.Fatalf("announcement not stored: %+v", st)
	}
	// A half-second nudge from the TV: the old T would still fit the new
	// trajectory, so only the memory can refuse it.
	st2, out := s.ApplyIntent("p1", "Living Room TV", IntentRequest{Type: IntentSetPositionMs, PositionMs: 177000, BasedOnRevision: st.Revision})
	if out != OutcomeOK || !noSchedule(st2) {
		t.Fatalf("a seek intent left the announcement standing: %v %+v", out, st2)
	}
	based := func(sampledAtMs int64) ReportOpts {
		o := leaderOpts(sampledAtMs)
		o.BasedOnRevision = rev(st2.Revision)
		return o
	}
	if !nextStartFitsTrajectory(180, 177100, 1, now+100, T) {
		t.Fatal("setup: the re-sent T should fit the acknowledging report's trajectory")
	}
	st3, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177100,
		StartAtMs: T, StartItemID: "b"}, based(now+100))
	if out != OutcomeOK || st3.LeaderAckRevision != st3.Revision {
		t.Fatalf("the acknowledging report: %v %+v", out, st3)
	}
	if !noSchedule(st3) {
		t.Fatalf("the acknowledging report restored the cancelled announcement: %+v", st3)
	}

	// The leader re-armed for the new trajectory: a new T stands.
	T2 := now + 3500
	st4, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177200,
		StartAtMs: T2, StartItemID: "b"}, based(now+200))
	if st4.StartItemID != "b" || st4.StartAtMs != T2 {
		t.Fatalf("a re-armed announcement was refused: %+v", st4)
	}
	// The cancelled pair itself stays refused.
	if st5, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177300,
		StartAtMs: T, StartItemID: "b"}, based(now+300)); !noSchedule(st5) {
		t.Fatalf("the cancelled (item, T) was accepted again before T + 2 s: %+v", st5)
	}

	// Until T + 2 s, then accepted. Without a duration there is no
	// trajectory to judge, so only the memory speaks.
	s = NewStore()
	s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 100000}, leaderOpts(now))
	s.mu.Lock()
	s.withdrawn["p1"] = withdrawnStart{itemID: "b", atMs: now - 1500} // refused until now + 500
	s.mu.Unlock()
	if st6, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 100100,
		StartAtMs: now - 1500, StartItemID: "b"}, leaderOpts(now+100)); !noSchedule(st6) {
		t.Fatalf("a cancelled pair was accepted inside T + 2 s: %+v", st6)
	}
	s.mu.Lock()
	s.withdrawn["p1"] = withdrawnStart{itemID: "b", atMs: now - 2001} // lapsed
	s.mu.Unlock()
	st7, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 100200,
		StartAtMs: now - 2001, StartItemID: "b"}, leaderOpts(now+200))
	if st7.StartItemID != "b" || st7.StartAtMs != now-2001 {
		t.Fatalf("the refusal outlived T + 2 s: %+v", st7)
	}
	s.mu.Lock()
	_, still := s.withdrawn["p1"]
	s.mu.Unlock()
	if still {
		t.Fatal("a lapsed refusal was not forgotten")
	}

	// A start of the CURRENT item cancelled by an intent: no memory, and
	// a re-send of it is honoured as it always was.
	s = NewStore()
	Tc := now + 1500
	sc, _ := s.Report("p1", State{ItemID: "c", IsPlaying: true, PositionMs: 0, StartAtMs: Tc, StartItemID: "c"}, leaderOpts(now))
	sc2, out := s.ApplyIntent("p1", "Living Room TV", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: sc.Revision})
	if out != OutcomeOK || !noSchedule(sc2) {
		t.Fatalf("a pause intent left the start standing: %v %+v", out, sc2)
	}
	s.mu.Lock()
	_, remembered := s.withdrawn["p1"]
	s.mu.Unlock()
	if remembered {
		t.Fatal("a cancelled start of the current item was remembered")
	}
	o := leaderOpts(now + 50)
	o.BasedOnRevision = rev(sc2.Revision)
	if sc3, _ := s.Report("p1", State{ItemID: "c", IsPlaying: true, PositionMs: 0, StartAtMs: Tc, StartItemID: "c"}, o); sc3.StartAtMs != Tc || sc3.TrackStartedAtMs != Tc {
		t.Fatalf("a re-sent start of the current item was not honoured: %+v", sc3)
	}
}

// The intent-cancel memory on its own: exactly the cancelled item AND
// instant, refused while now < T + 2 s.
func TestAWithdrawnStartRefusesOnlyTheSamePairUntilTPlus2s(t *testing.T) {
	w := withdrawnStart{itemID: "b", atMs: 10_000}
	for _, c := range []struct {
		item string
		at   int64
		now  int64
		want bool
	}{
		{"b", 10_000, 9_000, true},
		{"b", 10_000, 11_999, true},
		{"b", 10_000, 12_000, false},
		{"b", 10_001, 11_000, false},
		{"b", 9_999, 11_000, false},
		{"c", 10_000, 11_000, false},
	} {
		if got := w.refuses(c.item, c.at, c.now); got != c.want {
			t.Errorf("refuses(%q, %d) at %d = %v, want %v", c.item, c.at, c.now, got, c.want)
		}
	}
	if w.lapsed(11_999) || !w.lapsed(12_000) {
		t.Errorf("lapsed: %v at 11 999, %v at 12 000", w.lapsed(11_999), w.lapsed(12_000))
	}
}

// A next-track announcement never outlives its item or its leader: the
// next track's first report WITHOUT the fields anchors as an ordinary
// one (the leader must carry them for the pin), a device that takes the
// seat over inherits nothing, and a handover clears it for good.
func TestANextItemScheduleDoesNotOutliveItsItemOrItsLeader(t *testing.T) {
	now := time.Now().UnixMilli()
	announce := func() *Store {
		t.Helper()
		s := NewStore()
		if st, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177000,
			StartAtMs: now + 3500, StartItemID: "b"}, leaderOpts(now)); out != OutcomeOK || st.StartItemID != "b" {
			t.Fatalf("announce: %v %+v", out, st)
		}
		return s
	}

	// The item change without the fields: ordinary anchoring, nothing carried.
	s := announce()
	sampled := now + 3600
	st, _ := s.Report("p1", State{ItemID: "b", IsPlaying: true, DurationSec: 200, PositionMs: 0}, leaderOpts(sampled))
	if !noSchedule(st) || st.TrackStartedAtMs != sampled {
		t.Fatalf("the next track's first report without the fields: %+v (want its sampled zero %d)", st, sampled)
	}

	// Another device takes the seat over, playing the same item.
	s = announce()
	st, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177200},
		ReportOpts{DeviceID: "tv", BasedOnRevision: rev(0), HasPositionMs: true, TakeOver: true, SampledAtMs: now + 200})
	if out != OutcomeOK || st.LeaderTokenID != "tv" || !noSchedule(st) {
		t.Fatalf("a takeover inherited the old leader's announcement: %v %+v", out, st)
	}

	// A handover clears it, and the new leader's first report does not
	// bring it back.
	s = announce()
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	st, out = s.HandOver("p1", "phone", "", "tv1")
	if out != OutcomeOK || !noSchedule(st) {
		t.Fatalf("a handover left the announcement standing: %v %+v", out, st)
	}
	st, out = s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177500},
		ReportOpts{DeviceID: "tv1", DeviceLabel: "Living Room TV", BasedOnRevision: rev(st.Revision), HasPositionMs: true, SampledAtMs: now + 500})
	if out != OutcomeOK || !noSchedule(st) {
		t.Fatalf("the new leader inherited the announcement: %v %+v", out, st)
	}
}

// The leader leaving with followers vacates the seat. A next-track
// announcement is that leader's word about its own boundary and goes
// with the seat, so nobody arriving later arms on it; a start of the
// CURRENT item stays on the state, as before.
func TestALeaderVacatingTheSeatDropsANextItemSchedule(t *testing.T) {
	now := time.Now().UnixMilli()
	s := NewStore()
	s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177000,
		StartAtMs: now + 3500, StartItemID: "b"}, leaderOpts(now))
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	if out := s.EndBy("p1", "phone", "iPhone", "stopped"); out != EndVacated {
		t.Fatalf("leave: %v", out)
	}
	st, ok := s.Get("p1")
	if !ok || st.LeaderTokenID != "" || !st.EndedByLeader || !st.IsPlaying || st.ItemID != "a" {
		t.Fatalf("the seat was not vacated cleanly: %v %+v", ok, st)
	}
	if !noSchedule(st) {
		t.Fatalf("the vacated seat kept the leader's announcement: %+v", st)
	}

	s = NewStore()
	T := now + 1500
	s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 0, StartAtMs: T, StartItemID: "a"}, leaderOpts(now))
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	s.EndBy("p1", "phone", "iPhone", "stopped")
	st, _ = s.Get("p1")
	if st.StartAtMs != T || st.StartItemID != "a" || st.TrackStartedAtMs != T {
		t.Fatalf("a vacate dropped a start of the current item: %+v", st)
	}
}

// startAtMs <= 0 is no schedule: a negative one is not stored verbatim,
// and sibling fields left without an instant go with it. The report
// itself anchors as an ordinary one.
func TestANonPositiveStartAtMsIsNoSchedule(t *testing.T) {
	now := time.Now().UnixMilli()
	for i, c := range []State{
		{ItemID: "a", IsPlaying: true, PositionMs: 1000, StartAtMs: -5, StartItemID: "b"},
		{ItemID: "a", IsPlaying: true, PositionMs: 1000, StartAtMs: -5, StartPositionMs: 3000},
		{ItemID: "a", IsPlaying: true, PositionMs: 1000, StartAtMs: 0, StartPositionMs: 3000, StartItemID: "b"},
	} {
		st, out := NewStore().Report("p1", c, leaderOpts(now))
		if out != OutcomeOK {
			t.Fatalf("%d: refused: %v", i, out)
		}
		if !noSchedule(st) {
			t.Errorf("%d: a non-positive startAtMs left a schedule: %+v", i, st)
		}
		if st.TrackStartedAtMs != now-1000 {
			t.Errorf("%d: anchor %d, want the sampled zero %d", i, st.TrackStartedAtMs, now-1000)
		}
	}
}

// The boundary itself: the leader's first report of the announced
// track, carrying startItemId = that track and startAtMs = T up to
// ~1.5 s in the past, is anchored AT T whatever position it reports —
// judged at the instant it sampled its position, so a request that
// arrives late does not forfeit the pin. Without a sampled instant the
// arrival time judges, exactly as before.
func TestFirstReportOfTheAnnouncedTrackIsAnchoredAtT(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	opts := ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true, SampledAtMs: now - 3500}
	T := now - 900
	s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 177000,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"}, opts)

	opts.SampledAtMs = now - 300 // measured before the request arrived
	st, out := s.Report("p1", State{ItemID: "b", Title: "B", IsPlaying: true, PositionMs: 1200,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"}, opts)
	if out != OutcomeOK {
		t.Fatalf("refused: %v", out)
	}
	if st.TrackStartedAtMs != T {
		t.Fatalf("the announced track anchored at %d, want T=%d", st.TrackStartedAtMs, T)
	}
	if st.StartAtMs != T || st.StartItemID != "b" {
		t.Fatalf("the schedule did not stay on the state for the readers: %+v", st)
	}
	if st.ItemRevision != st.Revision || st.AnchorRevision != st.Revision {
		t.Fatalf("item/anchor revisions %d/%d, want the report's %d", st.ItemRevision, st.AnchorRevision, st.Revision)
	}
	s.mu.Lock()
	pos := s.extrapolatedLocked("p1", st, now)
	s.mu.Unlock()
	if pos < 850 || pos > 950 {
		t.Fatalf("position at now = %d, want ~900 (now − T), not the reported 1200", pos)
	}

	// 2.5 s after T by arrival but 1.5 s by the sampled instant: pinned.
	s = NewStore()
	T = now - 2500
	opts.SampledAtMs = now - 1000
	st, _ = s.Report("p1", State{ItemID: "b", IsPlaying: true, PositionMs: 1500,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"}, opts)
	if st.TrackStartedAtMs != T || st.StartAtMs != T {
		t.Fatalf("a late-arriving first report lost its pin: anchor %d startAt %d, want %d", st.TrackStartedAtMs, st.StartAtMs, T)
	}

	// Without sampledAtServerMs arrival is the judge, as before: spent.
	s = NewStore()
	opts.SampledAtMs = 0
	st, _ = s.Report("p1", State{ItemID: "b", IsPlaying: true, PositionMs: 1500,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"}, opts)
	if st.StartAtMs != 0 || st.StartItemID != "" {
		t.Fatalf("a schedule 2.5 s past T was honoured without a sampled instant: %+v", st)
	}
	if abs64(st.TrackStartedAtMs-(now-1500)) > 100 {
		t.Fatalf("the fallback anchor %d, want ~arrival − 1500 = %d", st.TrackStartedAtMs, now-1500)
	}
}

// A first report of the announced track sampled later than 2 s after T
// (a slow open, a stall) falls back to its sampled position: the
// schedule is dropped, not honoured, so followers measure the truth.
func TestALateFirstReportOfTheAnnouncedTrackFallsBackToTheSampledAnchor(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	T := now - 2500
	sampled := now - 100
	st, out := s.Report("p1", State{ItemID: "b", IsPlaying: true, PositionMs: 400,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"},
		ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true, SampledAtMs: sampled})
	if out != OutcomeOK {
		t.Fatalf("refused: %v", out)
	}
	if st.StartAtMs != 0 || st.StartItemID != "" || st.StartPositionMs != 0 {
		t.Fatalf("a spent schedule survived: %+v", st)
	}
	if st.TrackStartedAtMs != sampled-400 {
		t.Fatalf("fallback anchor %d, want sampledAt − position = %d", st.TrackStartedAtMs, sampled-400)
	}
}

// A follower that joined just before the boundary set anchorRefresh;
// the leader's scheduled first report still pins at T — the pin and
// the refresh share one case, so a disagreeing position cannot win —
// and the refresh is consumed.
func TestAPendingJoinRefreshDoesNotOverrideAScheduledAnchor(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	opts := ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true}
	s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 177000}, opts)
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	if st, _ := s.Get("p1"); !st.AnchorRefreshPending {
		t.Fatalf("a join did not ask for a refresh: %+v", st)
	}
	T := now - 700
	st, out := s.Report("p1", State{ItemID: "b", IsPlaying: true, PositionMs: 2600,
		StartAtMs: T, StartPositionMs: 0, StartItemID: "b"}, opts)
	if out != OutcomeOK {
		t.Fatalf("refused: %v", out)
	}
	if st.TrackStartedAtMs != T {
		t.Fatalf("the refresh overrode the scheduled anchor: %d, want %d", st.TrackStartedAtMs, T)
	}
	if st.AnchorRefreshPending {
		t.Fatal("the refresh was not consumed by the scheduled report")
	}
}

// Announcing the next track is not a track change: the item revision
// stands (pending intents stay valid) while the revision moves and
// every parked poll wakes to see the announcement.
func TestANextItemAnnouncementBumpsRevisionButNotItemRevision(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	opts := ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true}
	first, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 177000}, opts)

	woke := make(chan struct{})
	go func() {
		s.WaitForChange(context.Background(), "p1", first.Revision, 5*time.Second)
		close(woke)
	}()
	time.Sleep(50 * time.Millisecond)

	st, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 177100,
		StartAtMs: now + 3000, StartPositionMs: 0, StartItemID: "b"}, opts)
	if out != OutcomeOK {
		t.Fatalf("refused: %v", out)
	}
	if st.ItemRevision != first.ItemRevision {
		t.Fatalf("an announcement bumped the item revision: %d → %d", first.ItemRevision, st.ItemRevision)
	}
	if st.Revision <= first.Revision {
		t.Fatalf("an announcement did not bump the revision: %d → %d", first.Revision, st.Revision)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("a parked follower was not woken by the announcement")
	}
	// A seek chosen from the state before the announcement still lands.
	if _, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPositionMs, PositionMs: 100000, BasedOnRevision: first.Revision}); out != OutcomeOK {
		t.Fatalf("an intent based on the revision before the announcement was refused: %v", out)
	}
}

// Nothing starts after the party ended: the stop-all tombstone carries
// no schedule, and a start still holding stops at its start position.
func TestAStopAllTombstoneCarriesNoSchedule(t *testing.T) {
	s := NewStore()
	rev := int64(0)
	now := time.Now().UnixMilli()
	opts := ReportOpts{DeviceID: "phone", BasedOnRevision: &rev, HasPositionMs: true}

	// A next-track announcement.
	s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 177000,
		StartAtMs: now + 3000, StartPositionMs: 0, StartItemID: "b"}, opts)
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	if out := s.EndBy("p1", "phone", "iPhone", "stop-all"); out != EndAll {
		t.Fatalf("stop-all: %v", out)
	}
	st, ok := s.Get("p1")
	if !ok || !st.Ended {
		t.Fatalf("no tombstone: %v %+v", ok, st)
	}
	if st.StartAtMs != 0 || st.StartPositionMs != 0 || st.StartItemID != "" {
		t.Fatalf("the tombstone carries a schedule: %+v", st)
	}

	// A scheduled start of the current item, still holding.
	s = NewStore()
	s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 30000,
		StartAtMs: now + 1500, StartPositionMs: 30000}, opts)
	s.EndBy("p1", "phone", "iPhone", "stop-all")
	st, _ = s.Get("p1")
	if st.StartAtMs != 0 || st.StartItemID != "" || st.StartPositionMs != 0 {
		t.Fatalf("the tombstone carries a schedule: %+v", st)
	}
	if st.PositionMs != 30000 || st.IsPlaying {
		t.Fatalf("the tombstone did not stop at the held start position: %+v", st)
	}
}

// The truthful pin: the leader's first report of the announced track
// pins T before its clock rolls; its first ROLLING report pins its
// measured audible zero Z = sampledAt − position ÷ rate instead. The
// server takes Z — judged against the sampled instant, it is within 2 s
// — even when T itself is more than 1.5 s gone (a leader whose open
// overran by a second), and ordinary reports keep Z.
func TestTheMeasuredPinReAnchorsTheAnnouncedTrack(t *testing.T) {
	s := NewStore()
	now := time.Now().UnixMilli()
	T := now - 1600
	s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 177000}, leaderOpts(now-2000))
	st, _ := s.Report("p1", State{ItemID: "b", IsPlaying: true, DurationSec: 200, PositionMs: 0,
		StartAtMs: T, StartItemID: "b"}, leaderOpts(T+1005))
	if st.TrackStartedAtMs != T || st.StartAtMs != T || st.ItemRevision != st.Revision {
		t.Fatalf("the first report of the announced track did not pin T: %+v", st)
	}
	Z := T + 1060 // it played 1060 ms late
	st, _ = s.Report("p1", State{ItemID: "b", IsPlaying: true, DurationSec: 200, PositionMs: 540,
		StartAtMs: Z, StartItemID: "b"}, leaderOpts(now))
	if st.TrackStartedAtMs != Z || st.StartAtMs != Z || st.AnchorRevision != st.Revision {
		t.Fatalf("the measured zero was not taken: anchor %d startAt %d, want %d (%+v)", st.TrackStartedAtMs, st.StartAtMs, Z, st)
	}
	st, _ = s.Report("p1", State{ItemID: "b", IsPlaying: true, DurationSec: 200, PositionMs: 1540}, leaderOpts(now+1000))
	if st.TrackStartedAtMs != Z || !noSchedule(st) {
		t.Fatalf("an ordinary report after the pin: %+v (want the anchor kept at %d)", st, Z)
	}
}

// Phone 1197 — the build in people's hands — never announces a next
// track. It schedules only the item it is starting (startItemId ==
// itemId; T = its sample + 1.5 s on a skip, + 0.7 s at a track's end),
// once, and every other report carries no schedule at all. None of the
// v2 rules may touch that: this walks a 1197 session — skip, the first
// ordinary report, pause, resume, scrub, the natural end, a pause from
// the room while the start is held and the leader's ack, a resume, a
// skip and the leader leaving with a follower — and checks each state
// against what the server produced for it before v2. (These same
// expectations pass against that code, commit bfc332a, too.)
func TestPhone1197StyleReportsBehaveExactlyAsAtHead(t *testing.T) {
	now := time.Now().UnixMilli()
	var s *Store
	position := func(st State, at int64) int64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.extrapolatedLocked("p1", st, at)
	}
	basedOn := int64(0)
	report := func(st State, sampledAtMs int64) State {
		t.Helper()
		o := leaderOpts(sampledAtMs)
		o.BasedOnRevision = rev(basedOn)
		got, out := s.Report("p1", st, o)
		if out != OutcomeOK {
			t.Fatalf("report refused: %v", out)
		}
		return got
	}
	plain := func(item string, playing bool, durSec int, posMs int64) State {
		return State{Source: "plex", ItemID: item, Title: item, IsPlaying: playing, DurationSec: durSec,
			PositionMs: posMs, Rate: 1}
	}
	scheduled := func(item string, durSec int, startAt int64) State {
		st := plain(item, true, durSec, 0)
		st.StartAtMs, st.StartPositionMs, st.StartItemID = startAt, 0, item
		return st
	}

	s = NewStore()
	// 1. a plays: an ordinary report.
	r1 := report(plain("a", true, 180, 60000), now-4000)
	if !noSchedule(r1) || r1.TrackStartedAtMs != now-64000 || r1.AnchorRevision != r1.Revision || r1.ItemRevision != r1.Revision {
		t.Fatalf("1: %+v", r1)
	}
	// 2. A skip: b scheduled 1.5 s after its sample, held at 0:00 until T.
	T := now + 1450
	r2 := report(scheduled("b", 200, T), now-50)
	if r2.StartAtMs != T || r2.StartItemID != "b" || r2.StartPositionMs != 0 || r2.TrackStartedAtMs != T ||
		r2.ItemRevision != r2.Revision || r2.AnchorRevision != r2.Revision {
		t.Fatalf("2: %+v", r2)
	}
	if p := position(r2, now); p != 0 {
		t.Fatalf("2: position before T = %d, want 0", p)
	}
	if p := position(r2, T+2000); p != 2000 {
		t.Fatalf("2: position 2 s after T = %d, want 2000", p)
	}
	// 3. b's first ordinary report, after T: the schedule clears, the
	// anchor stays at T (inside the 2 s window).
	r3 := report(plain("b", true, 200, 150), T+200)
	if !noSchedule(r3) || r3.TrackStartedAtMs != T || r3.AnchorRevision != r2.AnchorRevision || r3.ItemRevision != r2.ItemRevision {
		t.Fatalf("3: %+v", r3)
	}
	// 4. A pause re-anchors at the paused position; 5. the resume too.
	r4 := report(plain("b", false, 200, 1000), T+1100)
	if !noSchedule(r4) || r4.TrackStartedAtMs != T+100 || r4.AnchorRevision != r4.Revision || position(r4, T+5000) != 1000 {
		t.Fatalf("4: %+v", r4)
	}
	r5 := report(plain("b", true, 200, 1000), T+2000)
	if !noSchedule(r5) || r5.TrackStartedAtMs != T+1000 || r5.AnchorRevision != r5.Revision {
		t.Fatalf("5: %+v", r5)
	}
	// 6. A scrub to 2:00.
	r6 := report(plain("b", true, 200, 120000), T+2500)
	if !noSchedule(r6) || r6.TrackStartedAtMs != T+2500-120000 || r6.AnchorRevision != r6.Revision {
		t.Fatalf("6: %+v", r6)
	}

	s = NewStore()
	basedOn = 0
	// 7. The natural end of a: 'advance' schedules c 0.7 s after its
	// sample. (The samples run a second ahead of the wall clock here, so
	// the pause below surely lands while c is still held for T.)
	report(plain("a", true, 180, 179000), now)
	T = now + 1000 + 700
	r7 := report(scheduled("c", 240, T), now+1000)
	if r7.StartAtMs != T || r7.StartItemID != "c" || r7.TrackStartedAtMs != T || r7.ItemRevision != r7.Revision {
		t.Fatalf("7: %+v", r7)
	}
	// 8. A pause from the room while c is held for T: the start is
	// cancelled and the position stays at the start position.
	r8, out := s.ApplyIntent("p1", "Living Room TV", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: r7.Revision})
	if out != OutcomeOK || r8.IsPlaying || !noSchedule(r8) || r8.PositionMs != 0 || r8.LastIntentRevision != r8.Revision {
		t.Fatalf("8: %v %+v", out, r8)
	}
	// 9. The leader's acknowledging report: paused at 0:00, no schedule.
	basedOn = r8.Revision
	r9 := report(plain("c", false, 240, 0), now+1030)
	if !noSchedule(r9) || r9.TrackStartedAtMs != now+1030 || r9.LeaderAckRevision != r9.Revision || r9.AnchorRevision != r9.Revision {
		t.Fatalf("9: %+v", r9)
	}
	// 10. Play on the phone: an ordinary resume.
	r10 := report(plain("c", true, 240, 0), now+1500)
	if !noSchedule(r10) || r10.TrackStartedAtMs != now+1500 || r10.AnchorRevision != r10.Revision {
		t.Fatalf("10: %+v", r10)
	}
	// 11. A skip to d with a follower present, and the leader leaves
	// before T: the seat empties and the start of the current item stays.
	T = now + 1600 + 1500
	r11 := report(scheduled("d", 300, T), now+1600)
	if r11.StartAtMs != T || r11.StartItemID != "d" || r11.TrackStartedAtMs != T {
		t.Fatalf("11: %+v", r11)
	}
	s.Join("p1", "tv1", "Living Room TV", RoleFollower)
	if out := s.EndBy("p1", "phone", "iPhone", "stopped"); out != EndVacated {
		t.Fatalf("11: leave: %v", out)
	}
	r12, ok := s.Get("p1")
	if !ok || r12.LeaderTokenID != "" || !r12.IsPlaying || r12.StartAtMs != T || r12.StartItemID != "d" ||
		r12.StartPositionMs != 0 || r12.TrackStartedAtMs != T {
		t.Fatalf("12: %v %+v", ok, r12)
	}
}
