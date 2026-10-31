package nowplaying

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// Party volume and device features (party_audio.go): read-time, kept
// beside the state, never decoded from a report, and nothing to do with
// the intent path.

// audioParty: the phone leads; a TV follows under a UUID it minted with
// its token noted; a PC follows under its token; a Remote and a Home card
// watch.
func audioParty(t *testing.T) (*Store, State) {
	t.Helper()
	s := NewStore()
	if _, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 200},
		ReportOpts{DeviceID: "tok-phone", DeviceLabel: "Kate's iPhone", BasedOnRevision: rev(0)}); out != OutcomeOK {
		t.Fatalf("leader's first report: %v", out)
	}
	s.JoinFollow("p1", "uuid-tv", "tok-tv", "Living Room TV", RoleFollower, nil, 0, nil)
	s.JoinFollow("p1", "tok-pc", "tok-pc", "Windows PC", RoleFollower, nil, 0, nil)
	s.JoinFollow("p1", "uuid-den:remote", "tok-remote", "Den Remote", RoleController, nil, 0, nil)
	s.JoinFollow("p1", "uuid-den:viewer", "tok-viewer", "Den TV", RoleViewer, nil, 0, nil)
	st, _ := s.Get("p1")
	return s, st
}

func revOf(s *Store, profileID string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rev[profileID]
}

func follower(st State, key string) (Presence, bool) {
	for _, f := range st.Followers {
		if f.DeviceKey == key {
			return f, true
		}
	}
	return Presence{}, false
}

func intp(v int) *int { return &v }

func volIs(p *int, want int) bool { return p != nil && *p == want }

// 1. A volume change wakes every parked poll at once, and the leader's
// next heartbeat — based on the revision before it — is accepted.
func TestPartyVolumeWakesPollsAndTheLeadersNextHeartbeatIsNotStale(t *testing.T) {
	s, st := audioParty(t)
	woke := make(chan time.Duration, 1)
	go func() {
		began := time.Now()
		s.WaitForChange(context.Background(), "p1", st.Revision, 5*time.Second)
		woke <- time.Since(began)
	}()
	deadline := time.Now().Add(2 * time.Second)
	for s.Waiting("p1") < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the poll never parked")
		}
		time.Sleep(2 * time.Millisecond)
	}

	got, out := s.SetPartyVolume("p1", "tok-remote", "Den Remote", 40)
	if out != OutcomeOK || !volIs(got.PartyVolume, 40) || got.PartyVolumeByLabel != "Den Remote" {
		t.Fatalf("set 40: %v %+v", out, got)
	}
	if got.Revision <= st.Revision {
		t.Fatalf("revision did not move: %d → %d", st.Revision, got.Revision)
	}
	select {
	case took := <-woke:
		if took > time.Second {
			t.Fatalf("the parked poll took %v to wake", took)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the parked poll never woke")
	}

	hb, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 200},
		ReportOpts{DeviceID: "tok-phone", DeviceLabel: "Kate's iPhone", BasedOnRevision: rev(st.Revision)})
	if out != OutcomeOK {
		t.Fatalf("a leader heartbeat based on the revision before the volume change: %v, want OK", out)
	}
	if !volIs(hb.PartyVolume, 40) {
		t.Fatalf("the heartbeat lost the party volume: %+v", hb.PartyVolume)
	}
}

// bookkeeping is everything a party-audio change must leave alone.
type bookkeeping struct {
	lastIntent                                         Intent
	lastIntentRev, ackRev, intentSeq, itemRev, anchRev int64
	anchor, trackStartedAt                             int64
	startAt, startPos                                  int64
	startItem                                          string
	queueRev                                           int64
	queueLen                                           int
	refresh                                            bool
}

func snapshot(s *Store, profileID string) bookkeeping {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := s.m[profileID]
	b := bookkeeping{
		lastIntentRev: st.LastIntentRevision, ackRev: st.LeaderAckRevision, intentSeq: st.IntentSeq,
		itemRev: st.ItemRevision, anchRev: st.AnchorRevision, anchor: s.anchor[profileID],
		trackStartedAt: st.TrackStartedAtMs, startAt: st.StartAtMs, startPos: st.StartPositionMs,
		startItem: st.StartItemID, queueRev: st.QueueRevision, queueLen: len(s.q[profileID]),
		refresh: s.anchorRefresh[profileID],
	}
	if st.LastIntent != nil {
		b.lastIntent = *st.LastIntent
	}
	return b
}

