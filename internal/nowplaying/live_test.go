package nowplaying

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"
)

// Live announcements (live.go): a relay per session that never touches
// the party's revision for a chunk, readers parked in a pool of their
// own, and nothing kept past its time.

// pcm16 is n samples of a pattern that depends on seed and is never zero.
func pcm16(n int, seed int) []byte {
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(100+seed*1000+i%500)))
	}
	return b
}

// captureAgo moves the party's live announcement so that capture began
// agoMs ago (every instant moves with it).
func captureAgo(s *Store, profileID string, agoMs int64) {
	s.mu.RLock()
	from := s.audio[profileID].ann.captureFromMs
	s.mu.RUnlock()
	shiftAnnouncement(s, profileID, time.Now().UnixMilli()-agoMs-from)
}

// pinRev moves the profile's revision far ahead of the clock, so each
// bump from here is exactly +1 and bumps can be counted.
func pinRev(s *Store, profileID string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rev[profileID] = time.Now().UnixMilli() + 1_000_000_000
	return s.rev[profileID]
}

func liveCount(s *Store) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.liveSessions
}

func liveWaiting(s *Store, profileID string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if a := s.audio[profileID]; a != nil && a.ann != nil && a.ann.live != nil {
		return a.ann.live.waiting
	}
	return -1
}

