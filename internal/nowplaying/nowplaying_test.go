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
	st, out := s.HandOver("p", "phone", "tv")
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

	if _, out := s.HandOver("p1", "tv1", "phone"); out != OutcomeNotLeader {
		t.Fatalf("a follower handed the seat over: %v", out)
	}
	if _, out := s.HandOver("p1", "phone", "nobody"); out != OutcomeNoSuchFollower {
		t.Fatalf("an unknown target was accepted: %v", out)
	}
	if _, out := s.HandOver("nobody", "x", "y"); out != OutcomeNoSession {
		t.Fatalf("handover without a session: %v", out)
	}

	st, out := s.HandOver("p1", "phone", "tv1")
	if out != OutcomeOK || st.LeaderTokenID != "tv1" || st.LeaderLabel != "Living Room TV" || st.HandedOverAtMs == 0 {
		t.Fatalf("handover: %v %+v", out, st)
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