// 2. The volume setters touch no intent, anchor, schedule or queue
// bookkeeping: a current-item start and a next-item announcement stand.
func TestPartyVolumeLeavesIntentAnchorAndScheduleAlone(t *testing.T) {
	t.Run("current-item schedule", func(t *testing.T) {
		s, st := audioParty(t)
		s.SetQueue("p1", []QueueItem{song("a"), song("b")})
		in, out := s.ApplyIntent("p1", "Den Remote", IntentRequest{Type: IntentSetPositionMs, PositionMs: 30_000,
			BasedOnRevision: st.Revision})
		if out != OutcomeOK || in.LastIntent == nil {
			t.Fatalf("intent: %v", out)
		}
		now := time.Now().UnixMilli()
		ack, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 200,
			StartAtMs: now + 1500, StartPositionMs: 30_000, StartItemID: "a"},
			ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(in.Revision), HasPositionMs: true})
		if out != OutcomeOK || ack.StartAtMs != now+1500 || ack.LeaderAckRevision == 0 {
			t.Fatalf("the acknowledging, scheduling report: %v %+v", out, ack)
		}
		before := snapshot(s, "p1")
		if _, out := s.SetPartyVolume("p1", "tok-tv", "Living Room TV", 30); out != OutcomeOK {
			t.Fatal(out)
		}
		if _, out := s.SetDeviceVolume("p1", "tok-remote", "uuid-tv", 55); out != OutcomeOK {
			t.Fatal(out)
		}
		if _, out := s.SetDeviceVolume("p1", "tok-remote", "tok-phone", 70); out != OutcomeOK {
			t.Fatal(out)
		}
		if after := snapshot(s, "p1"); !reflect.DeepEqual(before, after) {
			t.Fatalf("a volume change moved the bookkeeping:\nbefore %+v\nafter  %+v", before, after)
		}
	})
	t.Run("next-item schedule", func(t *testing.T) {
		s, _ := audioParty(t)
		now := time.Now().UnixMilli()
		T := now + 3000 + 540
		st, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 180,
			PositionMs: 177_000, StartAtMs: T, StartItemID: "b"},
			ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), HasPositionMs: true, SampledAtMs: now})
		if out != OutcomeOK || st.StartItemID != "b" || st.StartAtMs != T {
			t.Fatalf("next-item announcement: %v %+v", out, st)
		}
		before := snapshot(s, "p1")
		s.SetPartyVolume("p1", "tok-pc", "Windows PC", 10)
		s.SetDeviceVolume("p1", "tok-pc", "tok-pc", 20)
		if after := snapshot(s, "p1"); !reflect.DeepEqual(before, after) {
			t.Fatalf("a volume change moved the bookkeeping:\nbefore %+v\nafter  %+v", before, after)
		}
		cur, _ := s.Get("p1")
		if cur.StartItemID != "b" || cur.StartAtMs != T {
			t.Fatalf("the next-item announcement did not stand: %+v", cur)
		}
	})
}

// 3. A PUT body that carries the read-time fields changes nothing — the
// same guard as queue edits.
func TestReportBodyCannotPlantPartyAudio(t *testing.T) {
	s, _ := audioParty(t)
	s.SetPartyVolume("p1", "tok-tv", "Living Room TV", 40)
	planted := State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 200,
		PartyVolume: intp(0), PartyVolumeByLabel: "evil", LeaderVolume: intp(1),
		YourDeviceVolume: intp(2), LeaderFeatures: []string{"evil"}, AnnounceLeaderOnly: true,
		Announcement:  &AnnouncementView{ID: "evil", Kind: KindClip, ChimeAtMs: 1, VoiceStartsAtMs: 2},
		deviceVolumes: map[string]int{"tok-phone": 3}}
	st, out := s.Report("p1", planted, ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)})
	if out != OutcomeOK {
		t.Fatalf("report: %v", out)
	}
	for _, got := range []State{st, func() State { g, _ := s.Get("p1"); return g }()} {
		if !volIs(got.PartyVolume, 40) || got.PartyVolumeByLabel != "Living Room TV" ||
			got.LeaderVolume != nil || got.YourDeviceVolume != nil || got.LeaderFeatures != nil ||
			got.AnnounceLeaderOnly || got.Announcement != nil || got.VolumeFor("tok-phone") != nil {
			t.Fatalf("the body planted party audio: %+v", got)
		}
	}
	s.mu.RLock()
	stored := s.m["p1"]
	s.mu.RUnlock()
	if stored.PartyVolume != nil || stored.LeaderFeatures != nil || stored.deviceVolumes != nil ||
		stored.AnnounceLeaderOnly || stored.Announcement != nil {
		t.Fatalf("the stored state kept body fields: %+v", stored)
	}
	// Nor can it plant into a refused report's answer, or a policy.
	if _, out := s.Report("p1", planted, ReportOpts{DeviceID: "tok-pc", BasedOnRevision: rev(0)}); out != OutcomeSeatHeld {
		t.Fatalf("expected a seat-held refusal: %v", out)
	}
	if got, _ := s.Get("p1"); got.AnnounceLeaderOnly || got.Announcement != nil {
		t.Fatalf("a refused body planted party audio: %+v", got)
	}
}

