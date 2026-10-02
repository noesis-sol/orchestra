//go:build unix

package project

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sameHolder compares holders whose start times went through JSON.
func sameHolder(a, b Holder) bool {
	return a.Started.Equal(b.Started) && a.PID == b.PID && a.Version == b.Version && a.Branch == b.Branch &&
		a.Ticket == b.Ticket && a.Feature == b.Feature && a.Pane == b.Pane
}

// lockedBy takes repo's run lock for h, failing the test if it can't, and releases it at the end.
func lockedBy(t *testing.T, repo string, h Holder) *RunLock {
	t.Helper()
	l, err := LockRun(repo, h)
	if err != nil {
		t.Fatalf("LockRun: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

// A second run is refused with the first one's details; once the first lets go, it gets the lock,
// and the file says it holds it.
func TestRunLockRefusesASecondRun(t *testing.T) {
	repo := t.TempDir()
	first := Holder{PID: 44497, Started: time.Now().Truncate(time.Second), Version: "v1.2.3", Branch: "main",
		Ticket: "x-1", Pane: "w2B:p60"}
	l := lockedBy(t, repo, first)

	_, err := LockRun(repo, Holder{PID: 2})
	var held *HeldError
	if !errors.As(err, &held) || held.Repo != repo || !sameHolder(held.Holder, first) {
		t.Fatalf("second LockRun: %v (%+v), want a *HeldError naming %+v", err, held, first)
	}
	want := fmt.Sprintf("orchestra is already running in %s (pid 44497, since %s, main, --ticket x-1, pane w2B:p60)",
		repo, first.Started.Format("15:04"))
	if err.Error() != want {
		t.Errorf("error:\n%s\nwant:\n%s", err, want)
	}
	if h, ok, err := RunHolder(repo); err != nil || !ok || !sameHolder(h, first) {
		t.Errorf("RunHolder while held: %+v %v %v", h, ok, err)
	}

	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if h, ok, err := RunHolder(repo); err != nil || ok {
		t.Errorf("RunHolder once released: %+v %v %v", h, ok, err)
	}
	second := Holder{PID: 3, Started: time.Now().Truncate(time.Second), Feature: "Add a --json flag"}
	lockedBy(t, repo, second)
	if h, ok, err := RunHolder(repo); err != nil || !ok || !sameHolder(h, second) {
		t.Errorf("RunHolder for the second run: %+v %v %v", h, ok, err)
	}
	b, err := os.ReadFile(filepath.Join(repo, RunPath(LockName)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "44497") || !strings.Contains(string(b), `"feature": "Add a --json flag"`) {
		t.Errorf("the lock's file after the second run took it:\n%s", b)
	}
}

func TestRunLocksInDifferentRepositoriesDontBlockEachOther(t *testing.T) {
	lockedBy(t, t.TempDir(), Holder{PID: 1})
	lockedBy(t, t.TempDir(), Holder{PID: 2})
}

// Looking makes nothing in a repository no run has locked.
func TestRunHolderMakesNothing(t *testing.T) {
	repo := t.TempDir()
	if h, ok, err := RunHolder(repo); err != nil || ok {
		t.Errorf("RunHolder: %+v %v %v", h, ok, err)
	}
	if _, err := os.Stat(filepath.Join(repo, Dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s: %v, want none", Dir, err)
	}
}

// The lock is taken through OpenRun: a symlink in place of .orchestra/run is refused.
func TestRunLockRefusesASymlinkedRunFolder(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	plant(t, repo, filepath.Join(Dir, RunName), outside)
	_, err := LockRun(repo, Holder{PID: 1})
	var e *EscapeError
	if !errors.As(err, &e) {
		t.Fatalf("LockRun: %v, want an *EscapeError", err)
	}
	if got := listing(t, outside); len(got) != 0 {
		t.Errorf("written outside the repository: %v", got)
	}
}

// holderRepo is the variable that makes TestRunLockGoesWithAKilledRun the holder process.
const holderRepo = "ORCHESTRA_TEST_LOCK_REPO"

// A run killed with SIGKILL leaves the lock free: the next run takes it at once, with nothing to
// clean up.
func TestRunLockGoesWithAKilledRun(t *testing.T) {
	if repo := os.Getenv(holderRepo); repo != "" {
		// The holder: it takes the lock, says so, and waits to be killed.
		l, err := LockRun(repo, Holder{PID: os.Getpid()})
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		defer func() { _ = l.Close() }() // keeps l reachable: an unreachable file is closed
		fmt.Println("locked")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n') // until killed, or the test ends
		return
	}

	repo := t.TempDir()
	holder := exec.Command(os.Args[0], "-test.run=^TestRunLockGoesWithAKilledRun$")
	holder.Env = append(os.Environ(), holderRepo+"="+repo)
	stdin, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }() // ends the holder if the test fails before killing it
	stdout, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "locked\n" {
		_ = holder.Process.Kill()
		_ = holder.Wait()
		t.Fatalf("the holder said %q (%v)", line, err)
	}

	_, err = LockRun(repo, Holder{PID: 1})
	var held *HeldError
	if !errors.As(err, &held) || held.Holder.PID != holder.Process.Pid {
		t.Errorf("while the holder runs: %v, want a *HeldError naming pid %d", err, holder.Process.Pid)
	}
	if err := holder.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = holder.Wait() // killed: the error says so
	lockedBy(t, repo, Holder{PID: 1})
}
