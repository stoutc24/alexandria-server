package trailers

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

// The updater runs `yt-dlp -U` after the initial delay and then on every
// tick, and stops when the context ends.
func TestUpdaterRunsOnScheduleAndStops(t *testing.T) {
	r := &fakeRunner{stdout: "yt-dlp is up to date (2026.08.19)"}
	s := newTestService(t, r, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.RunUpdater(ctx, 5*time.Millisecond, 10*time.Millisecond)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for r.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("updater did not stop when the context ended")
	}
	if r.count() < 3 {
		t.Fatalf("yt-dlp -U ran %d times, want at least 3", r.count())
	}
	for _, call := range r.calls {
		if len(call) != 2 || call[0] != "/fake/bin/yt-dlp" || call[1] != "-U" {
			t.Fatalf("unexpected call %v", call)
		}
	}
}

// A failed update is a log line, never a crash — and the next tick tries
// again.
func TestUpdaterSurvivesFailure(t *testing.T) {
	r := &fakeRunner{err: errors.New("exit status 100"), stderr: "ERROR: Unable to write to /app/bin/yt-dlp; Try running as administrator"}
	s := newTestService(t, r, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	s.RunUpdater(ctx, time.Millisecond, 10*time.Millisecond)
	if r.count() < 2 {
		t.Fatalf("updater gave up after a failure (%d runs)", r.count())
	}
}

func TestUpdaterSkipsWhenYtDlpIsMissing(t *testing.T) {
	r := &fakeRunner{}
	s := newTestService(t, r, func(o *Options) {
		o.LookPath = func(file string) (string, error) {
			if file == "yt-dlp" {
				return "", exec.ErrNotFound
			}
			return "/fake/bin/" + file, nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	s.RunUpdater(ctx, 0, time.Millisecond)
	if r.count() != 0 {
		t.Fatalf("ran -U %d times without a yt-dlp", r.count())
	}
}
