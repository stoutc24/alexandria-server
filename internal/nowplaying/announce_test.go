package nowplaying

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"
)

// Announcements (announce.go): one at a time per party, the server picks
// the instants, the bytes are the party's alone and are zeroed once
// played.

// wav16k is a canonical clip of n samples (16 kHz, mono, 16-bit PCM)
// with a pattern that is never all zeros.
func wav16k(n int) []byte {
	le := binary.LittleEndian
	data := 2 * n
	b := make([]byte, 44+data)
	copy(b[0:], "RIFF")
	le.PutUint32(b[4:], uint32(36+data))
	copy(b[8:], "WAVEfmt ")
	le.PutUint32(b[16:], 16)
	le.PutUint16(b[20:], 1)
	le.PutUint16(b[22:], 1)
	le.PutUint32(b[24:], 16000)
	le.PutUint32(b[28:], 32000)
	le.PutUint16(b[32:], 2)
	le.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	le.PutUint32(b[40:], uint32(data))
	for i := 0; i < n; i++ {
		le.PutUint16(b[44+2*i:], uint16(int16(1000+i%2000)))
	}
	return b
}

// shiftAnnouncement moves every instant of the party's announcement by
// deltaMs (negative: into the past).
func shiftAnnouncement(s *Store, profileID string, deltaMs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ann := s.audio[profileID].ann
	ann.chimeAtMs += deltaMs
	ann.voiceStartsAtMs += deltaMs
	if !ann.open() {
		ann.voiceEndsAtMs += deltaMs
	}
	if ann.stoppedAtMs > 0 {
		ann.stoppedAtMs += deltaMs
	}
	if ann.kind == KindLive {
		ann.captureFromMs += deltaMs
		ann.maxEndsAtMs += deltaMs
	}
}

// endedAgo moves the announcement so that its effective end was agoMs ago.
func endedAgo(s *Store, profileID string, agoMs int64) {
	s.mu.RLock()
	end, _ := s.audio[profileID].ann.effectiveEnd()
	s.mu.RUnlock()
	shiftAnnouncement(s, profileID, time.Now().UnixMilli()-agoMs-end)
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// 11. The server picks the instants; every parked poll wakes; nothing of
// the intent, anchor or schedule bookkeeping moves.
func TestStartClipPicksTheInstantsAndWakesTheParty(t *testing.T) {
	s, st := audioParty(t)
	before := snapshot(s, "p1")
	woke := make(chan struct{}, 1)
	go func() {
		s.WaitForChange(context.Background(), "p1", st.Revision, 5*time.Second)
		woke <- struct{}{}
	}()
	for deadline := time.Now().Add(2 * time.Second); s.Waiting("p1") < 1; {
		if time.Now().After(deadline) {
			t.Fatal("the poll never parked")
		}
		time.Sleep(2 * time.Millisecond)
	}

	wav := wav16k(3 * 16000)
	t0 := time.Now().UnixMilli()
	got, ann, out := s.StartClip("p1", "tok-remote", "Den Remote", wav, 3000)
	t1 := time.Now().UnixMilli()
	if out != OutcomeOK || ann == nil {
		t.Fatalf("start: %v", out)
	}
	if ann.ChimeAtMs < t0+ClipChimeLeadMs || ann.ChimeAtMs > t1+ClipChimeLeadMs+50 ||
		ann.VoiceStartsAtMs != ann.ChimeAtMs+1100 || ann.DurationMs != 3000 ||
		ann.VoiceEndsAtMs != ann.VoiceStartsAtMs+3000 {
		t.Fatalf("instants: %+v (sent between %d and %d)", ann, t0, t1)
	}
	if ann.Kind != KindClip || ann.ByLabel != "Den Remote" || ann.ByDeviceKey != "tok-remote" ||
		ann.SampleRate != 16000 || ann.SizeBytes != len(wav) || ann.StoppedAtMs != 0 {
		t.Fatalf("announcement: %+v", ann)
	}
	if raw, err := hex.DecodeString(ann.ID); err != nil || len(raw) != 16 || ann.ID != hex.EncodeToString(raw) {
		t.Fatalf("id %q is not 32 lowercase hex characters", ann.ID)
	}
	if got.Announcement == nil || *got.Announcement != *ann {
		t.Fatalf("the state's announcement: %+v", got.Announcement)
	}
	select {
	case <-woke:
	case <-time.After(2 * time.Second):
		t.Fatal("the parked poll never woke")
	}
	if after := snapshot(s, "p1"); after != before {
		t.Fatalf("an announcement moved the bookkeeping:\nbefore %+v\nafter  %+v", before, after)
	}
	if _, out := s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true, DurationSec: 200},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(st.Revision)}); out != OutcomeOK {
		t.Fatalf("the leader's heartbeat after an announcement: %v", out)
	}
	if again, _ := s.Get("p1"); again.Announcement == nil || again.Announcement.ID != ann.ID {
		t.Fatal("a heartbeat dropped the announcement")
	}
}