// 4. A heartbeat that knows nothing of the fields keeps them; the same
// value is no change; 0 is published as 0.
func TestPartyVolumeSurvivesHeartbeatsSameValueAndZero(t *testing.T) {
	s, _ := audioParty(t)
	st, out := s.SetPartyVolume("p1", "tok-pc", "Windows PC", 0)
	if out != OutcomeOK || !volIs(st.PartyVolume, 0) {
		t.Fatalf("0 is not published as 0: %v %+v", out, st.PartyVolume)
	}
	r := revOf(s, "p1")
	if again, out := s.SetPartyVolume("p1", "tok-tv", "Living Room TV", 0); out != OutcomeOK ||
		revOf(s, "p1") != r || again.PartyVolumeByLabel != "Windows PC" {
		t.Fatalf("the same value bumped or relabelled: %v rev %d → %d %+v", out, r, revOf(s, "p1"), again)
	}
	for i := 0; i < 3; i++ {
		hb, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 200},
			ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(revOf(s, "p1"))})
		if out != OutcomeOK || !volIs(hb.PartyVolume, 0) {
			t.Fatalf("heartbeat %d: %v %+v", i, out, hb.PartyVolume)
		}
	}
	// Never set reads as absent; setting 100 over "never set" is no change.
	fresh, _ := audioParty(t)
	r = revOf(fresh, "p1")
	if st, out := fresh.SetPartyVolume("p1", "tok-pc", "Windows PC", 100); out != OutcomeOK ||
		st.PartyVolume != nil || revOf(fresh, "p1") != r {
		t.Fatalf("100 over the default: %v %+v", out, st.PartyVolume)
	}
	// And 100 over 40 is a change, published as set.
	fresh.SetPartyVolume("p1", "tok-pc", "Windows PC", 40)
	if st, _ := fresh.SetPartyVolume("p1", "tok-tv", "Living Room TV", 100); !volIs(st.PartyVolume, 100) ||
		st.PartyVolumeByLabel != "Living Room TV" {
		t.Fatalf("100 over 40: %+v", st)
	}
}

// 5. Only a device in the party: the seat (live or dormant) or a live
// presence of any role.
func TestPartyVolumeNeedsADeviceInTheParty(t *testing.T) {
	s, _ := audioParty(t)
	for _, tok := range []string{"tok-phone", "tok-tv", "tok-pc", "tok-remote", "tok-viewer"} {
		if _, out := s.SetPartyVolume("p1", tok, tok, 50+len(tok)); out != OutcomeOK {
			t.Errorf("%s: %v, want OK", tok, out)
		}
	}
	for _, tok := range []string{"tok-stranger", ""} {
		st, out := s.SetPartyVolume("p1", tok, "x", 7)
		if out != OutcomeNotInParty || st.LeaderTokenID == "" {
			t.Errorf("%q: %v, want not-in-party with the state", tok, out)
		}
		if _, out := s.SetDeviceVolume("p1", tok, "tok-pc", 7); out != OutcomeNotInParty {
			t.Errorf("%q device volume: %v, want not-in-party", tok, out)
		}
	}
	// A Remote whose presence aged out is no longer in it.
	s.mu.Lock()
	e := s.followers["p1"]["uuid-den:remote"]
	e.last = time.Now().Add(-2 * followerTTL)
	s.followers["p1"]["uuid-den:remote"] = e
	// A dormant seat holder still is.
	st := s.m["p1"]
	st.LeaderReportedAtMs -= leaderLostAfter.Milliseconds() + 1
	s.m["p1"] = st
	s.mu.Unlock()
	if _, out := s.SetPartyVolume("p1", "tok-remote", "Den Remote", 9); out != OutcomeNotInParty {
		t.Fatalf("an aged-out Remote: %v", out)
	}
	if _, out := s.SetPartyVolume("p1", "tok-phone", "Kate's iPhone", 9); out != OutcomeOK {
		t.Fatalf("a dormant seat holder: %v", out)
	}
	// Nothing playing, or ended: no session.
	if _, out := NewStore().SetPartyVolume("p1", "tok-phone", "x", 9); out != OutcomeNoSession {
		t.Fatalf("no session: %v", out)
	}
}

