//go:build !windows

package trailers

import (
	"os/exec"
	"syscall"
)

// killProcessGroup puts the child in its own process group and, when the
// context ends, signals the group rather than the one process — so a
// timed-out yt-dlp takes its ffmpeg and deno children with it instead of
// leaving them to finish a download nobody is waiting for.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// A negative pid addresses the whole group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
