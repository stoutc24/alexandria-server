package trailers

import (
	"bytes"
	"context"
	"os/exec"
	"time"
)

// Runner executes one external program and returns what it wrote.
//
// The service talks to yt-dlp through this and nothing else, so the tests
// can stand in a fake that writes a file where yt-dlp would have and never
// touches the network — and so a single-flight or cache bug shows up as a
// wrong call count, not as a flaky download.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)
}

// execRunner is the real one: os/exec, output captured, killed when the
// context ends.
type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	// yt-dlp forks ffmpeg and deno. Killing only yt-dlp on a timeout would
	// leave those children holding our stdout/stderr pipes, and Wait would
	// then block until they exited on their own. Two defences: the whole
	// process group is signalled (unix — see runner_unix.go), and WaitDelay
	// closes the pipes after a grace period regardless of who still holds
	// them.
	killProcessGroup(cmd)
	cmd.WaitDelay = 5 * time.Second

	err := cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}
