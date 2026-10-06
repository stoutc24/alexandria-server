package nowplaying

// The party line's findings from the 2026-10 security review: #3, a
// refused report replaced the party's queue anyway, and #2, one device
// could grow a profile's follower list without end.

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// queueIDs is the profile's served queue as comma-joined item ids, ""
// when there is none.
func queueIDs(s *Store, profileID string) string {
	items, ok := s.Queue(profileID)
	if !ok {
		return ""
	}
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ItemID
	}
	return strings.Join(ids, ",")
}

func queueOf(ids ...string) *[]QueueItem {
	q := make([]QueueItem, len(ids))
	for i, id := range ids {
		q[i] = QueueItem{ItemID: id}
	}
	return &q
}

// #3: SetQueue ran before the seat check, so a device another device's
// seat refused replaced the queue of the party it was refused from.
func TestARefusedReportLeavesThePartysQueueAlone(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), Queue: queueOf("a", "b")}
	st, out := s.Report("p1", State{ItemID: "a", IsPlaying: true}, phone)
	if out != OutcomeOK || queueIDs(s, "p1") != "a,b" {
		t.Fatalf("the leader's queue did not land: %v %q", out, queueIDs(s, "p1"))
	}

	tv := ReportOpts{DeviceID: "tok-tv", BasedOnRevision: rev(st.Revision), Queue: queueOf("evil")}
	got, out := s.Report("p1", State{ItemID: "evil", IsPlaying: true}, tv)
	if out != OutcomeSeatHeld {
		t.Fatalf("second device: %v, want seat-held", out)
	}
	if q := queueIDs(s, "p1"); q != "a,b" {
		t.Fatalf("a seat-held report replaced the party's queue: %q", q)
	}
	if got.QueueRevision != st.QueueRevision {
		t.Fatalf("a seat-held report moved the queue revision: %d -> %d",
			st.QueueRevision, got.QueueRevision)
	}

	// A legacy publisher (no basedOnRevision) is ignored the same way.
	legacy := ReportOpts{DeviceID: "tok-old", Queue: queueOf("old")}
	if _, out := s.Report("p1", State{ItemID: "old"}, legacy); out != OutcomeIgnored {
		t.Fatalf("legacy publisher: %v, want ignored", out)
	}
	if q := queueIDs(s, "p1"); q != "a,b" {
		t.Fatalf("an ignored report replaced the party's queue: %q", q)
	}

	// The leader's own heartbeat keeps its queue: no other device's held
	// queue lands on it.
	phone.Queue = nil
	phone.BasedOnRevision = rev(got.Revision)
	if _, out := s.Report("p1", State{ItemID: "a", IsPlaying: true}, phone); out != OutcomeOK {
		t.Fatalf("leader heartbeat: %v", out)
	}
	if q := queueIDs(s, "p1"); q != "a,b" {
		t.Fatalf("another device's held queue landed on the leader's heartbeat: %q", q)
	}
}

// A TV counts its queue as sent whatever the answer, so the take-over
// after a seat-held refusal carries none. The queue it was refused with
// lands with the report that takes the seat, and only then.
func TestAHeldQueueLandsWhenItsDeviceTakesTheSeat(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), Queue: queueOf("a", "b")}
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: false}, phone)

	tv := ReportOpts{DeviceID: "tok-tv", BasedOnRevision: rev(st.Revision), Queue: queueOf("x", "y")}
	refused, out := s.Report("p1", State{ItemID: "x", IsPlaying: true}, tv)
	if out != OutcomeSeatHeld || queueIDs(s, "p1") != "a,b" {
		t.Fatalf("seat-held: %v, queue %q", out, queueIDs(s, "p1"))
	}

	tv.Queue = nil
	tv.TakeOver = true
	tv.BasedOnRevision = rev(refused.Revision)
	took, out := s.Report("p1", State{ItemID: "x", IsPlaying: true}, tv)
	if out != OutcomeOK || took.LeaderTokenID != "tok-tv" {
		t.Fatalf("take-over: %v %+v", out, took)
	}
	if q := queueIDs(s, "p1"); q != "x,y" {
		t.Fatalf("the TV's held queue did not land with its take-over: %q", q)
	}
	if took.QueueRevision <= st.QueueRevision {
		t.Fatalf("the queue revision did not move with the take-over: %d -> %d",
			st.QueueRevision, took.QueueRevision)
	}

	// Spent: it never lands again over a newer queue.
	tv.TakeOver = false
	tv.Queue = queueOf("z")
	tv.BasedOnRevision = rev(took.Revision)
	again, _ := s.Report("p1", State{ItemID: "z", IsPlaying: true}, tv)
	tv.Queue = nil
	tv.BasedOnRevision = rev(again.Revision)
	s.Report("p1", State{ItemID: "z", IsPlaying: true}, tv)
	if q := queueIDs(s, "p1"); q != "z" {
		t.Fatalf("a spent held queue came back: %q", q)
	}
}