func waitFor(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(within); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v", what, within)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// 20. The instants, and exactly one bump.
func TestStartLivePicksTheInstants(t *testing.T) {
	s, _ := audioParty(t)
	p := pinRev(s, "p1")
	t0 := time.Now().UnixMilli()
	st, ann, out := s.StartLive("p1", "tok-remote", "Den Remote")
	t1 := time.Now().UnixMilli()
	if out != OutcomeOK || ann == nil {
		t.Fatalf("start: %v", out)
	}
	if ann.ChimeAtMs < t0+1000 || ann.ChimeAtMs > t1+1000 || ann.CaptureFromMs != ann.ChimeAtMs+1150 ||
		ann.VoiceStartsAtMs != ann.CaptureFromMs+1000 || ann.MaxEndsAtMs != ann.VoiceStartsAtMs+120_000 {
		t.Fatalf("instants: %+v (sent between %d and %d)", ann, t0, t1)
	}
	if ann.Kind != KindLive || !ann.Live || ann.Ended || ann.EndSample != nil || ann.VoiceEndsAtMs != 0 ||
		ann.DurationMs != 0 || ann.SizeBytes != 0 || ann.SampleRate != 16000 || ann.ByDeviceKey != "tok-remote" {
		t.Fatalf("announcement: %+v", ann)
	}
	if bumps := revOf(s, "p1") - p; bumps != 1 {
		t.Fatalf("%d bumps, want 1", bumps)
	}
	if st.Announcement == nil || st.Announcement.ID != ann.ID || !st.Announcement.Live {
		t.Fatalf("the state: %+v", st.Announcement)
	}
	if liveCount(s) != 1 {
		t.Fatalf("live sessions %d", liveCount(s))
	}
	// An open live session is busy, and never listed off.
	if _, _, out := s.StartClip("p1", "tok-tv", "TV", wav16k(16000), 1000); out != OutcomeAnnounceBusy {
		t.Fatalf("a clip during live: %v", out)
	}
	if _, _, out := s.StartLive("p1", "tok-tv", "TV"); out != OutcomeAnnounceBusy {
		t.Fatalf("live during live: %v", out)
	}
	captureAgo(s, "p1", 600_000)
	if st, _ := s.Get("p1"); st.Announcement == nil {
		t.Fatal("an open live announcement was unlisted")
	}
	// A live id is not a clip.
	if _, out := s.Clip("p1", "tok-tv", ann.ID); out != OutcomeNotFound {
		t.Fatalf("a live id fetched as a clip: %v", out)
	}
}

// 21. Appends: the talker only, a retry is idempotent, a gap is silence,
// nothing far ahead, and no party revision for a chunk.
func TestAppendLive(t *testing.T) {
	s, _ := audioParty(t)
	s.Report("p2", State{ItemID: "q", IsPlaying: true}, ReportOpts{DeviceID: "tok-remote", BasedOnRevision: rev(0)})
	_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
	if _, _, out := s.AppendLive("p1", "tok-remote", ann.ID, 0, pcm16(1600, 1)); out != OutcomeAhead {
		t.Fatalf("a chunk before capture can have begun: %v, want ahead", out)
	}
	captureAgo(s, "p1", 3000)
	for name, c := range map[string]struct{ profile, tok, id string }{
		"a member, not the talker": {"p1", "tok-tv", ann.ID},
		"a stranger":               {"p1", "tok-stranger", ann.ID},
		"no device":                {"p1", "", ann.ID},
		"another profile":          {"p2", "tok-remote", ann.ID},
		"an unknown id":            {"p1", "tok-remote", "nope"},
	} {
		chunk := pcm16(1600, 9)
		if _, _, out := s.AppendLive(c.profile, c.tok, c.id, 0, chunk); out != OutcomeNotFound || !allZero(chunk) {
			t.Errorf("%s: %v (refused chunk zeroed %v)", name, out, allZero(chunk))
		}
	}

	p := pinRev(s, "p1")
	one := pcm16(1600, 1)
	if total, _, out := s.AppendLive("p1", "tok-remote", ann.ID, 0, append([]byte(nil), one...)); out != OutcomeOK || total != 1600 {
		t.Fatalf("first chunk: %v %d", out, total)
	}
	// A retry of the same chunk changes nothing; one that overlaps adds
	// only what is new.
	if total, _, _ := s.AppendLive("p1", "tok-remote", ann.ID, 0, pcm16(1600, 7)); total != 1600 {
		t.Fatalf("a retry: total %d", total)
	}
	two := pcm16(1600, 2)
	if total, _, _ := s.AppendLive("p1", "tok-remote", ann.ID, 800, append(pcm16(800, 7), two[:1600]...)); total != 2400 {
		t.Fatalf("an overlap: total %d, want 2400", total)
	}
	// A gap reads back as silence.
	three := pcm16(1600, 3)
	if total, _, _ := s.AppendLive("p1", "tok-remote", ann.ID, 4000, append([]byte(nil), three...)); total != 5600 {
		t.Fatalf("after a gap: total %d", total)
	}
	r, out := s.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 0, 0)
	want := append(append(append(append([]byte(nil), one...), two[:1600]...), make([]byte, 2*1600)...), three...)
	if out != OutcomeOK || r.From != 0 || r.Total != 5600 || r.Ended || r.Stopped || r.Skipped || !bytes.Equal(r.PCM, want) {
		t.Fatalf("read back: %v from %d total %d, %d bytes, equal %v", out, r.From, r.Total, len(r.PCM), bytes.Equal(r.PCM, want))
	}
	if _, _, out := s.AppendLive("p1", "tok-remote", ann.ID, (3000+LiveAheadSlackMs+500)*16, pcm16(16, 1)); out != OutcomeAhead {
		t.Fatalf("a chunk too far ahead: %v", out)
	}

	captureAgo(s, "p1", 10_000)
	for i := 0; i < 50; i++ {
		if _, _, out := s.AppendLive("p1", "tok-remote", ann.ID, 5600+int64(i)*1600, pcm16(1600, i)); out != OutcomeOK {
			t.Fatalf("append %d: %v", i, out)
		}
	}
	if bumps := revOf(s, "p1") - p; bumps != 0 {
		t.Fatalf("%d party bumps across chunks, want 0", bumps)
	}
}

// 21. The sample cap ends it, with one bump; after the end, live-ended.
func TestLiveEndsAtItsSampleCap(t *testing.T) {
	s, _ := audioParty(t)
	_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
	// The last chunk starts at 58 × 32768 = 1,900,544: capture began long
	// enough ago for it not to be ahead, and maxEndsAt is still to come.
	captureAgo(s, "p1", 117_500)
	p := pinRev(s, "p1")
	const chunk = MaxLiveChunkBytes / 2
	var total, endSample int64
	var out Outcome
	for from := int64(0); from < MaxLiveSamples; from += chunk {
		total, endSample, out = s.AppendLive("p1", "tok-remote", ann.ID, from, pcm16(chunk, int(from)))
		if out != OutcomeOK {
			t.Fatalf("chunk at %d: %v", from, out)
		}
	}
	if total != MaxLiveSamples || endSample != MaxLiveSamples {
		t.Fatalf("at the cap: total %d end %d", total, endSample)
	}
	if bumps := revOf(s, "p1") - p; bumps != 1 {
		t.Fatalf("%d bumps, want 1", bumps)
	}
	st, _ := s.Get("p1")
	if a := st.Announcement; a == nil || !a.Ended || a.Live || a.EndSample == nil || *a.EndSample != MaxLiveSamples ||
		a.VoiceEndsAtMs != a.VoiceStartsAtMs+LiveMaxMs {
		t.Fatalf("ended at the cap: %+v", st.Announcement)
	}
	if _, end, out := s.AppendLive("p1", "tok-remote", ann.ID, MaxLiveSamples, pcm16(16, 1)); out != OutcomeLiveEnded || end != MaxLiveSamples {
		t.Fatalf("after the end: %v %d", out, end)
	}
	if liveCount(s) != 0 {
		t.Fatalf("live sessions %d", liveCount(s))
	}
}

