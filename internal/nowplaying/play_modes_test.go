package nowplaying

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// The leader's play modes (owner, 2026-10-31: Shuffle and Repeat on the
// Remote, carried out by the leading device on its own queue).

func boolp(v bool) *bool { return &v }

// leadWithModes starts a party: the phone leads "a" (180 s, at 60 s),
// reporting the play modes given (nil / "" = leaves that one out).
func leadWithModes(t *testing.T, s *Store, shuffle *bool, repeat string) State {
	t.Helper()
	st, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
		PositionMs: 60_000, Shuffle: shuffle, Repeat: repeat}, leaderOpts(0))
	if out != OutcomeOK {
		t.Fatalf("the leader's report: %v", out)
	}
	return st
}

// published is a state as a device receives it.
func published(t *testing.T, st State) map[string]any {
	t.Helper()
	b, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// assertNoModes: neither mode is set, and neither key is published — a
// Remote shows no Shuffle or Repeat button.
func assertNoModes(t *testing.T, what string, st State) {
	t.Helper()
	if st.Shuffle != nil || st.Repeat != "" {
		t.Fatalf("%s: shuffle %v, repeat %q; want both absent", what, st.Shuffle, st.Repeat)
	}
	m := published(t, st)
	for _, k := range []string{"shuffle", "repeat"} {
		if v, has := m[k]; has {
			t.Fatalf("%s: the state publishes %s = %v", what, k, v)
		}
	}
}

// The modes are the leader's: what its report says is what every device
// reads, shuffle off and repeat off included (both are modes it can do).
func TestALeaderReportCarriesItsPlayModes(t *testing.T) {
	s := NewStore()
	st := leadWithModes(t, s, boolp(true), RepeatAll)
	if st.Shuffle == nil || !*st.Shuffle || st.Repeat != RepeatAll {
		t.Fatalf("the report's modes were not kept: shuffle %v repeat %q", st.Shuffle, st.Repeat)
	}
	got, _ := s.Get("p1")
	if m := published(t, got); m["shuffle"] != true || m["repeat"] != "all" {
		t.Fatalf("a follower reads shuffle %v, repeat %v", m["shuffle"], m["repeat"])
	}

	o := leaderOpts(0)
	o.BasedOnRevision = rev(got.Revision)
	st, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
		PositionMs: 61_000, Shuffle: boolp(false), Repeat: RepeatOff}, o)
	if out != OutcomeOK {
		t.Fatalf("heartbeat: %v", out)
	}
	if m := published(t, st); m["shuffle"] != false || m["repeat"] != "off" {
		t.Fatalf("modes switched off must still be published: shuffle %v, repeat %v", m["shuffle"], m["repeat"])
	}
}

// A report without the modes leaves them unknown — never false or "off",
// which would show a Remote buttons nobody can carry out — and nothing of
// another device's is carried over to it.
func TestAnOldLeadersReportLeavesThePlayModesUnknown(t *testing.T) {
	s := NewStore()
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true}, leaderOpts(0))
	assertNoModes(t, "a leader that predates them", st)
	got, _ := s.Get("p1")
	assertNoModes(t, "a follower's read of it", got)

	// A leader that has them loses the seat to one that predates them.
	s = NewStore()
	st = leadWithModes(t, s, boolp(true), RepeatOne)
	old := ReportOpts{DeviceID: "tok-old-tv", DeviceLabel: "Old TV", BasedOnRevision: rev(st.Revision),
		TakeOver: true, HasPositionMs: true}
	got, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, PositionMs: 61_000}, old)
	if out != OutcomeOK || got.LeaderTokenID != "tok-old-tv" {
		t.Fatalf("take-over: %v %+v", out, got)
	}
	assertNoModes(t, "after an older device took the seat", got)
	got, _ = s.Get("p1")
	assertNoModes(t, "a follower's read after the take-over", got)

	// The same leader leaving them out: exactly what its report says.
	s = NewStore()
	st = leadWithModes(t, s, boolp(false), RepeatOff)
	o := leaderOpts(0)
	o.BasedOnRevision = rev(st.Revision)
	got, _ = s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 62_000}, o)
	assertNoModes(t, "a report that leaves them out", got)

	// A repeat that is not one of the three reads as absent, and the
	// heartbeat itself is never refused for it.
	for _, bad := range []string{"ALL", "shuffle", " one", "true", "Off"} {
		o.BasedOnRevision = rev(got.Revision)
		got, out = s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
			PositionMs: 63_000, Shuffle: boolp(true), Repeat: bad}, o)
		if out != OutcomeOK || got.Repeat != "" || got.Shuffle == nil || !*got.Shuffle {
			t.Fatalf("repeat %q: %v, shuffle %v, repeat %q", bad, out, got.Shuffle, got.Repeat)
		}
		if _, has := published(t, got)["repeat"]; has {
			t.Fatalf("repeat %q was published", bad)
		}
	}
}

