//go:build unix

package command

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// inGroup starts cmd in its own process group and makes cancelling it stop the whole group:
// SIGTERM now, SIGKILL after grace.
func inGroup(cmd *exec.Cmd, grace time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		time.AfterFunc(grace, func() { syscall.Kill(-pgid, syscall.SIGKILL) })
		err := syscall.Kill(-pgid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

// killGroup kills whatever is left of cmd's process group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
