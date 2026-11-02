package nowplaying

// Live voice keeps what it heard, not the requests it came in (security
// review, 2026-11-02: a talker resending 64 KiB to add one sample at a time
// made the server hold 125 MiB for 69 KB of voice).

import (
	"context"
	"testing"
)

// liveHeld is the bytes the party's live relay holds, and its run count.
func liveHeld(s *Store, profileID string) (held, runs int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lb := s.audio[profileID].ann.live
	for _, r := range lb.runs {
		held += cap(r.pcm)
	}
	return held, len(lb.runs)
}

func TestLiveHoldsWhatItHeardNotWhatWasSent(t *testing.T) {
	s, _ := audioParty(t)
	_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
	captureAgo(s, "p1", 2000)

	// 2,000 requests of 64 KiB, each adding ONE new sample.
	const chunk = 32 * 1024 // samples
	var total int64
	for i := 0; i < 2000; i++ {
		body := pcm16(chunk, i%7)
		got, _, out := s.AppendLive("p1", "tok-remote", ann.ID, total+1-chunk, body)
		if out != OutcomeOK || got != total+1 {
			t.Fatalf("append %d: %v total %d", i, out, got)
		}
		total = got
		if !allZero(body) {
			t.Fatalf("append %d: the request's copy was not zeroed", i)
		}
	}
	held, runs := liveHeld(s, "p1")
	if held > 64<<10 {
		t.Fatalf("holds %d bytes in %d runs for %d samples heard", held, runs, total)
	}

	// What it serves is still what was heard.
	r, out := s.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 0, 0)
	if out != OutcomeOK || r.Total != total || len(r.PCM) != 2*int(total) {
		t.Fatalf("read back: %v total %d, %d bytes", out, r.Total, len(r.PCM))
	}
}

func TestShortGapsJoinARunLongOnesStartOne(t *testing.T) {
	s, _ := audioParty(t)
	_, ann, _ := s.StartLive("p1", "tok-remote", "Den Remote")
	captureAgo(s, "p1", 2000)
	// 100 ms chunks with a 50 ms gap between each: one run, gaps silent.
	var from int64
	for i := 0; i < 5; i++ {
		if _, _, out := s.AppendLive("p1", "tok-remote", ann.ID, from, pcm16(1600, i+1)); out != OutcomeOK {
			t.Fatalf("append %d: %v", i, out)
		}
		from += 1600 + 800
	}
	if _, runs := liveHeld(s, "p1"); runs != 1 {
		t.Fatalf("%d runs, want 1", runs)
	}
	r, _ := s.ReadLive(context.Background(), "p1", "tok-tv", ann.ID, 0, 0)
	for i := 0; i < 4; i++ {
		gap := r.PCM[2*(i*2400+1600) : 2*(i*2400+2400)]
		if !allZero(gap) {
			t.Fatalf("gap %d is not silence", i)
		}
	}
	// A 200 ms gap starts a run of its own.
	if _, _, out := s.AppendLive("p1", "tok-remote", ann.ID, from+3200, pcm16(1600, 9)); out != OutcomeOK {
		t.Fatal(out)
	}
	if _, runs := liveHeld(s, "p1"); runs != 2 {
		t.Fatalf("%d runs, want 2", runs)
	}
}