// A press is carried out by the leader the way a pause is: the state
// shows it at once and wakes the leader's long-poll; the leader's
// heartbeat from before the press is refused stale (the press is not
// lost to it); its report that has seen the press is the ack and has the
// last word on the mode.
func TestAPlayModePressIsCarriedOutByTheLeader(t *testing.T) {
	s := NewStore()
	st := leadWithModes(t, s, boolp(false), RepeatOff)

	woke := make(chan struct{})
	go func() {
		s.WaitForChange(context.Background(), "p1", st.Revision, 5*time.Second)
		close(woke)
	}()
	for i := 0; s.Waiting("p1") == 0 && i < 400; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Waiting("p1") == 0 {
		t.Fatal("setup: the leader's long-poll never parked")
	}

	on, out := s.ApplyIntent("p1", "Kitchen Remote", IntentRequest{Type: IntentSetShuffle, Shuffle: true,
		BasedOnRevision: st.Revision})
	if out != OutcomeOK || on.Shuffle == nil || !*on.Shuffle || on.Repeat != RepeatOff {
		t.Fatalf("setShuffle: %v shuffle %v repeat %q", out, on.Shuffle, on.Repeat)
	}
	if on.LastIntent == nil || on.LastIntent.Type != IntentSetShuffle || on.LastIntent.ByLabel != "Kitchen Remote" ||
		on.LastIntentRevision != on.Revision || on.IntentSeq != st.IntentSeq+1 || on.Revision <= st.Revision {
		t.Fatalf("intent bookkeeping: %+v (lastIntent %+v)", on, on.LastIntent)
	}
	// What a leader reads as "an intent I have not carried out".
	if on.LastIntentRevision <= on.LeaderAckRevision {
		t.Fatalf("the leader would not see the press: lastIntentRevision %d, leaderAckRevision %d",
			on.LastIntentRevision, on.LeaderAckRevision)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("the press did not wake the leader's long-poll")
	}

	o := leaderOpts(0)
	o.BasedOnRevision = rev(st.Revision)
	got, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
		PositionMs: 61_000, Shuffle: boolp(false), Repeat: RepeatOff}, o)
	if out != OutcomeStale || got.Shuffle == nil || !*got.Shuffle {
		t.Fatalf("the heartbeat from before the press: %v, shuffle %v", out, got.Shuffle)
	}
	if cur, _ := s.Get("p1"); cur.Shuffle == nil || !*cur.Shuffle {
		t.Fatal("a heartbeat from before the press put the old mode back")
	}

	o.BasedOnRevision = rev(on.Revision)
	acked, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
		PositionMs: 61_100, Shuffle: boolp(true), Repeat: RepeatOff}, o)
	if out != OutcomeOK || acked.LeaderAckRevision != acked.Revision || acked.Shuffle == nil || !*acked.Shuffle {
		t.Fatalf("the ack: %v %+v", out, acked)
	}

	// The same press again: already so — no bump, no intent.
	again, out := s.ApplyIntent("p1", "Kitchen Remote", IntentRequest{Type: IntentSetShuffle, Shuffle: true})
	if out != OutcomeOK || again.Revision != acked.Revision || again.IntentSeq != acked.IntentSeq {
		t.Fatalf("a satisfied press: %v, revision %d → %d, seq %d → %d", out, acked.Revision, again.Revision,
			acked.IntentSeq, again.IntentSeq)
	}

	// Repeat the same way; the leader's report has the last word — one
	// that can only repeat all says so, and that is what everyone reads.
	one, out := s.ApplyIntent("p1", "Kitchen Remote", IntentRequest{Type: IntentSetRepeat, Repeat: RepeatOne})
	if out != OutcomeOK || one.Repeat != RepeatOne || one.LastIntent == nil || one.LastIntent.Type != IntentSetRepeat ||
		one.LastIntentRevision != one.Revision {
		t.Fatalf("setRepeat: %v %+v", out, one)
	}
	o.BasedOnRevision = rev(one.Revision)
	got, out = s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
		PositionMs: 61_300, Shuffle: boolp(true), Repeat: RepeatAll}, o)
	if out != OutcomeOK || got.Repeat != RepeatAll || got.LeaderAckRevision != got.Revision {
		t.Fatalf("the leader's answer to repeat one: %v repeat %q", out, got.Repeat)
	}
	if cur, _ := s.Get("p1"); cur.Repeat != RepeatAll {
		t.Fatalf("a follower reads repeat %q, want the leader's %q", cur.Repeat, RepeatAll)
	}
}