// 22. Readers: a parked one wakes on a chunk at once; one that fell
// behind skips ahead; past 32 parked a reader is answered at once; and
// none of it is the profile's long-poll pool.
func TestReadLive(t *testing.T) {
	s, _ := audioParty(t)
	_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
	captureAgo(s, "p1", 3000)
	if _, out := s.ReadLive(context.Background(), "p1", "tok-stranger", ann.ID, 0, 0); out != OutcomeNotFound {
		t.Fatalf("a stranger: %v", out)
	}

	got := make(chan LiveRead, 1)
	go func() {
		r, _ := s.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 0, 3*time.Second)
		got <- r
	}()
	waitFor(t, "a parked reader", 2*time.Second, func() bool { return liveWaiting(s, "p1") == 1 })
	if n := s.Waiting("p1"); n != 0 {
		t.Fatalf("a live reader took a long-poll place: %d", n)
	}
	chunk := pcm16(1600, 4)
	sent := time.Now()
	s.AppendLive("p1", "tok-remote", ann.ID, 0, append([]byte(nil), chunk...))
	select {
	case r := <-got:
		if took := time.Since(sent); took > 50*time.Millisecond {
			t.Errorf("the reader woke %v after the chunk", took)
		}
		if r.From != 0 || r.Total != 1600 || !bytes.Equal(r.PCM, chunk) {
			t.Fatalf("woken read: from %d total %d, %d bytes", r.From, r.Total, len(r.PCM))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the parked reader never woke")
	}

	// 32 parked; the 33rd is answered at once, with nothing; a chunk wakes
	// them all.
	done := make(chan LiveRead, MaxLiveWaitersPerSession)
	for i := 0; i < MaxLiveWaitersPerSession; i++ {
		go func() {
			r, _ := s.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 1600, 5*time.Second)
			done <- r
		}()
	}
	waitFor(t, "32 parked readers", 2*time.Second, func() bool { return liveWaiting(s, "p1") == MaxLiveWaitersPerSession })
	began := time.Now()
	r, out := s.ReadLive(context.Background(), "p1", "tok-viewer", ann.ID, 1600, 5*time.Second)
	if took := time.Since(began); out != OutcomeOK || took > 200*time.Millisecond || len(r.PCM) != 0 || r.Ended {
		t.Fatalf("the 33rd reader: %v after %v, %d bytes", out, took, len(r.PCM))
	}
	if n := s.Waiting("p1"); n != 0 {
		t.Fatalf("live readers took long-poll places: %d", n)
	}
	s.AppendLive("p1", "tok-remote", ann.ID, 1600, pcm16(1600, 5))
	for i := 0; i < MaxLiveWaitersPerSession; i++ {
		select {
		case r := <-done:
			if r.From != 1600 || len(r.PCM) != 2*1600 {
				t.Fatalf("a woken reader: from %d, %d bytes", r.From, len(r.PCM))
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d parked readers woke", i, MaxLiveWaitersPerSession)
		}
	}
	waitFor(t, "the pool emptying", time.Second, func() bool { return liveWaiting(s, "p1") == 0 })

	// One that fell behind: what was heard over LiveRetainMs ago is gone.
	s.AppendLive("p1", "tok-remote", ann.ID, 3200, pcm16(1600, 6))
	captureAgo(s, "p1", 4250) // the voice began 3.25 s ago: everything before ~4000 is dropped
	r, _ = s.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 0, 0)
	if !r.Skipped || r.From < 4000 || r.From >= 4800 || r.Total != 4800 || len(r.PCM) != int(2*(4800-r.From)) {
		t.Fatalf("a reader behind: skipped %v from %d total %d, %d bytes", r.Skipped, r.From, r.Total, len(r.PCM))
	}
	if !bytes.Equal(r.PCM, pcm16(1600, 6)[2*(r.From-3200):]) {
		t.Fatal("a skipping reader got the wrong samples")
	}
}