// 6. A device level is kept for the device's TOKEN: an Apple TV follows
// under a UUID it minted, leads under its token, and keeps its level.
func TestDeviceVolumeIsKeptPerToken(t *testing.T) {
	s, _ := audioParty(t)
	st, out := s.SetDeviceVolume("p1", "tok-remote", "uuid-tv", 30)
	if out != OutcomeOK {
		t.Fatalf("set the TV row: %v", out)
	}
	tv, ok := follower(st, "uuid-tv")
	if !ok || !volIs(tv.Volume, 30) || !volIs(st.VolumeFor("tok-tv"), 30) {
		t.Fatalf("the TV row: %+v, VolumeFor %v", tv, st.VolumeFor("tok-tv"))
	}
	if pc, _ := follower(st, "tok-pc"); pc.Volume != nil {
		t.Fatalf("another row has a level: %+v", pc)
	}
	if st.VolumeFor("tok-pc") != nil || st.LeaderVolume != nil {
		t.Fatalf("levels leaked: %+v", st)
	}

	st, out = s.HandOver("p1", "tok-phone", "Kate's iPhone", "uuid-tv")
	if out != OutcomeOK || st.LeaderTokenID != "tok-tv" {
		t.Fatalf("handover: %v %q", out, st.LeaderTokenID)
	}
	if !volIs(st.LeaderVolume, 30) || !volIs(st.VolumeFor("tok-tv"), 30) {
		t.Fatalf("the TV's level after it took the seat: leader %v, its own %v", st.LeaderVolume, st.VolumeFor("tok-tv"))
	}

	// The seat by its token id resolves; 100 removes the entry.
	if st, out = s.SetDeviceVolume("p1", "tok-pc", "tok-tv", 80); out != OutcomeOK || !volIs(st.LeaderVolume, 80) {
		t.Fatalf("by the seat's token id: %v %v", out, st.LeaderVolume)
	}
	r := revOf(s, "p1")
	if st, out = s.SetDeviceVolume("p1", "tok-pc", "tok-tv", 80); out != OutcomeOK || revOf(s, "p1") != r {
		t.Fatalf("the same level bumped: %v", out)
	}
	if st, out = s.SetDeviceVolume("p1", "tok-pc", "tok-tv", 100); out != OutcomeOK || st.LeaderVolume != nil {
		t.Fatalf("100 kept an entry: %v %v", out, st.LeaderVolume)
	}
	s.mu.RLock()
	_, kept := s.audio["p1"].devices["tok-tv"]
	s.mu.RUnlock()
	if kept {
		t.Fatal("100 left an entry behind")
	}

	// Who is not a live follower or the seat is no device.
	for _, key := range []string{"nobody", "uuid-den:remote", "uuid-den:viewer", "", "tok-remote"} {
		if _, out := s.SetDeviceVolume("p1", "tok-pc", key, 20); out != OutcomeNoSuchDevice {
			t.Errorf("deviceKey %q: %v, want no-such-device", key, out)
		}
	}
	// A vacant seat is "" — and "" names nothing.
	s.EndBy("p1", "tok-tv", "Living Room TV", "stopped")
	if _, out := s.SetDeviceVolume("p1", "tok-pc", "", 20); out != OutcomeNoSuchDevice {
		t.Fatalf("an empty key with the seat vacant: %v", out)
	}

	// The 65th entry is refused; the 64 kept still change.
	full, _ := audioParty(t)
	full.SetDeviceVolume("p1", "tok-pc", "tok-pc", 20)
	full.mu.Lock()
	for i := 0; len(full.audio["p1"].devices) < MaxDeviceVolumes; i++ {
		full.audio["p1"].devices[fmt.Sprintf("tok-x%d", i)] = 50
	}
	full.mu.Unlock()
	if _, out := full.SetDeviceVolume("p1", "tok-pc", "uuid-tv", 30); out != OutcomeServerBusy {
		t.Fatalf("a 65th level: %v, want server-busy", out)
	}
	if st, out := full.SetDeviceVolume("p1", "tok-pc", "tok-pc", 25); out != OutcomeOK || !volIs(st.VolumeFor("tok-pc"), 25) {
		t.Fatalf("changing a kept level while full: %v", out)
	}
	if _, out := full.SetDeviceVolume("p1", "tok-pc", "uuid-tv", 100); out != OutcomeOK {
		t.Fatalf("100 for a device with no entry while full: %v", out)
	}
}