// THE DECISION: a play-mode press does not move the playing song, so it
// leaves everything that says where the music is alone — the position,
// updatedAtMs, the anchor and its revision, the item revision and a
// scheduled start (of the current item, or the next track announced
// ahead). ApplyIntent's other intents zero a schedule and remember a
// cancelled next-track (item, T) so the leader cannot restore it until
// T + 2 s (TestAnIntentCancelledNextItemScheduleIsNotRestoredByAReSend);
// done for a mode press, that would cost the boundary its scheduled start
// whenever the next track did not change. The leader's own report keeps,
// withdraws or replaces the announcement, as after a queue edit. Its ack
// re-anchors from its own clock, as after every intent.
func TestAPlayModePressLeavesTheMusicWhereItIs(t *testing.T) {
	now := time.Now().UnixMilli()
	where := func(st State) [10]int64 {
		playing := int64(0)
		if st.IsPlaying {
			playing = 1
		}
		return [10]int64{st.PositionMs, st.UpdatedAtMs, st.TrackStartedAtMs, st.AnchorRevision,
			st.ItemRevision, st.StartAtMs, st.StartPositionMs, int64(len(st.StartItemID)), playing,
			int64(st.QueueIndex)}
	}
	anchorOf := func(s *Store) int64 {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.anchor["p1"]
	}
	remembered := func(s *Store) bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		_, ok := s.withdrawn["p1"]
		return ok
	}

	// A start of the CURRENT item, still holding at its position until T.
	s := NewStore()
	T := now + 1500
	st, _ := s.Report("p1", State{ItemID: "c", IsPlaying: true, PositionMs: 0, StartAtMs: T, StartItemID: "c",
		Shuffle: boolp(false), Repeat: RepeatOff}, leaderOpts(now))
	if st.StartAtMs != T || st.TrackStartedAtMs != T {
		t.Fatalf("setup: the start was not pinned at T: %+v", st)
	}
	before, anchor := where(st), anchorOf(s)
	for _, in := range []IntentRequest{{Type: IntentSetShuffle, Shuffle: true}, {Type: IntentSetRepeat, Repeat: RepeatOne}} {
		got, out := s.ApplyIntent("p1", "Remote", in)
		if out != OutcomeOK {
			t.Fatalf("%s: %v", in.Type, out)
		}
		if where(got) != before || anchorOf(s) != anchor {
			t.Fatalf("%s moved the music: %v → %v, anchor %d → %d", in.Type, before, where(got), anchor, anchorOf(s))
		}
		s.mu.Lock()
		held := s.extrapolatedLocked("p1", s.m["p1"], now+500)
		s.mu.Unlock()
		if held != 0 {
			t.Fatalf("%s: the start no longer holds at its position until T (%d ms)", in.Type, held)
		}
	}

	// The NEXT track announced ahead (3 s left of "a"; "b" starts at T).
	s = NewStore()
	T = now + 2800
	st, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177500,
		StartAtMs: T, StartItemID: "b", Shuffle: boolp(false), Repeat: RepeatAll}, leaderOpts(now))
	if st.StartItemID != "b" || st.StartAtMs != T {
		t.Fatalf("setup: announcement not stored: %+v", st)
	}
	before, anchor = where(st), anchorOf(s)
	off, out := s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetRepeat, Repeat: RepeatOff})
	if out != OutcomeOK || off.StartItemID != "b" || off.StartAtMs != T {
		t.Fatalf("a mode press cancelled the next-track announcement: %v %+v", out, off)
	}
	if where(off) != before || anchorOf(s) != anchor || remembered(s) {
		t.Fatalf("a mode press moved the music: %v → %v, anchor %d → %d, remembered %v",
			before, where(off), anchor, anchorOf(s), remembered(s))
	}
	// Repeat all → off mid-queue leaves the next track as it was: the
	// leader's ack repeats (b, T), and the boundary keeps its start.
	o := leaderOpts(now + 100)
	o.BasedOnRevision = rev(off.Revision)
	ack, out := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177600,
		StartAtMs: T, StartItemID: "b", Shuffle: boolp(false), Repeat: RepeatOff}, o)
	if out != OutcomeOK || ack.LeaderAckRevision != ack.Revision || ack.StartItemID != "b" || ack.StartAtMs != T {
		t.Fatalf("the leader's ack re-sending (b, T) after a mode press: %v %+v", out, ack)
	}
	// Shuffle on changed it: the ack names the new next track, and that
	// stands as any re-announcement does.
	sh, _ := s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetShuffle, Shuffle: true})
	if sh.StartItemID != "b" || sh.StartAtMs != T {
		t.Fatalf("shuffle cancelled the announcement: %+v", sh)
	}
	o = leaderOpts(now + 200)
	o.BasedOnRevision = rev(sh.Revision)
	ack, out = s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 177700,
		StartAtMs: T, StartItemID: "x", Shuffle: boolp(true), Repeat: RepeatOff}, o)
	if out != OutcomeOK || ack.StartItemID != "x" || ack.StartAtMs != T {
		t.Fatalf("the leader's re-announcement after shuffle: %v %+v", out, ack)
	}

	// The anchor: the press leaves it; the leader's ack re-anchors from its
	// own clock — a leader whose player hitched 400 ms while it reshuffled
	// is then followed where it really is. An ordinary heartbeat 400 ms off
	// would not move it (the 2 s keep-window).
	s = NewStore()
	st, _ = s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 60_000,
		Shuffle: boolp(false), Repeat: RepeatOff}, leaderOpts(now))
	if st.TrackStartedAtMs != now-60_000 {
		t.Fatalf("setup: anchor %d, want %d", st.TrackStartedAtMs, now-60_000)
	}
	o = leaderOpts(now + 1000)
	o.BasedOnRevision = rev(st.Revision)
	hb, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 60_600,
		Shuffle: boolp(false), Repeat: RepeatOff}, o)
	if hb.TrackStartedAtMs != now-60_000 || hb.AnchorRevision != st.AnchorRevision {
		t.Fatalf("setup: an ordinary heartbeat 400 ms off re-anchored: %d", hb.TrackStartedAtMs)
	}
	pressed, _ := s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetShuffle, Shuffle: true})
	if pressed.TrackStartedAtMs != hb.TrackStartedAtMs || pressed.AnchorRevision != hb.AnchorRevision ||
		pressed.UpdatedAtMs != hb.UpdatedAtMs || pressed.PositionMs != hb.PositionMs {
		t.Fatalf("the press moved the clock: %+v", pressed)
	}
	o = leaderOpts(now + 2000)
	o.BasedOnRevision = rev(pressed.Revision)
	acked, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 61_600,
		Shuffle: boolp(true), Repeat: RepeatOff}, o)
	if acked.TrackStartedAtMs != now-59_600 || acked.AnchorRevision != acked.Revision ||
		acked.LeaderAckRevision != acked.Revision {
		t.Fatalf("the ack did not re-anchor from the leader's clock: anchor %d (want %d), anchorRevision %d, revision %d",
			acked.TrackStartedAtMs, now-59_600, acked.AnchorRevision, acked.Revision)
	}
}