func TestAHeldQueueGoesWithTheSession(t *testing.T) {
	s := NewStore()
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)})
	s.Report("p1", State{ItemID: "x", IsPlaying: true},
		ReportOpts{DeviceID: "tok-tv", BasedOnRevision: rev(st.Revision), Queue: queueOf("x")})
	s.Clear("p1")
	s.mu.RLock()
	_, kept := s.held["p1"]
	s.mu.RUnlock()
	if kept {
		t.Fatal("a held queue outlived its session")
	}
}

// A stale report comes from a device that passed the seat check, so its
// queue still lands, as it always has, and the refusal says so.
func TestAStaleReportsQueueStillLands(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), HasPositionMs: true, Queue: queueOf("a", "b")}
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 10_000, DurationSec: 300}, phone)
	paused, out := s.ApplyIntent("p1", "TV", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: st.Revision})
	if out != OutcomeOK {
		t.Fatalf("pause: %v", out)
	}

	phone.BasedOnRevision = rev(st.Revision)
	phone.Queue = queueOf("b", "a")
	got, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 12_000}, phone)
	if out != OutcomeStale {
		t.Fatalf("a heartbeat based before the pause: %v, want stale", out)
	}
	if q := queueIDs(s, "p1"); q != "b,a" {
		t.Fatalf("the leader's queue was dropped with its stale report: %q", q)
	}
	if got.QueueRevision <= paused.QueueRevision || got.IsPlaying {
		t.Fatalf("stale refusal: queue revision %d (was %d), playing %v",
			got.QueueRevision, paused.QueueRevision, got.IsPlaying)
	}
}

// The second review: a take-over refused as stale must not land its
// queue under the leader still holding the seat. It is held, and lands
// when the take-over goes through.
func TestAStaleTakeOverHoldsItsQueue(t *testing.T) {
	s := NewStore()
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), HasPositionMs: true, Queue: queueOf("a", "b")}
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 10_000, DurationSec: 300}, phone)
	if _, out := s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: st.Revision}); out != OutcomeOK {
		t.Fatalf("pause: %v", out)
	}

	tv := ReportOpts{DeviceID: "tok-tv", BasedOnRevision: rev(st.Revision), TakeOver: true, Queue: queueOf("x", "y")}
	got, out := s.Report("p1", State{ItemID: "x", IsPlaying: true}, tv)
	if out != OutcomeStale || got.LeaderTokenID != "tok-phone" {
		t.Fatalf("a take-over based before the pause: %v, leader %q; want stale, the phone", out, got.LeaderTokenID)
	}
	if q := queueIDs(s, "p1"); q != "a,b" {
		t.Fatalf("a stale take-over landed its queue under the phone: %q", q)
	}

	tv.Queue = nil
	tv.BasedOnRevision = rev(got.Revision)
	if took, out := s.Report("p1", State{ItemID: "x", IsPlaying: true}, tv); out != OutcomeOK || took.LeaderTokenID != "tok-tv" {
		t.Fatalf("the take-over, caught up: %v %+v", out, took)
	}
	if q := queueIDs(s, "p1"); q != "x,y" {
		t.Fatalf("the held queue did not land with the take-over: %q", q)
	}
}