// 7. Kept across hand-overs, a vacant seat and devices leaving; dropped
// with the session.
func TestPartyAudioLifetime(t *testing.T) {
	setAll := func(s *Store) {
		s.SetPartyVolume("p1", "tok-pc", "Windows PC", 40)
		s.SetDeviceVolume("p1", "tok-pc", "uuid-tv", 30)
		s.JoinFollow("p1", "uuid-tv", "tok-tv", "Living Room TV", RoleFollower, nil, 0, []string{"volume"})
	}
	has := func(st State) bool {
		tv, _ := follower(st, "uuid-tv")
		return volIs(st.PartyVolume, 40) && volIs(st.VolumeFor("tok-tv"), 30) && len(tv.Features) == 1
	}
	none := func(s *Store) bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.audio["p1"] == nil
	}

	t.Run("kept", func(t *testing.T) {
		s, _ := audioParty(t)
		setAll(s)
		s.StopFollowing("p1", "tok-pc") // a member leaves
		if st, _ := s.Get("p1"); !has(st) {
			t.Fatalf("after a member left: %+v", st)
		}
		if out := s.EndBy("p1", "tok-pc", "Windows PC", "stop-all"); out != EndLeft {
			t.Fatalf("a non-member's stop-all: %v", out)
		}
		s.JoinFollow("p1", "tok-pc", "tok-pc", "Windows PC", RoleFollower, nil, 0, nil)
		if out := s.EndBy("p1", "tok-pc", "Windows PC", ""); out != EndLeft {
			t.Fatalf("a follower leaving: %v", out)
		}
		if st, _ := s.Get("p1"); !has(st) {
			t.Fatalf("after a follower's DELETE: %+v", st)
		}
		s.JoinFollow("p1", "tok-pc", "tok-pc", "Windows PC", RoleFollower, nil, 0, nil)
		st, out := s.HandOver("p1", "tok-phone", "Kate's iPhone", "tok-pc")
		if out != OutcomeOK || !volIs(st.PartyVolume, 40) {
			t.Fatalf("after a hand-over: %v %+v", out, st)
		}
		s.JoinFollow("p1", "tok-phone", "tok-phone", "Kate's iPhone", RoleFollower, nil, 0, nil)
		if out := s.EndBy("p1", "tok-pc", "Windows PC", "stopped"); out != EndVacated {
			t.Fatalf("vacate: %v", out)
		}
		if st, _ := s.Get("p1"); !has(st) || st.LeaderTokenID != "" {
			t.Fatalf("after the seat went vacant: %+v", st)
		}
	})

	t.Run("dropped by stop-all; the next party starts at the defaults", func(t *testing.T) {
		s, _ := audioParty(t)
		setAll(s)
		if out := s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-all"); out != EndAll {
			t.Fatal(out)
		}
		st, ok := s.Get("p1")
		if !ok || !st.Ended || st.PartyVolume != nil || st.VolumeFor("tok-tv") != nil || !none(s) {
			t.Fatalf("the tombstone carries party audio: %+v", st)
		}
		for _, f := range st.Followers {
			if f.Volume != nil || f.Features != nil {
				t.Fatalf("a tombstone row carries party audio: %+v", f)
			}
		}
		if _, out := s.SetPartyVolume("p1", "tok-pc", "x", 20); out != OutcomeNoSession {
			t.Fatalf("a volume on an ended party: %v", out)
		}
		s.JoinFollow("p1", "uuid-tv", "tok-tv", "Living Room TV", RoleFollower, nil, 0, []string{"volume", "announce"})
		if !none(s) {
			t.Fatal("features were kept for an ended party")
		}
		next, _ := s.Report("p1", State{ItemID: "z", IsPlaying: true},
			ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), Features: []string{"volume"}})
		if next.PartyVolume != nil || next.VolumeFor("tok-tv") != nil {
			t.Fatalf("the next party inherited party audio: %+v", next)
		}
		if !reflect.DeepEqual(next.LeaderFeatures, []string{"volume"}) {
			t.Fatalf("the new leader's features: %v", next.LeaderFeatures)
		}
	})

	t.Run("dropped by a deleting DELETE", func(t *testing.T) {
		s, _ := audioParty(t)
		setAll(s)
		if out := s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-sharing"); out != EndDeleted {
			t.Fatal(out)
		}
		if !none(s) {
			t.Fatal("a deleted session kept its party audio")
		}
	})

	t.Run("dropped by Get's stale expiry", func(t *testing.T) {
		s, _ := audioParty(t)
		setAll(s)
		s.mu.Lock()
		st := s.m["p1"]
		st.UpdatedAtMs = time.Now().Add(-2 * time.Minute).UnixMilli()
		s.m["p1"] = st
		s.lastActivity["p1"] = time.Now().Add(-2 * time.Minute)
		s.mu.Unlock()
		if _, ok := s.Get("p1"); ok || !none(s) {
			t.Fatalf("stale expiry kept party audio (ok=%v)", ok)
		}
	})

	t.Run("dropped by Get's tombstone expiry", func(t *testing.T) {
		s, _ := audioParty(t)
		s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-all")
		s.mu.Lock()
		s.audio["p1"] = &partyAudio{volume: intp(5)} // as if anything were left
		st := s.m["p1"]
		st.EndedAtMs = time.Now().Add(-time.Minute).UnixMilli()
		s.m["p1"] = st
		s.mu.Unlock()
		if _, ok := s.Get("p1"); ok || !none(s) {
			t.Fatalf("tombstone expiry kept party audio (ok=%v)", ok)
		}
	})

	t.Run("a report that finds the session stale starts without it", func(t *testing.T) {
		s, _ := audioParty(t)
		setAll(s)
		s.mu.Lock()
		st := s.m["p1"]
		st.UpdatedAtMs = time.Now().Add(-2 * time.Minute).UnixMilli()
		s.m["p1"] = st
		s.lastActivity["p1"] = time.Now().Add(-2 * time.Minute)
		s.mu.Unlock()
		got, out := s.Report("p1", State{ItemID: "x", IsPlaying: true},
			ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(revOf(s, "p1"))})
		if out != OutcomeOK || got.PartyVolume != nil || got.VolumeFor("tok-tv") != nil {
			t.Fatalf("a stale session's party volume carried on: %v %+v", out, got)
		}
		// A live one does not lose it.
		s.SetPartyVolume("p1", "tok-phone", "Kate's iPhone", 60)
		if got, _ := s.Report("p1", State{ItemID: "x", IsPlaying: true},
			ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(revOf(s, "p1"))}); !volIs(got.PartyVolume, 60) {
			t.Fatalf("a live session lost its party volume: %+v", got.PartyVolume)
		}
	})

	t.Run("a report over an ended record starts without it", func(t *testing.T) {
		s, _ := audioParty(t)
		s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-all")
		s.mu.Lock()
		s.audio["p1"] = &partyAudio{volume: intp(5)} // as if anything were left
		s.mu.Unlock()
		got, _ := s.Report("p1", State{ItemID: "x", IsPlaying: true},
			ReportOpts{DeviceID: "tok-pc", BasedOnRevision: rev(0)})
		if got.PartyVolume != nil || !none(s) {
			t.Fatalf("a party after a tombstone inherited party audio: %+v", got.PartyVolume)
		}
	})

	t.Run("a stale session takes no volume", func(t *testing.T) {
		s, _ := audioParty(t)
		s.mu.Lock()
		st := s.m["p1"]
		st.UpdatedAtMs = time.Now().Add(-2 * time.Minute).UnixMilli()
		s.m["p1"] = st
		s.lastActivity["p1"] = time.Now().Add(-2 * time.Minute)
		s.mu.Unlock()
		if _, out := s.SetPartyVolume("p1", "tok-phone", "Kate's iPhone", 20); out != OutcomeNoSession {
			t.Fatalf("a stale session: %v", out)
		}
	})
}