// 12. One at a time: busy until 1 s past the end; listed until 3 s past
// it, and leaving the listing wakes nobody.
func TestAnnouncementBusyAndListedWindows(t *testing.T) {
	s, _ := audioParty(t)
	_, first, _ := s.StartClip("p1", "tok-tv", "Living Room TV", wav16k(16000), 1000)
	st, _, out := s.StartClip("p1", "tok-pc", "Windows PC", wav16k(16000), 1000)
	if out != OutcomeAnnounceBusy || st.Announcement == nil || st.Announcement.ByLabel != "Living Room TV" {
		t.Fatalf("a second clip while one plays: %v %+v", out, st.Announcement)
	}
	endedAgo(s, "p1", 900)
	if _, _, out := s.StartClip("p1", "tok-pc", "Windows PC", wav16k(16000), 1000); out != OutcomeAnnounceBusy {
		t.Fatalf("900 ms past the end: %v, want busy", out)
	}
	endedAgo(s, "p1", 2900)
	r := revOf(s, "p1")
	if st, _ := s.Get("p1"); st.Announcement == nil || st.Announcement.ID != first.ID {
		t.Fatal("unlisted before 3 s past the end")
	}
	endedAgo(s, "p1", 3100)
	if st, _ := s.Get("p1"); st.Announcement != nil {
		t.Fatalf("still listed 3.1 s past the end: %+v", st.Announcement)
	}
	if revOf(s, "p1") != r {
		t.Fatal("leaving the listing bumped the revision")
	}
	if _, next, out := s.StartClip("p1", "tok-pc", "Windows PC", wav16k(16000), 1000); out != OutcomeOK || next.ID == first.ID {
		t.Fatalf("a clip 3.1 s after the last: %v", out)
	}
}

// 13. Six a minute per profile — and a new party buys no fresh allowance.
func TestAnnouncementsAreRateLimitedPerProfile(t *testing.T) {
	s, _ := audioParty(t)
	for i := 0; i < AnnounceStartsPerMinute; i++ {
		if _, _, out := s.StartClip("p1", "tok-tv", "TV", wav16k(16000), 1000); out != OutcomeOK {
			t.Fatalf("start %d: %v", i+1, out)
		}
		endedAgo(s, "p1", 1100)
	}
	if _, _, out := s.StartClip("p1", "tok-tv", "TV", wav16k(16000), 1000); out != OutcomeRateLimited {
		t.Fatalf("the 7th in a minute: %v, want rate-limited", out)
	}
	// A stop-all and a fresh party do not reset it.
	s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-all")
	s.Report("p1", State{ItemID: "b", IsPlaying: true}, ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)})
	if _, _, out := s.StartClip("p1", "tok-phone", "Kate's iPhone", wav16k(16000), 1000); out != OutcomeRateLimited {
		t.Fatalf("after a stop-all: %v, want rate-limited", out)
	}
	// Another profile has its own.
	s.Report("p2", State{ItemID: "q", IsPlaying: true}, ReportOpts{DeviceID: "tok-sarah", BasedOnRevision: rev(0)})
	if _, _, out := s.StartClip("p2", "tok-sarah", "Sarah", wav16k(16000), 1000); out != OutcomeOK {
		t.Fatalf("another profile: %v", out)
	}
	s.mu.Lock()
	for i := range s.announced["p1"] {
		s.announced["p1"][i] -= announceWindowMs + 1
	}
	s.mu.Unlock()
	if _, _, out := s.StartClip("p1", "tok-phone", "Kate's iPhone", wav16k(16000), 1000); out != OutcomeOK {
		t.Fatalf("a minute later: %v", out)
	}
}