// A press nobody can carry out is refused, and a refusal changes nothing:
// no revision, no intent.
func TestPlayModePressRefusals(t *testing.T) {
	shuffleOn := IntentRequest{Type: IntentSetShuffle, Shuffle: true}
	repeatOne := IntentRequest{Type: IntentSetRepeat, Repeat: RepeatOne}
	unchanged := func(what string, before, after State) {
		t.Helper()
		if after.Revision != before.Revision || after.LastIntentRevision != before.LastIntentRevision ||
			after.IntentSeq != before.IntentSeq {
			t.Fatalf("%s changed the state: revision %d → %d, lastIntentRevision %d → %d",
				what, before.Revision, after.Revision, before.LastIntentRevision, after.LastIntentRevision)
		}
	}

	s := NewStore()
	if _, out := s.ApplyIntent("p1", "Remote", shuffleOn); out != OutcomeNoSession {
		t.Fatalf("nothing playing: %v", out)
	}

	// A leader that predates the modes: both refused.
	s = NewStore()
	st, _ := s.Report("p1", State{ItemID: "a", IsPlaying: true}, leaderOpts(0))
	for _, in := range []IntentRequest{shuffleOn, repeatOne} {
		got, out := s.ApplyIntent("p1", "Remote", in)
		if out != OutcomeLeaderLacksMode {
			t.Fatalf("%s to a leader without the modes: %v", in.Type, out)
		}
		unchanged(in.Type+" refused", st, got)
		assertNoModes(t, in.Type+" refused", got)
	}
	// One that can shuffle but not repeat: each mode on its own.
	s = NewStore()
	leadWithModes(t, s, boolp(false), "")
	if _, out := s.ApplyIntent("p1", "Remote", shuffleOn); out != OutcomeOK {
		t.Fatalf("shuffle to a leader that has it: %v", out)
	}
	if _, out := s.ApplyIntent("p1", "Remote", repeatOne); out != OutcomeLeaderLacksMode {
		t.Fatalf("repeat to a leader without it: %v", out)
	}

	// A legacy publisher: the conductor's own refusal, as for any intent.
	s = NewStore()
	s.Report("p1", State{ItemID: "a", IsPlaying: true, Shuffle: boolp(false), Repeat: RepeatOff},
		ReportOpts{DeviceID: "tok-old"})
	if _, out := s.ApplyIntent("p1", "Remote", shuffleOn); out != OutcomeLegacyLeader {
		t.Fatalf("legacy leader: %v", out)
	}

	// A leader asleep (30 s quiet) keeps its modes on show, but cannot act.
	s = NewStore()
	leadWithModes(t, s, boolp(false), RepeatOff)
	s.mu.Lock()
	cur := s.m["p1"]
	cur.LeaderReportedAtMs = time.Now().Add(-45 * time.Second).UnixMilli()
	s.m["p1"] = cur
	s.mu.Unlock()
	st, _ = s.Get("p1")
	if !st.LeaderDormant || st.Shuffle == nil || st.Repeat != RepeatOff {
		t.Fatalf("setup: a dormant leader with its modes: %+v", st)
	}
	for _, in := range []IntentRequest{shuffleOn, repeatOne} {
		got, out := s.ApplyIntent("p1", "Remote", in)
		if out != OutcomeLeaderAsleep {
			t.Fatalf("%s to a leader asleep: %v", in.Type, out)
		}
		unchanged(in.Type+" to a leader asleep", st, got)
	}

	// A vacant seat: nobody to carry it out.
	s = NewStore()
	leadWithModes(t, s, boolp(false), RepeatOff)
	s.Join("p1", "tok-tv", "Living Room TV", RoleFollower)
	if out := s.EndBy("p1", "phone", "iPhone", "stopped"); out != EndVacated {
		t.Fatalf("setup: vacate: %v", out)
	}
	st, _ = s.Get("p1")
	for _, in := range []IntentRequest{shuffleOn, repeatOne} {
		got, out := s.ApplyIntent("p1", "Remote", in)
		if out != OutcomeLeaderAsleep {
			t.Fatalf("%s with the seat vacant: %v", in.Type, out)
		}
		unchanged(in.Type+" with the seat vacant", st, got)
	}

	// An ended party takes no press.
	s = NewStore()
	leadWithModes(t, s, boolp(false), RepeatOff)
	if out := s.EndBy("p1", "phone", "iPhone", "stop-all"); out != EndAll {
		t.Fatalf("setup: stop-all: %v", out)
	}
	if _, out := s.ApplyIntent("p1", "Remote", shuffleOn); out != OutcomeNoSession {
		t.Fatalf("an ended party: %v", out)
	}

	// A repeat that is not one of the three (the handler answers 400
	// first): nothing changes.
	s = NewStore()
	st = leadWithModes(t, s, boolp(false), RepeatOff)
	got, out := s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetRepeat, Repeat: "sometimes"})
	if out == OutcomeOK || got.Repeat != RepeatOff {
		t.Fatalf("an invalid repeat: %v, repeat %q", out, got.Repeat)
	}
	unchanged("an invalid repeat", st, got)
}