// 8. One profile's party audio is that profile's alone.
func TestPartyAudioIsPerProfile(t *testing.T) {
	s, _ := audioParty(t)
	s.Report("p2", State{ItemID: "q", IsPlaying: true}, ReportOpts{DeviceID: "tok-sarah", BasedOnRevision: rev(0)})
	s.JoinFollow("p2", "uuid-tv", "tok-sarah-tv", "Sarah's TV", RoleFollower, nil, 0, []string{"volume"})
	s.SetPartyVolume("p1", "tok-pc", "Windows PC", 40)
	s.SetDeviceVolume("p1", "tok-pc", "uuid-tv", 30)
	other, _ := s.Get("p2")
	if other.PartyVolume != nil || other.VolumeFor("tok-tv") != nil {
		t.Fatalf("p2 sees p1's volume: %+v", other)
	}
	if tv, _ := follower(other, "uuid-tv"); tv.Volume != nil {
		t.Fatalf("p2's row with p1's key took p1's level: %+v", tv)
	}
	// p1's tokens are nobody in p2's party.
	if _, out := s.SetPartyVolume("p2", "tok-pc", "Windows PC", 1); out != OutcomeNotInParty {
		t.Fatalf("a p1 token in p2's party: %v", out)
	}
	mine, _ := s.Get("p1")
	if tv, _ := follower(mine, "uuid-tv"); len(tv.Features) != 0 {
		t.Fatalf("p1 sees p2's features: %+v", tv)
	}
}