// 23. The watchdog ends a quiet live announcement at what arrived, once —
// counted from the last chunk, or from capture when none ever came.
func TestTheWatchdogEndsAQuietLiveAnnouncement(t *testing.T) {
	old := liveIdleEnd
	liveIdleEnd = 50 * time.Millisecond
	t.Cleanup(func() { liveIdleEnd = old })
	ended := func(s *Store) bool {
		s.mu.RLock()
		defer s.mu.RUnlock()
		a := s.audio["p1"]
		return a != nil && a.ann != nil && a.ann.ended
	}

	t.Run("after a chunk", func(t *testing.T) {
		s, _ := audioParty(t)
		_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
		captureAgo(s, "p1", 3000)
		s.AppendLive("p1", "tok-remote", ann.ID, 0, pcm16(1600, 1))
		p := pinRev(s, "p1")
		s.liveIdle("p1", ann.ID) // a timer that lost the race with the chunk
		if ended(s) {
			t.Fatal("an early watchdog ended it")
		}
		waitFor(t, "the watchdog", time.Second, func() bool { return ended(s) })
		st, _ := s.Get("p1")
		if a := st.Announcement; a == nil || a.EndSample == nil || *a.EndSample != 1600 || a.VoiceEndsAtMs != a.VoiceStartsAtMs+100 {
			t.Fatalf("ended: %+v", st.Announcement)
		}
		if bumps := revOf(s, "p1") - p; bumps != 1 || liveCount(s) != 0 {
			t.Fatalf("%d bumps, %d live sessions", bumps, liveCount(s))
		}
	})

	t.Run("no chunk ever, counted from capture", func(t *testing.T) {
		s, _ := audioParty(t)
		_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
		p := pinRev(s, "p1")
		due := time.UnixMilli(ann.CaptureFromMs).Add(liveIdleEnd)
		if time.Now().After(due.Add(-time.Second)) {
			t.Fatal("capture starts too soon to tell")
		}
		waitFor(t, "the watchdog", 4*time.Second, func() bool { return ended(s) })
		if late := time.Since(due); late < 0 || late > 500*time.Millisecond {
			t.Fatalf("ended %v from captureFrom + idle", late)
		}
		st, _ := s.Get("p1")
		if a := st.Announcement; a == nil || a.EndSample == nil || *a.EndSample != 0 || a.VoiceEndsAtMs != a.VoiceStartsAtMs {
			t.Fatalf("ended: %+v", st.Announcement)
		}
		if bumps := revOf(s, "p1") - p; bumps != 1 {
			t.Fatalf("%d bumps", bumps)
		}
	})
}