// 14. Every party's clip bytes together are capped, and given back.
func TestClipBytesAreCappedAcrossProfiles(t *testing.T) {
	s, _ := audioParty(t)
	wav := wav16k(16000)
	s.mu.Lock()
	s.clipBytesTotal = MaxClipBytesTotal - int64(len(wav)) + 1
	s.mu.Unlock()
	if _, _, out := s.StartClip("p1", "tok-tv", "TV", wav, 1000); out != OutcomeServerBusy {
		t.Fatalf("over the cap: %v, want server-busy", out)
	}
	if !allZero(wav) {
		t.Fatal("a refused clip's bytes were not zeroed")
	}
	s.mu.Lock()
	s.clipBytesTotal = 0
	s.mu.Unlock()

	a, b := wav16k(16000), wav16k(32000)
	s.StartClip("p1", "tok-tv", "TV", a, 1000)
	endedAgo(s, "p1", 1100)
	s.StartClip("p1", "tok-tv", "TV", b, 2000) // replaces the first
	s.mu.RLock()
	total := s.clipBytesTotal
	s.mu.RUnlock()
	if total != int64(len(b)) || !allZero(a) {
		t.Fatalf("after a replacement: %d bytes held, want %d; first zeroed %v", total, len(b), allZero(a))
	}
	s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-all")
	s.mu.RLock()
	total = s.clipBytesTotal
	s.mu.RUnlock()
	if total != 0 || !allZero(b) {
		t.Fatalf("after a stop-all: %d bytes held; zeroed %v", total, allZero(b))
	}
}