// 10. What each device can do: kept per token from a follower's join and
// an accepted report, listed per row and for the seat.
func TestFeaturesArePerTokenAndListed(t *testing.T) {
	s, _ := audioParty(t)
	// The leader's report answers the joins' anchor requests first.
	s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(revOf(s, "p1"))})
	r := revOf(s, "p1")
	all := []string{"volume", "announce", "live-announce"}
	s.JoinFollow("p1", "uuid-tv", "tok-tv", "Living Room TV", RoleFollower, nil, 0, all)
	if revOf(s, "p1") <= r {
		t.Fatal("a changed list did not bump")
	}
	s.mu.RLock()
	refresh := s.anchorRefresh["p1"]
	s.mu.RUnlock()
	if refresh {
		t.Fatal("a features change asked the leader for a fresh anchor")
	}
	st, _ := s.Get("p1")
	if tv, _ := follower(st, "uuid-tv"); !reflect.DeepEqual(tv.Features, all) {
		t.Fatalf("the TV row's features: %v", tv.Features)
	}
	// The same list, and a heartbeat without one, change nothing.
	r = revOf(s, "p1")
	s.JoinFollow("p1", "uuid-tv", "tok-tv", "Living Room TV", RoleFollower, nil, 0, all)
	s.JoinFollow("p1", "uuid-tv", "tok-tv", "Living Room TV", RoleFollower, nil, 0, nil)
	if revOf(s, "p1") != r {
		t.Fatal("an unchanged list bumped")
	}
	// A changed list bumps exactly once.
	s.JoinFollow("p1", "uuid-tv", "tok-tv", "Living Room TV", RoleFollower, nil, 0, []string{"volume"})
	if got := revOf(s, "p1"); got <= r {
		t.Fatal("a changed list did not bump")
	}
	// A viewer's or a Remote's list is not kept.
	s.JoinFollow("p1", "uuid-den:remote", "tok-remote", "Den Remote", RoleController, nil, 0, all)
	s.mu.RLock()
	_, kept := s.audio["p1"].features["tok-remote"]
	s.mu.RUnlock()
	if kept {
		t.Fatal("a Remote's features were kept")
	}

	// An accepted report's list is the seat's.
	st, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(revOf(s, "p1")), Features: []string{"volume", "announce"}})
	if out != OutcomeOK || !reflect.DeepEqual(st.LeaderFeatures, []string{"volume", "announce"}) {
		t.Fatalf("leaderFeatures: %v %v", out, st.LeaderFeatures)
	}
	// A refused report's is not kept: seat-held, and stale.
	s.Report("p1", State{ItemID: "x", IsPlaying: true},
		ReportOpts{DeviceID: "tok-pc", BasedOnRevision: rev(revOf(s, "p1")), Features: all})
	s.ApplyIntent("p1", "Den Remote", IntentRequest{Type: IntentSetPlaying, Playing: false, BasedOnRevision: revOf(s, "p1")})
	if _, out := s.Report("p1", State{ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), Features: all}); out != OutcomeStale {
		t.Fatalf("expected a stale report: %v", out)
	}
	s.mu.RLock()
	_, pcKept := s.audio["p1"].features["tok-pc"]
	phone := append([]string(nil), s.audio["p1"].features["tok-phone"]...)
	s.mu.RUnlock()
	if pcKept || !reflect.DeepEqual(phone, []string{"volume", "announce"}) {
		t.Fatalf("a refused report's features were kept: pc %v, phone %v", pcKept, phone)
	}

	// After a hand-over the new leader's features are what it sent while
	// following.
	st, out = s.HandOver("p1", "tok-phone", "Kate's iPhone", "uuid-tv")
	if out != OutcomeOK || !reflect.DeepEqual(st.LeaderFeatures, []string{"volume"}) {
		t.Fatalf("the TV leading: %v %v", out, st.LeaderFeatures)
	}
	// An empty list says "none".
	s.JoinFollow("p1", "tok-pc", "tok-pc", "Windows PC", RoleFollower, nil, 0, []string{"volume"})
	s.JoinFollow("p1", "tok-pc", "tok-pc", "Windows PC", RoleFollower, nil, 0, []string{})
	st, _ = s.Get("p1")
	if pc, _ := follower(st, "tok-pc"); pc.Features != nil {
		t.Fatalf("an empty list kept the old one: %v", pc.Features)
	}
}

