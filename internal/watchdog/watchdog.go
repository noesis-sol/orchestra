// Package watchdog is for tests that start a process outliving them unless stopped, such as a tmux
// server, which runs as a daemon, or the cmux app: a watchdog stops it once the test process has
// exited, however it exits. A test killed, or past its timeout, exits without running its cleanups,
// and a process its cleanup was to stop would otherwise keep running for good.
package watchdog

import (
	"os"
	"os/exec"
	"syscall"
)

// Watchdog is a process that runs a script once the test process has exited, unless stopped first.
type Watchdog struct {
	cmd *exec.Cmd
	w   *os.File // the write end of its input, which only this process holds
}

// Start starts a watchdog that runs script with /bin/sh once this process has exited, however it
// exits, unless Stop stops it first. It learns of the exit from its input, a pipe that only this
// process writes to, and that the system closes as the process exits: no polling, and no process ID
// to be reused. It runs in a session of its own, so that a signal to the test's process group, a
// Ctrl+C say, doesn't end it with the test. What script prints is discarded.
func Start(script string) (*Watchdog, error) {
	r, w, err := os.Pipe() // both ends close on exec: only the watchdog gets r, as its input
	if err != nil {
		return nil, err
	}
	// The watchdog is to outlive the test, so it isn't started through internal/command, which runs a
	// command to its end: Stop ends it.
	cmd := exec.Command("/bin/sh", "-c", "cat >/dev/null\n"+script)
	cmd.Stdin = r
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	err = cmd.Start()
	_ = r.Close() // the watchdog has its own copy; a close of a pipe's end doesn't fail
	if err != nil {
		_ = w.Close() // nor this
		return nil, err
	}
	return &Watchdog{cmd: cmd, w: w}, nil
}

// Stop ends the watchdog without its script running, and waits for it to exit.
func (d *Watchdog) Stop() {
	// The watchdog leads its session's process group, which holds it and the cat it waits on.
	_ = syscall.Kill(-d.cmd.Process.Pid, syscall.SIGKILL) // one that has exited already needs none
	_ = d.cmd.Wait()                                      // killed, it exits with an error, as expected
	_ = d.w.Close()
}
