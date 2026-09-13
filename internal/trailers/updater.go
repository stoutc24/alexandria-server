package trailers

import (
	"context"
	"time"
)

// RunUpdater keeps yt-dlp current: `yt-dlp -U` once after initialDelay,
// then every interval, until ctx ends. It blocks, so run it in a
// goroutine.
//
// Why bother: yt-dlp is the piece of this that YouTube breaks, and the
// fix is always "update yt-dlp". The standalone binary the Docker image
// ships replaces itself in place (which is why it lives under /app,
// owned by the server's user). A pip-installed yt-dlp prints "use pip"
// and exits non-zero instead, which is logged and otherwise harmless.
// Nothing here can take the server down: a failed update is a log line,
// and a panic is recovered.
func (s *Service) RunUpdater(ctx context.Context, initialDelay, interval time.Duration) {
	if s.ytdlp == "" {
		s.logf("trailers: yt-dlp not installed; skipping the daily self-update")
		return
	}
	if interval <= 0 {
		interval = 24 * time.Hour
	}

	first := time.NewTimer(initialDelay)
	defer first.Stop()
	select {
	case <-first.C:
	case <-ctx.Done():
		return
	}
	s.updateOnce(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.updateOnce(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// updateOnce runs a single `yt-dlp -U` while holding the tool lock
// exclusively, so the binary is never rewritten under a download that
// is about to start — and never starts rewriting while one runs.
func (s *Service) updateOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("trailers: yt-dlp update panicked: %v", r)
		}
	}()

	s.toolLock.Lock()
	defer s.toolLock.Unlock()

	uctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()

	stdout, stderr, err := s.runner.Run(uctx, s.ytdlp, "-U")
	if err != nil {
		s.logf("trailers: yt-dlp -U failed: %v — %s", err, lastLine(stderr, stdout))
		return
	}
	s.logf("trailers: yt-dlp -U: %s", lastLine(stdout, stderr))
}