func TestCleanFeatures(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want []string
		ok   bool
	}{
		{nil, nil, false},
		{[]string{}, []string{}, true},
		{[]string{"volume", "announce", "volume"}, []string{"volume", "announce"}, true},
		{[]string{"live-announce", "x9-"}, []string{"live-announce", "x9-"}, true},
		{[]string{"a", "b", "c", "d", "e", "f", "g", "h"}, []string{"a", "b", "c", "d", "e", "f", "g", "h"}, true},
		{[]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, nil, false},
		{[]string{"Volume"}, nil, false},
		{[]string{"9lives"}, nil, false},
		{[]string{"-x"}, nil, false},
		{[]string{""}, nil, false},
		{[]string{"vol ume"}, nil, false},
		{[]string{"a234567890123456789012345678901b"}, []string{"a234567890123456789012345678901b"}, true},
		{[]string{"a2345678901234567890123456789012c"}, nil, false},
		{[]string{"volume", "évier"}, nil, false},
	} {
		got, ok := CleanFeatures(c.in)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("CleanFeatures(%q) = %q %v, want %q %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

// The features kept per party are bounded: tokens gone from the party go
// first; while every place is taken by a live device, a new list is not
// kept.
func TestFeatureListsAreBoundedPerParty(t *testing.T) {
	s, _ := audioParty(t)
	s.mu.Lock()
	a := s.audioLocked("p1")
	a.features = map[string][]string{}
	for i := 0; len(a.features) < maxFeatureTokens; i++ {
		a.features[fmt.Sprintf("tok-gone%d", i)] = []string{"volume"}
	}
	s.mu.Unlock()
	s.JoinFollow("p1", "tok-pc", "tok-pc", "Windows PC", RoleFollower, nil, 0, []string{"volume"})
	st, _ := s.Get("p1")
	if pc, _ := follower(st, "tok-pc"); len(pc.Features) != 1 {
		t.Fatalf("departed tokens were not pruned for a live one: %+v", pc)
	}
	s.mu.Lock()
	n := len(s.audio["p1"].features)
	s.mu.Unlock()
	if n > maxFeatureTokens {
		t.Fatalf("%d lists kept", n)
	}
}
