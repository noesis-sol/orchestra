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
	exited bool          // the command has exited and is about to be reaped: signal nothing more
	kill   *time.Timer   // the SIGKILL that follows a cancel's SIGTERM
	killed chan struct{} // closed once that SIGKILL has been sent
}

// inGroup starts cmd in its own process group and makes cancelling it stop the whole group:
// SIGTERM now, SIGKILL after grace.
func inGroup(cmd *exec.Cmd, grace time.Duration) *group {
	g := &group{cmd: cmd, killed: make(chan struct{})}
	ownGroup(cmd)
	cmd.Cancel = func() error {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.exited {
			g.kill = time.AfterFunc(grace, func() {
				// Best effort: the group may have exited since, and nothing more can be done if not.
				_ = g.signal(syscall.SIGKILL)
				close(g.killed)
			})
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

// settle waits for cmd to exit, leaving it unreaped, for the caller to reap with cmd.Wait and then
// call release. What a command that exits by itself leaves running in its group keeps running: bd
// may start a server on purpose. What a cancelled one leaves doesn't: settle keeps cmd unreaped
// until the SIGKILL that follows the cancel's SIGTERM by grace has gone to the group, so whatever
// cmd started, such as a git hook, has the same grace as cmd and is then killed with it, even when
// cmd itself ended on the SIGTERM at once.
func (g *group) settle() {
	if awaitExit(g.cmd.Process.Pid) != nil {
		return // release stops the signals once cmd is reaped
	}
	g.mu.Lock()
	cancelled := g.kill != nil
	g.exited = !cancelled // a cancel from now on signals nothing
	g.mu.Unlock()
	if cancelled {
		<-g.killed
	}
}

// release stops signalling the group, once cmd is reaped, without killing anything. Where settle
// can't wait for cmd's exit, a SIGKILL due between the reaping and release could reach a group that
// has reused the ID, though only after a full PID wrap, as in stop.
func (g *group) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.exited = true
	if g.kill != nil {
		g.kill.Stop()
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

// ownGroup starts cmd in a process group of its own, out of reach of the signals the terminal
// sends to orchestra's.
func ownGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// terminate asks a process to stop: SIGTERM, on which a program in raw mode puts the terminal back.
// Interactive's WaitDelay kills it if it doesn't.
func terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
