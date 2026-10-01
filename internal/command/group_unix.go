//go:build unix

package command

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// group signals a command's process group, whose ID is the command's PID. It signals only while
// the command is unreaped: until then no other process can take that PID, so no other group can
// take the ID either, and a signal can't reach a stranger.
type group struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	exited bool        // the command has exited and is about to be reaped: signal nothing more
	kill   *time.Timer // the SIGKILL that follows a cancel's SIGTERM
}

// inGroup starts cmd in its own process group and makes cancelling it stop the whole group:
// SIGTERM now, SIGKILL after grace.
func inGroup(cmd *exec.Cmd, grace time.Duration) *group {
	g := &group{cmd: cmd}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.exited {
			// Best effort: the group may have exited since, and nothing more can be done if not.
			g.kill = time.AfterFunc(grace, func() { _ = g.signal(syscall.SIGKILL) })
		}
		return g.signalLocked(syscall.SIGTERM)
	}
	return g
}

func (g *group) signal(sig syscall.Signal) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.signalLocked(sig)
}

func (g *group) signalLocked(sig syscall.Signal) error {
	if g.exited {
		return os.ErrProcessDone
	}
	err := syscall.Kill(-g.cmd.Process.Pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// wait waits for cmd to exit and then, before it is reaped, stops the group. The caller then
// reaps cmd with cmd.Wait and calls stop.
func (g *group) wait() {
	if awaitExit(g.cmd.Process.Pid) == nil {
		g.stop()
	}
}

// stop kills whatever cmd left running in its group and stops signalling the group. Once cmd is
// reaped this is safe only if wait already did it: then it does nothing. Where wait can't, the
// kill here could reach a group that has reused the ID, though only after a full PID wrap.
func (g *group) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	_ = g.signalLocked(syscall.SIGKILL) // best effort: usually nothing is left in the group
	g.exited = true
	if g.kill != nil {
		g.kill.Stop()
	}
}

// terminate asks a process to stop, as Ctrl+C or a time limit does: SIGTERM, on which git removes
// its lock files. Output's WaitDelay kills it if it doesn't.
func terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