// The modes are the leader's: a hand-over, or the seat going vacant, makes
// them unknown until a leader reports its own.
func TestAHandOverOrAVacantSeatForgetsThePlayModes(t *testing.T) {
	s := NewStore()
	leadWithModes(t, s, boolp(true), RepeatAll)
	s.JoinFollow("p1", "tv-uuid", "tok-tv", "Living Room TV", RoleFollower, nil, 0, nil)
	got, out := s.HandOver("p1", "phone", "iPhone", "tv-uuid")
	if out != OutcomeOK || got.LeaderTokenID != "tok-tv" {
		t.Fatalf("setup: hand-over: %v %+v", out, got)
	}
	assertNoModes(t, "after a hand-over", got)
	if _, out := s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetShuffle, Shuffle: false}); out != OutcomeLeaderLacksMode {
		t.Fatalf("a press before the new leader said what it can do: %v", out)
	}
	o := ReportOpts{DeviceID: "tok-tv", DeviceLabel: "Living Room TV", BasedOnRevision: rev(got.Revision), HasPositionMs: true}
	mine, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180, PositionMs: 61_000,
		Shuffle: boolp(false), Repeat: RepeatOne}, o)
	if out != OutcomeOK || mine.Shuffle == nil || *mine.Shuffle || mine.Repeat != RepeatOne {
		t.Fatalf("the new leader's own modes: %v shuffle %v repeat %q", out, mine.Shuffle, mine.Repeat)
	}

	// The leader leaves the seat with a follower playing on.
	s = NewStore()
	leadWithModes(t, s, boolp(true), RepeatAll)
	s.Join("p1", "tok-tv", "Living Room TV", RoleFollower)
	if out := s.EndBy("p1", "phone", "iPhone", "stopped"); out != EndVacated {
		t.Fatalf("setup: vacate: %v", out)
	}
	got, _ = s.Get("p1")
	assertNoModes(t, "with the seat vacated", got)

	// The leader silent past staleAfter while a follower keeps the party
	// alive: Get vacates the seat, and the modes go with it.
	s = NewStore()
	leadWithModes(t, s, boolp(true), RepeatAll)
	s.Join("p1", "tok-tv", "Living Room TV", RoleFollower)
	s.mu.Lock()
	cur := s.m["p1"]
	cur.LeaderReportedAtMs = time.Now().Add(-100 * time.Second).UnixMilli()
	s.m["p1"] = cur
	s.mu.Unlock()
	got, ok := s.Get("p1")
	if !ok || got.LeaderTokenID != "" {
		t.Fatalf("setup: Get did not vacate the seat: %v %+v", ok, got)
	}
	assertNoModes(t, "with the seat vacated by Get", got)
}

