//go:build darwin || linux

package command

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// runOnTerminal starts cmd, a program on the terminal in orchestra's process group, and waits for
// it to exit. A program in raw mode, Claude Code for one, reads Ctrl+Z as a key and stops itself
// alone (SIGTSTP to its own process), where the terminal would have stopped the whole process
// group: the shell that started orchestra waits on orchestra, which waits on the stopped program,
// and the terminal hangs. So whenever cmd stops, orchestra stops its process group as the terminal
// does (suspend), and the shell takes the terminal back. Continued, by fg say, orchestra continues
// cmd too if the shell hasn't: fg continues the whole group, kill -CONT orchestra alone.
func runOnTerminal(cmd *exec.Cmd) error {
	sigchld := make(chan os.Signal, 1)
	signal.Notify(sigchld, syscall.SIGCHLD)
	defer signal.Stop(sigchld)
	if err := cmd.Start(); err != nil {
		return err
	}
	exited, followed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(followed)
		followStops(cmd.Process, sigchld, exited)
	}()
	// Best effort: where orchestra can't wait for cmd to exit without reaping it, cmd's stops go
	// unfollowed, as they did before.
	_ = awaitExit(cmd.Process.Pid)
	close(exited)
	<-followed // cmd stays unreaped until followStops is done, so it can't signal another process
	return cmd.Wait()
}

// followStops suspends orchestra each time p stops, as SIGCHLD on sigchld tells, until exited is
// closed. Once orchestra is continued, it continues p if p is still stopped.
func followStops(p *os.Process, sigchld <-chan os.Signal, exited <-chan struct{}) {
	for {
		select {
		case <-exited:
			return
		case <-sigchld: // p, or another child of orchestra's, has stopped, continued or exited
		}
		if !stopped(p.Pid) {
			continue
		}
		suspend(exited)
		if stopped(p.Pid) {
			_ = p.Signal(syscall.SIGCONT) // best effort: p may be on its way out, and nothing else can be done
		}
	}
}
