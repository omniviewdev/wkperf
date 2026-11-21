//go:build unix

package workload

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Own configures a CommandContext to run in its own process group. After a
// successful Start, call the returned cleanup function and call Cmd.Wait.
// Cleanup is safe to call concurrently with cancellation and Wait.
func Own(cmd *exec.Cmd) (cleanup func()) {
	return OwnGracefully(cmd, 0)
}

// OwnGracefully gives the group time to handle SIGTERM before sending SIGKILL.
// Use this when a workload may need to clean up its own process groups.
func OwnGracefully(cmd *exec.Cmd, grace time.Duration) (cleanup func()) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0
	kill := sync.OnceValue(func() error {
		group := -cmd.Process.Pid
		if grace > 0 {
			err := syscall.Kill(group, syscall.SIGTERM)
			if err == syscall.ESRCH {
				return os.ErrProcessDone
			}
			if err == nil && waitForGroupExit(group, grace) {
				return nil
			}
		}
		err := syscall.Kill(group, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	})
	cmd.Cancel = kill
	return func() { _ = kill() }
}

func waitForGroupExit(group int, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-timer.C:
			return false
		case <-poll.C:
			if syscall.Kill(group, 0) == syscall.ESRCH {
				return true
			}
		}
	}
}