// 24. The talker ends it: clamped, once, its end visible; a stop ends it
// too and tells every reader to stop.
func TestEndLiveAndStop(t *testing.T) {
	s, _ := audioParty(t)
	_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
	captureAgo(s, "p1", 3000)
	kept := pcm16(3200, 1)
	s.AppendLive("p1", "tok-remote", ann.ID, 0, kept)
	if _, _, out := s.EndLive("p1", "tok-tv", ann.ID, nil); out != OutcomeNotFound {
		t.Fatalf("a member ending another's live: %v", out)
	}
	p := pinRev(s, "p1")
	tooMany := int64(99_999)
	st, v, out := s.EndLive("p1", "tok-remote", ann.ID, &tooMany)
	if out != OutcomeOK || !v.Ended || v.Live || v.EndSample == nil || *v.EndSample != 3200 ||
		v.VoiceEndsAtMs != v.VoiceStartsAtMs+200 {
		t.Fatalf("end: %v %+v", out, v)
	}
	if st.Announcement == nil || !st.Announcement.Ended || revOf(s, "p1")-p != 1 || liveCount(s) != 0 {
		t.Fatalf("after the end: %+v, %d bumps", st.Announcement, revOf(s, "p1")-p)
	}
	if _, v2, out := s.EndLive("p1", "tok-remote", ann.ID, nil); out != OutcomeOK || revOf(s, "p1")-p != 1 || *v2.EndSample != 3200 {
		t.Fatalf("a repeat end: %v, %d bumps", out, revOf(s, "p1")-p)
	}
	if _, end, out := s.AppendLive("p1", "tok-remote", ann.ID, 3200, pcm16(16, 1)); out != OutcomeLiveEnded || end != 3200 {
		t.Fatalf("a chunk after the end: %v %d", out, end)
	}
	r, _ := s.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 1600, time.Second)
	if !r.Ended || r.Stopped || r.Total != 3200 || len(r.PCM) != 2*1600 {
		t.Fatalf("a read after the end: %+v", r)
	}

	// Clamped low, and a voice that ends mid-way serves nothing past it.
	s2, _ := audioParty(t)
	_, ann2, _ := s2.StartLive("p1", "tok-remote", "Den Remote")
	captureAgo(s2, "p1", 3000)
	tail := pcm16(3200, 2)
	s2.AppendLive("p1", "tok-remote", ann2.ID, 0, tail)
	mid := int64(1601)
	_, v, _ = s2.EndLive("p1", "tok-remote", ann2.ID, &mid)
	if *v.EndSample != 1601 || v.VoiceEndsAtMs != v.VoiceStartsAtMs+101 {
		t.Fatalf("ended mid-way: %+v (ceil(1601/16) = 101)", v)
	}
	if r, _ := s2.ReadLive(context.Background(), "p1", "tok-tv", ann2.ID, 0, 0); r.Total != 1601 || len(r.PCM) != 2*1601 {
		t.Fatalf("a read of a voice ended mid-way: total %d, %d bytes", r.Total, len(r.PCM))
	}
	if !allZero(tail[2*1601:]) {
		t.Fatal("samples past the end were kept")
	}
	s3, _ := audioParty(t)
	_, ann3, _ := s3.StartLive("p1", "tok-remote", "Den Remote")
	negative := int64(-5)
	if _, v, _ := s3.EndLive("p1", "tok-remote", ann3.ID, &negative); *v.EndSample != 0 || v.VoiceEndsAtMs != v.VoiceStartsAtMs {
		t.Fatalf("a negative end: %+v", v)
	}

	// A stop: the seat holder stops another's live; a parked reader wakes
	// to stop.
	s4, _ := audioParty(t)
	_, ann4, _ := s4.StartLive("p1", "tok-tv", "Living Room TV")
	captureAgo(s4, "p1", 3000)
	s4.AppendLive("p1", "tok-tv", ann4.ID, 0, pcm16(1600, 3))
	got := make(chan LiveRead, 1)
	go func() {
		r, _ := s4.ReadLive(context.Background(), "p1", "tok-pc", ann4.ID, 1600, 3*time.Second)
		got <- r
	}()
	waitFor(t, "a parked reader", 2*time.Second, func() bool { return liveWaiting(s4, "p1") == 1 })
	st, out = s4.StopAnnouncement("p1", "tok-phone", "Kate's iPhone", ann4.ID)
	if a := st.Announcement; out != OutcomeOK || a == nil || !a.Ended || a.StoppedAtMs == 0 || *a.EndSample != 1600 {
		t.Fatalf("stop: %v %+v", out, st.Announcement)
	}
	select {
	case r := <-got:
		if !r.Stopped || !r.Ended || len(r.PCM) != 0 {
			t.Fatalf("the reader after a stop: %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("a stop did not wake the reader")
	}
	if _, end, out := s4.AppendLive("p1", "tok-tv", ann4.ID, 1600, pcm16(16, 1)); out != OutcomeLiveEnded || end != 1600 {
		t.Fatalf("a chunk after a stop: %v %d", out, end)
	}
	if liveCount(s4) != 0 {
		t.Fatalf("live sessions after a stop: %d", liveCount(s4))
	}
}

// 25. Sixteen open at once across every profile; ends, stops and drops all
// give their place back.
func TestLiveSessionsAreCappedServerWide(t *testing.T) {
	s := NewStore()
	party := func(p string) {
		s.Report(p, State{ItemID: "a", IsPlaying: true}, ReportOpts{DeviceID: "tok-" + p, BasedOnRevision: rev(0)})
	}
	ids := map[string]string{}
	for i := 0; i < MaxLiveSessions; i++ {
		p := "q" + string(rune('a'+i))
		party(p)
		_, ann, out := s.StartLive(p, "tok-"+p, p)
		if out != OutcomeOK {
			t.Fatalf("live %d: %v", i+1, out)
		}
		ids[p] = ann.ID
	}
	party("qz")
	if _, _, out := s.StartLive("qz", "tok-qz", "qz"); out != OutcomeServerBusy {
		t.Fatalf("the 17th open at once: %v, want server-busy", out)
	}
	s.EndLive("qa", "tok-qa", ids["qa"], nil)
	if _, _, out := s.StartLive("qz", "tok-qz", "qz"); out != OutcomeOK {
		t.Fatalf("after one ended: %v", out)
	}
	s.StopAnnouncement("qb", "tok-qb", "qb", ids["qb"])
	s.EndBy("qc", "tok-qc", "qc", "stop-all")
	s.EndBy("qd", "tok-qd", "qd", "stop-sharing")
	if n := liveCount(s); n != MaxLiveSessions-3 {
		t.Fatalf("after an end, a stop, a stop-all and a delete: %d open, want %d", n, MaxLiveSessions-3)
	}
}

// Clips and live together: one rate, and both go with the party, zeroed.
func TestLiveSharesTheRateAndGoesWithTheParty(t *testing.T) {
	s, _ := audioParty(t)
	for i := 0; i < AnnounceStartsPerMinute/2; i++ {
		s.StartClip("p1", "tok-tv", "TV", wav16k(16000), 1000)
		endedAgo(s, "p1", 1100)
		_, ann, out := s.StartLive("p1", "tok-tv", "TV")
		if out != OutcomeOK {
			t.Fatalf("live %d: %v", i+1, out)
		}
		s.EndLive("p1", "tok-tv", ann.ID, nil)
		endedAgo(s, "p1", 1100)
	}
	if _, _, out := s.StartLive("p1", "tok-tv", "TV"); out != OutcomeRateLimited {
		t.Fatalf("the 7th, clips and live together: %v", out)
	}

	s2, _ := audioParty(t)
	_, ann, _ := s2.StartLive("p1", "tok-remote", "Den Remote")
	captureAgo(s2, "p1", 3000)
	voice := pcm16(1600, 8)
	s2.AppendLive("p1", "tok-remote", ann.ID, 0, voice)
	got := make(chan Outcome, 1)
	go func() {
		_, out := s2.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 1600, 3*time.Second)
		got <- out
	}()
	waitFor(t, "a parked reader", 2*time.Second, func() bool { return liveWaiting(s2, "p1") == 1 })
	s2.EndBy("p1", "tok-phone", "Kate's iPhone", "stop-all")
	select {
	case out := <-got:
		if out == OutcomeOK {
			t.Fatal("a reader got audio from an ended party")
		}
	case <-time.After(time.Second):
		t.Fatal("a stop-all did not wake the reader")
	}
	if !allZero(voice) || liveCount(s2) != 0 {
		t.Fatalf("after a stop-all: zeroed %v, %d live", allZero(voice), liveCount(s2))
	}

	// Ended and past its retention: zeroed, gone.
	s3, _ := audioParty(t)
	_, ann3, _ := s3.StartLive("p1", "tok-remote", "Den Remote")
	captureAgo(s3, "p1", 3000)
	voice3 := pcm16(1600, 9)
	s3.AppendLive("p1", "tok-remote", ann3.ID, 0, voice3)
	s3.EndLive("p1", "tok-remote", ann3.ID, nil)
	endedAgo(s3, "p1", RetainAfterEndMs+1)
	if st, _ := s3.Get("p1"); st.Announcement != nil || !allZero(voice3) {
		t.Fatalf("past its retention: listed %v, zeroed %v", st.Announcement != nil, allZero(voice3))
	}
	if _, out := s3.ReadLive(context.Background(), "p1", "tok-tv", ann3.ID, 0, 0); out != OutcomeNotFound {
		t.Fatalf("a read past its retention: %v", out)
	}
}

// The frame on the wire, against the contract's golden header.
func TestEncodeFrame(t *testing.T) {
	golden, _ := hex.DecodeString("50414c31" + "4006000000000000" + "800c000000000000" + "00000000")
	if got := EncodeFrame(LiveRead{From: 1600, Total: 3200}); !bytes.Equal(got, golden) {
		t.Fatalf("header % x\nwant   % x", got, golden)
	}
	got := EncodeFrame(LiveRead{From: 5, Total: 9, Ended: true, Stopped: true, Skipped: true, PCM: []byte{1, 2, 3, 4}})
	if binary.LittleEndian.Uint32(got[20:]) != 7 || !bytes.Equal(got[24:], []byte{1, 2, 3, 4}) || len(got) != 28 {
		t.Fatalf("flags and samples: % x", got)
	}
}