// Presses, heartbeats and reads at once (go test -race): the modes are
// replaced, never written through, so a state handed out is never
// changed under its reader.
func TestPlayModesUnderConcurrentPressesAndReports(t *testing.T) {
	s := NewStore()
	leadWithModes(t, s, boolp(false), RepeatOff)
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			modes := []string{RepeatOff, RepeatAll, RepeatOne}
			for i := 0; i < 100; i++ {
				s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetShuffle, Shuffle: (i+g)%2 == 0})
				s.ApplyIntent("p1", "Remote", IntentRequest{Type: IntentSetRepeat, Repeat: modes[(i+g)%3]})
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			cur, ok := s.Get("p1")
			if !ok {
				continue
			}
			o := leaderOpts(0)
			o.BasedOnRevision = rev(cur.Revision)
			s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
				PositionMs: 60_000 + int64(i)*10, Shuffle: boolp(i%2 == 0), Repeat: RepeatAll}, o)
		}
	}()
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if cur, ok := s.Get("p1"); ok {
					if _, err := json.Marshal(cur); err != nil {
						t.Error(err)
						return
					}
					if cur.Shuffle != nil {
						_ = *cur.Shuffle
					}
				}
			}
		}()
	}
	wg.Wait()
	got, ok := s.Get("p1")
	if !ok || got.Shuffle == nil || !ValidRepeat(got.Repeat) {
		t.Fatalf("after the storm: %v shuffle %v repeat %q", ok, got.Shuffle, got.Repeat)
	}
}
