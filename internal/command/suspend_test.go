//go:build darwin || linux

package command

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The variables that make a test of this file orchestra (playOrchestra), started by the test
// itself, which plays the shell.
const (
	orchestraDir     = "ORCHESTRA_TEST_SUSPEND_DIR"    // the command's folder
	orchestraStopsBy = "ORCHESTRA_TEST_SUSPEND_SIGNAL" // the signal the command stops itself with
	orchestraIgnores = "ORCHESTRA_TEST_SUSPEND_IGNORE" // set: orchestra ignores SIGTSTP
)

// playOrchestra runs a command on the terminal, as the interview runs claude, if the test was
// started as orchestra: one that stops itself with the signal orchestraStopsBy names, as Claude
// Code stops itself on Ctrl+Z, and once continued writes "resumed" and exits. It reports whether
// it played orchestra; the test fails if Interactive does.
func playOrchestra(t *testing.T) bool {
	dir := os.Getenv(orchestraDir)
	if dir == "" {
		return false
	}
	if os.Getenv(orchestraIgnores) != "" {
		signal.Ignore(syscall.SIGTSTP)
	}
	err := Interactive(context.Background(), dir, nil, io.Discard, io.Discard, "sh", "-c",
		"echo $$ > pid; kill -"+os.Getenv(orchestraStopsBy)+" $$; echo resumed > resumed")
	if err != nil {
		t.Fatal(err)
	}
	return true
}

// orchestraRun is orchestra as the test started it: its PID, its command's folder, and each state
// wait reports for it, stops until it exits.
type orchestraRun struct {
	pid    int
	dir    string
	out    string // orchestra's stdout and stderr
	states chan syscall.WaitStatus
}

// startOrchestra starts the test binary as orchestra (playOrchestra) running test, in a process
// group of its own as a shell starts a job, with the command stopping itself by signal stopsBy.
// The test plays the shell: it waits for orchestra's stops as well as its exit.
func startOrchestra(t *testing.T, test, stopsBy string, env ...string) *orchestraRun {
	t.Helper()
	r := &orchestraRun{dir: t.TempDir(), out: filepath.Join(t.TempDir(), "out"),
		states: make(chan syscall.WaitStatus, 4)} // more than a test waits for: a stop or two, the exit
	out, err := os.Create(r.out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = out.Close() }() // orchestra has its own copy
	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$")
	cmd.Env = append(append(os.Environ(), orchestraDir+"="+r.dir, orchestraStopsBy+"="+stopsBy), env...)
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	r.pid = cmd.Process.Pid
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(r.states)
		for {
			var ws syscall.WaitStatus
			_, err := syscall.Wait4(r.pid, &ws, syscall.WUNTRACED, nil)
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if err != nil {
				return
			}
			r.states <- ws
			if !ws.Stopped() {
				return
			}
		}
	}()
	t.Cleanup(func() {
		select {
		case <-done:
		default:
			// orchestra hangs: kill its group, which it leads, while it is unreaped.
			_ = syscall.Kill(-r.pid, syscall.SIGKILL)
			<-done
		}
		_ = cmd.Process.Release() // reaped by Wait4, not cmd.Wait
	})
	return r
}

// next is orchestra's next state: a stop, or its exit.
func (r *orchestraRun) next(t *testing.T) syscall.WaitStatus {
	t.Helper()
	select {
	case ws, ok := <-r.states:
		if !ok {
			t.Fatal("orchestra can't be waited for")
		}
		return ws
	case <-time.After(hung):
		t.Fatalf("orchestra neither stopped nor exited\n%s", r.output())
	}
	return 0
}

// exited checks that orchestra exits 0, its command having been continued.
func (r *orchestraRun) exited(t *testing.T) {
	t.Helper()
	if ws := r.next(t); !ws.Exited() || ws.ExitStatus() != 0 {
		t.Fatalf("orchestra ended with %v, want exit 0\n%s", ws, r.output())
	}
	if _, err := os.Stat(filepath.Join(r.dir, "resumed")); err != nil {
		t.Errorf("the command wasn't continued: %v", err)
	}
}

// suspended checks that orchestra stops as Ctrl+Z stops a program, its command stopped with it.
func (r *orchestraRun) suspended(t *testing.T) {
	t.Helper()
	if ws := r.next(t); !ws.Stopped() || ws.StopSignal() != syscall.SIGTSTP {
		t.Fatalf("orchestra reported %v, want it stopped by SIGTSTP\n%s", ws, r.output())
	}
	pid := childPID(t, filepath.Join(r.dir, "pid"))
	stat, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(stat)), "T") {
		t.Errorf("while orchestra is stopped, its command's state is %q (%v), want stopped (T)", stat, err)
	}
}

func (r *orchestraRun) output() string {
	b, _ := os.ReadFile(r.out) // best effort: it only explains a failure
	return string(b)
}

// A command on the terminal that stops itself alone, as Claude Code does on Ctrl+Z, suspends
// orchestra with it, so that the shell gets the terminal back; fg, which continues orchestra's
// process group, brings both back, and orchestra goes on waiting for the command to exit.
func TestCtrlZSuspendsOrchestraWithItsCommand(t *testing.T) {
	if playOrchestra(t) {
		return
	}
	r := startOrchestra(t, t.Name(), "TSTP")
	r.suspended(t)
	if err := syscall.Kill(-r.pid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	r.exited(t)
}

// Continued alone (kill -CONT), orchestra continues its stopped command itself.
func TestContinuedOrchestraContinuesItsCommand(t *testing.T) {
	if playOrchestra(t) {
		return
	}
	r := startOrchestra(t, t.Name(), "TSTP")
	r.suspended(t)
	if err := syscall.Kill(r.pid, syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	r.exited(t)
}

// An orchestra that SIGTSTP doesn't stop, as it ignores it, continues a command that stops at
// once, rather than leave it stopped and the terminal hanging.
func TestOrchestraThatCantStopContinuesItsCommand(t *testing.T) {
	if playOrchestra(t) {
		return
	}
	r := startOrchestra(t, t.Name(), "STOP", orchestraIgnores+"=1")
	r.exited(t)
}
