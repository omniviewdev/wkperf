//go:build !unix

package workload

import (
	"os/exec"
	"time"
)

// Own retains CommandContext's process cancellation on non-Unix platforms.
// Builds for these platforms can clean up only the direct child.
func Own(cmd *exec.Cmd) (cleanup func()) {
	return func() { _ = cmd.Process.Kill() }
}

// OwnGracefully falls back to direct-process cancellation on non-Unix platforms.
func OwnGracefully(cmd *exec.Cmd, _ time.Duration) (cleanup func()) {
	return Own(cmd)
}