// Each refused device keeps its own held queue: a second refusal no
// longer drops the first one's.
func TestEachRefusedDeviceHoldsItsOwnQueue(t *testing.T) {
	s := NewStore()
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: false},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), Queue: queueOf("a", "b")})
	for _, tv := range []string{"tv1", "tv2"} {
		if _, out := s.Report("p1", State{ItemID: tv, IsPlaying: true},
			ReportOpts{DeviceID: tv, BasedOnRevision: rev(st.Revision), Queue: queueOf(tv)}); out != OutcomeSeatHeld {
			t.Fatalf("%s: %v, want seat-held", tv, out)
		}
	}
	cur, _ := s.Get("p1")
	if _, out := s.Report("p1", State{ItemID: "tv1", IsPlaying: true},
		ReportOpts{DeviceID: "tv1", BasedOnRevision: rev(cur.Revision), TakeOver: true}); out != OutcomeOK {
		t.Fatalf("tv1's take-over: %v", out)
	}
	if q := queueIDs(s, "p1"); q != "tv1" {
		t.Fatalf("tv1 leads with queue %q, want its own held one", q)
	}
}

// At most maxHeldQueues per profile, the oldest dropped first.
func TestHeldQueuesAreCappedPerProfile(t *testing.T) {
	s := NewStore()
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: false},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), Queue: queueOf("a")})
	for i := 0; i <= maxHeldQueues; i++ {
		s.Report("p1", State{ItemID: "x", IsPlaying: true}, ReportOpts{
			DeviceID: fmt.Sprintf("tv%d", i), BasedOnRevision: rev(st.Revision),
			Queue: queueOf(fmt.Sprintf("q%d", i))})
	}
	s.mu.RLock()
	n := len(s.held["p1"])
	oldest := s.held["p1"][0].deviceID
	s.mu.RUnlock()
	if n != maxHeldQueues || oldest != "tv1" {
		t.Fatalf("%d held, oldest %q; want %d, tv1 (tv0's dropped)", n, oldest, maxHeldQueues)
	}
}

// #2: the follower list is capped per profile; expired entries make room.
func TestFollowersAreCappedPerProfile(t *testing.T) {
	s := NewStore()
	for i := 0; i < MaxFollowersPerProfile; i++ {
		if _, ok := s.JoinWithDrift("p1", fmt.Sprintf("tv-%d", i), "TV", RoleFollower, nil, 0); !ok {
			t.Fatalf("follower %d of %d turned away", i+1, MaxFollowersPerProfile)
		}
	}
	if _, ok := s.JoinWithDrift("p1", "one-too-many", "TV", RoleFollower, nil, 0); ok {
		t.Fatal("a follower past the cap was let in")
	}
	// A device already on the list still heartbeats, and may change role.
	if _, ok := s.JoinWithDrift("p1", "tv-0", "TV", RoleViewer, nil, 0); !ok {
		t.Fatal("a listed device's heartbeat was refused at the cap")
	}
	// Another profile has a list of its own.
	if _, ok := s.JoinWithDrift("p2", "tv-0", "TV", RoleFollower, nil, 0); !ok {
		t.Fatal("the cap spilled into another profile")
	}

	// Everyone goes quiet: swept out, and the newcomer gets in.
	s.mu.Lock()
	for k, e := range s.followers["p1"] {
		e.last = time.Now().Add(-2 * followerTTL)
		s.followers["p1"][k] = e
	}
	s.mu.Unlock()
	if _, ok := s.JoinWithDrift("p1", "newcomer", "TV", RoleFollower, nil, 0); !ok {
		t.Fatal("a newcomer was turned away by expired entries")
	}
	s.mu.RLock()
	n := len(s.followers["p1"])
	s.mu.RUnlock()
	if n != 1 {
		t.Fatalf("%d entries after the sweep, want just the newcomer", n)
	}
}
