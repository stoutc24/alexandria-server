//go:build windows

package trailers

import "os/exec"

// killProcessGroup is a no-op on Windows: there are no process groups to
// signal, so a timed-out yt-dlp is killed on its own by exec.CommandContext
// and WaitDelay releases the pipes its children may still hold.
func killProcessGroup(*exec.Cmd) {}