// 15 and 9. The leader-only switch: the seat holder's alone, true only
// while its setter holds the seat, and it stops members, not the holder.
func TestAnnouncePolicyFollowsItsSetter(t *testing.T) {
	s, _ := audioParty(t)
	for _, tok := range []string{"tok-tv", "tok-remote", ""} {
		if _, out := s.SetAnnouncePolicy("p1", tok, true); out != OutcomeNotLeader {
			t.Errorf("%q set the policy: %v", tok, out)
		}
	}
	r := revOf(s, "p1")
	st, out := s.SetAnnouncePolicy("p1", "tok-phone", true)
	if out != OutcomeOK || !st.AnnounceLeaderOnly || revOf(s, "p1") <= r {
		t.Fatalf("the holder's switch: %v %+v", out, st.AnnounceLeaderOnly)
	}
	r = revOf(s, "p1")
	if _, out := s.SetAnnouncePolicy("p1", "tok-phone", true); out != OutcomeOK || revOf(s, "p1") != r {
		t.Fatal("an unchanged policy bumped")
	}
	if _, _, out := s.StartClip("p1", "tok-tv", "TV", wav16k(16000), 1000); out != OutcomeLeaderOnly {
		t.Fatalf("a member while leader-only: %v", out)
	}
	if _, _, out := s.StartClip("p1", "tok-phone", "Kate's iPhone", wav16k(16000), 1000); out != OutcomeOK {
		t.Fatalf("the holder while leader-only: %v", out)
	}

	// Handed over: the TV leads, and the switch reads off.
	st, _ = s.HandOver("p1", "tok-phone", "Kate's iPhone", "uuid-tv")
	if st.AnnounceLeaderOnly {
		t.Fatal("the switch outlived its setter's seat")
	}
	// The setter takes the seat back: its setting applies again.
	st, out = s.Report("p1", State{Source: "plex", ItemID: "a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(revOf(s, "p1")), TakeOver: true})
	if out != OutcomeOK || !st.AnnounceLeaderOnly {
		t.Fatalf("the setter back in the seat: %v %v", out, st.AnnounceLeaderOnly)
	}
	// A dormant holder still holds it.
	s.mu.Lock()
	cur := s.m["p1"]
	cur.LeaderReportedAtMs -= leaderLostAfter.Milliseconds() + 1
	s.m["p1"] = cur
	s.mu.Unlock()
	if st, _ := s.Get("p1"); !st.LeaderDormant || !st.AnnounceLeaderOnly {
		t.Fatalf("a dormant setter: dormant %v, leader-only %v", st.LeaderDormant, st.AnnounceLeaderOnly)
	}
	if _, out := s.SetAnnouncePolicy("p1", "tok-phone", false); out != OutcomeOK {
		t.Fatalf("a dormant holder switching it off: %v", out)
	}
	s.SetAnnouncePolicy("p1", "tok-phone", true)
	// A vacant seat reads off.
	s.EndBy("p1", "tok-phone", "Kate's iPhone", "stopped")
	if st, _ := s.Get("p1"); st.LeaderTokenID != "" || st.AnnounceLeaderOnly {
		t.Fatalf("a vacant seat: %+v", st)
	}
	if _, out := s.SetAnnouncePolicy("p1", "tok-phone", true); out != OutcomeNotLeader {
		t.Fatalf("the old holder with the seat vacant: %v", out)
	}
}

// 16. A copy for the party only; zeroed once past its retention, by the
// sweep or the timer.
func TestClipIsACopyForThePartyAndIsZeroedAfterwards(t *testing.T) {
	s, _ := audioParty(t)
	s.Report("p2", State{ItemID: "q", IsPlaying: true}, ReportOpts{DeviceID: "tok-sarah", BasedOnRevision: rev(0)})
	wav := wav16k(16000)
	orig := append([]byte(nil), wav...)
	_, ann, _ := s.StartClip("p1", "tok-remote", "Den Remote", wav, 1000)

	got, out := s.Clip("p1", "tok-tv", ann.ID)
	if out != OutcomeOK || !bytes.Equal(got, orig) {
		t.Fatalf("fetch: %v, %d bytes", out, len(got))
	}
	got[100] ^= 0xFF
	if again, _ := s.Clip("p1", "tok-viewer", ann.ID); !bytes.Equal(again, orig) {
		t.Fatal("mutating a fetched copy changed the stored clip")
	}
	for name, c := range map[string]struct{ profile, tok, id string }{
		"another profile":  {"p2", "tok-sarah", ann.ID},
		"not in the party": {"p1", "tok-stranger", ann.ID},
		"no device":        {"p1", "", ann.ID},
		"unknown id":       {"p1", "tok-tv", "00000000000000000000000000000000"},
	} {
		if _, out := s.Clip(c.profile, c.tok, c.id); out != OutcomeNotFound {
			t.Errorf("%s: %v, want not-found", name, out)
		}
	}

	// Past its retention the next read deletes it, bytes zeroed in place.
	endedAgo(s, "p1", RetainAfterEndMs-100)
	if _, out := s.Clip("p1", "tok-tv", ann.ID); out != OutcomeOK {
		t.Fatalf("within its retention: %v", out)
	}
	endedAgo(s, "p1", RetainAfterEndMs+1)
	if st, _ := s.Get("p1"); st.Announcement != nil {
		t.Fatal("listed past its retention")
	}
	if _, out := s.Clip("p1", "tok-tv", ann.ID); out != OutcomeNotFound || !allZero(wav) {
		t.Fatalf("past its retention: %v, zeroed %v", out, allZero(wav))
	}

	// The timer's path: early it does nothing; past the time it deletes;
	// a stale id does nothing.
	second := wav16k(16000)
	_, ann2, _ := s.StartClip("p1", "tok-tv", "TV", second, 1000)
	s.expireAnnouncement("p1", ann2.ID)
	if _, out := s.Clip("p1", "tok-tv", ann2.ID); out != OutcomeOK {
		t.Fatalf("an early timer deleted the clip: %v", out)
	}
	endedAgo(s, "p1", RetainAfterEndMs+1)
	s.expireAnnouncement("p1", ann.ID)
	s.mu.RLock()
	still := s.audio["p1"].ann != nil
	s.mu.RUnlock()
	if !still {
		t.Fatal("another announcement's timer deleted this one")
	}
	s.expireAnnouncement("p1", ann2.ID)
	s.mu.RLock()
	gone, total := s.audio["p1"].ann == nil, s.clipBytesTotal
	s.mu.RUnlock()
	if !gone || !allZero(second) || total != 0 {
		t.Fatalf("the timer: gone %v, zeroed %v, %d bytes held", gone, allZero(second), total)
	}
}

// 17. The seat holder or the sender stops it; a repeat changes nothing.
func TestStopAnnouncement(t *testing.T) {
	s, _ := audioParty(t)
	_, ann, _ := s.StartClip("p1", "tok-tv", "Living Room TV", wav16k(3*16000), 3000)
	if _, out := s.StopAnnouncement("p1", "tok-pc", "Windows PC", ann.ID); out != OutcomeNotAllowed {
		t.Fatalf("a bystander: %v", out)
	}
	if _, out := s.StopAnnouncement("p1", "tok-tv", "Living Room TV", "nope"); out != OutcomeNotFound {
		t.Fatalf("an unknown id: %v", out)
	}
	r := revOf(s, "p1")
	st, out := s.StopAnnouncement("p1", "tok-tv", "Living Room TV", ann.ID)
	if out != OutcomeOK || st.Announcement == nil || st.Announcement.StoppedAtMs == 0 ||
		st.Announcement.StoppedByLabel != "Living Room TV" || revOf(s, "p1") <= r {
		t.Fatalf("the sender's stop: %v %+v", out, st.Announcement)
	}
	r = revOf(s, "p1")
	if _, out := s.StopAnnouncement("p1", "tok-phone", "Kate's iPhone", ann.ID); out != OutcomeOK || revOf(s, "p1") != r {
		t.Fatalf("a repeat stop: %v, bumped %v", out, revOf(s, "p1") != r)
	}
	endedAgo(s, "p1", 2900)
	if st, _ := s.Get("p1"); st.Announcement == nil {
		t.Fatal("unlisted before 3 s past the stop")
	}
	endedAgo(s, "p1", 3100)
	if st, _ := s.Get("p1"); st.Announcement != nil {
		t.Fatal("listed 3.1 s past the stop")
	}

	// The seat holder stops another device's.
	_, other, _ := s.StartClip("p1", "tok-pc", "Windows PC", wav16k(16000), 1000)
	if st, out := s.StopAnnouncement("p1", "tok-phone", "Kate's iPhone", other.ID); out != OutcomeOK ||
		st.Announcement.StoppedByLabel != "Kate's iPhone" {
		t.Fatalf("the holder's stop: %v", out)
	}
	// One that already ended on its own is not stopped after the fact.
	endedAgo(s, "p1", 1100)
	_, done, _ := s.StartClip("p1", "tok-pc", "Windows PC", wav16k(16000), 1000)
	shiftAnnouncement(s, "p1", -5000)
	r = revOf(s, "p1")
	if st, out := s.StopAnnouncement("p1", "tok-pc", "Windows PC", done.ID); out != OutcomeOK ||
		revOf(s, "p1") != r || (st.Announcement != nil && st.Announcement.StoppedAtMs != 0) {
		t.Fatalf("a stop after the end: %v %+v", out, st.Announcement)
	}
}

// 18. Exactly the one format.
func TestParseClipWAV(t *testing.T) {
	le := binary.LittleEndian
	mut := func(n int, f func(b []byte)) []byte {
		b := wav16k(n)
		f(b)
		return b
	}
	good := wav16k(16000)
	odd := append(wav16k(16000), 0)
	le.PutUint32(odd[4:], uint32(len(odd)-8))
	le.PutUint32(odd[40:], uint32(len(odd)-44))
	for _, c := range []struct {
		name string
		in   []byte
		ms   int64
		code string
	}{
		{"a good file", good, 1000, ""},
		{"exactly 300 ms", wav16k(4800), 300, ""},
		{"exactly 60.5 s", wav16k(968_000), 60_500, ""},
		{"44.1 kHz", mut(16000, func(b []byte) { le.PutUint32(b[24:], 44100); le.PutUint32(b[28:], 88200) }), 0, "bad-audio"},
		{"stereo", mut(16000, func(b []byte) { le.PutUint16(b[22:], 2) }), 0, "bad-audio"},
		{"8-bit", mut(16000, func(b []byte) { le.PutUint16(b[34:], 8); le.PutUint16(b[32:], 1) }), 0, "bad-audio"},
		{"not PCM", mut(16000, func(b []byte) { le.PutUint16(b[20:], 3) }), 0, "bad-audio"},
		{"fmt size 18", mut(16000, func(b []byte) { le.PutUint32(b[16:], 18) }), 0, "bad-audio"},
		{"byte rate wrong", mut(16000, func(b []byte) { le.PutUint32(b[28:], 16000) }), 0, "bad-audio"},
		{"an odd data length", odd, 0, "bad-audio"},
		{"RIFF size wrong", mut(16000, func(b []byte) { le.PutUint32(b[4:], 7) }), 0, "bad-audio"},
		{"data size wrong", mut(16000, func(b []byte) { le.PutUint32(b[40:], 2) }), 0, "bad-audio"},
		{"not RIFF", mut(16000, func(b []byte) { copy(b, "RIFX") }), 0, "bad-audio"},
		{"a LIST chunk where data goes", mut(16000, func(b []byte) { copy(b[36:], "LIST") }), 0, "bad-audio"},
		{"299 ms", wav16k(4784), 299, "too-short"},
		{"no samples", wav16k(0), 0, "too-short"},
		{"60.501 s", wav16k(968_016), 60_501, "too-large"},
		{"empty", nil, 0, "bad-audio"},
		{"a header only, short", good[:43], 0, "bad-audio"},
		{"garbage", bytes.Repeat([]byte{0x5A}, 4000), 0, "bad-audio"},
	} {
		ms, code := ParseClipWAV(c.in)
		if code != c.code || (c.code == "" && ms != c.ms) || (c.code == "too-short" && ms != c.ms) {
			t.Errorf("%s: %d ms %q, want %d ms %q", c.name, ms, code, c.ms, c.code)
		}
	}
}

// 19. The clip goes with the party — a stop-all, a deleting DELETE or an
// expiry — and stays through a hand-over and a vacant seat.
func TestTheClipGoesWithTheParty(t *testing.T) {
	held := func(s *Store) int64 {
		s.mu.RLock()
		defer s.mu.RUnlock()
		return s.clipBytesTotal
	}
	for name, end := range map[string]func(s *Store){
		"stop-all": func(s *Store) { s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-all") },
		"delete":   func(s *Store) { s.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-sharing") },
		"expiry": func(s *Store) {
			s.mu.Lock()
			st := s.m["p1"]
			st.UpdatedAtMs = time.Now().Add(-2 * time.Minute).UnixMilli()
			s.m["p1"] = st
			s.lastActivity["p1"] = time.Now().Add(-2 * time.Minute)
			s.mu.Unlock()
			s.Get("p1")
		},
	} {
		s, _ := audioParty(t)
		wav := wav16k(16000)
		_, ann, _ := s.StartClip("p1", "tok-tv", "TV", wav, 1000)
		end(s)
		if held(s) != 0 || !allZero(wav) {
			t.Errorf("%s: %d bytes held, zeroed %v", name, held(s), allZero(wav))
		}
		if _, out := s.Clip("p1", "tok-tv", ann.ID); out == OutcomeOK {
			t.Errorf("%s: the clip is still served", name)
		}
	}

	s, _ := audioParty(t)
	_, ann, _ := s.StartClip("p1", "tok-tv", "TV", wav16k(16000), 1000)
	s.HandOver("p1", "tok-phone", "Kate's iPhone", "tok-pc")
	s.JoinFollow("p1", "tok-phone", "tok-phone", "Kate's iPhone", RoleFollower, nil, 0, nil)
	s.EndBy("p1", "tok-pc", "Windows PC", "stopped")
	if _, out := s.Clip("p1", "tok-tv", ann.ID); out != OutcomeOK || held(s) == 0 {
		t.Fatalf("a hand-over and a vacant seat dropped the clip: %v", out)
	}
}
