//go:build unix

package main

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// windDownAfterPipe is a run's stop as orchestra --plain | tee meets it: it catches the stop
// signals, says it is ready on stdout, and once Ctrl+C has stopped it (and ended tee, the reader
// of its stdout) it shows the lines of its winding down, then does its bookkeeping: it writes the
// file its one argument names, as leaveBehind saves the workers it leaves, and exits 130.
func windDownAfterPipe(args []string) int {
	stops := catchStops()
	defer stops.release()
	stopped := make(chan struct{})
	stops.on(func(os.Signal) { close(stopped) })
	out := tui.Printer{Out: os.Stdout}
	out.Event(dispatch.Event{Kind: dispatch.EvInfo, Text: "ready", Time: time.Now()})
	<-stopped
	for _, line := range []string{"left running: orchestra-1, labelled", "INTERRUPTED with Ctrl+C"} {
		out.Event(dispatch.Event{Kind: dispatch.EvInfo, Text: line, Time: time.Now()})
	}
	if err := os.WriteFile(args[0], []byte("left\n"), 0o644); err != nil {
		return 1
	}
	return dispatch.ExitInterrupted
}

var _ = faketool.Func("wind-down-after-pipe", windDownAfterPipe)

// Ctrl+C on orchestra --plain | tee ends tee as well, so the lines orchestra shows as it winds down
// go to a pipe nobody reads. They are lost, but orchestra isn't: SIGPIPE doesn't end it before its
// bookkeeping, and it exits 130 as a stopped run does.
func TestABrokenPipeDoesNotEndTheWindingDown(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := faketool.WriteFunc(t, dir, "wind-down-after-pipe")
	left := filepath.Join(dir, "state.json")
	cmd := exec.Command(bin, left)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("no ready line: %v", err)
	}
	if err := stdout.Close(); err != nil { // tee has gone
		t.Fatal(err)
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err = <-waited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("still running: %v", <-waited)
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.Sys().(syscall.WaitStatus).Signaled():
		t.Fatalf("ended by %v as it wound down", exit.Sys().(syscall.WaitStatus).Signal())
	case errors.As(err, &exit) && exit.ExitCode() != dispatch.ExitInterrupted:
		t.Errorf("exit %d, want %d", exit.ExitCode(), dispatch.ExitInterrupted)
	case err == nil:
		t.Errorf("exit 0, want %d", dispatch.ExitInterrupted)
	case !errors.As(err, &exit):
		t.Fatal(err)
	}
	if _, err := os.Stat(left); err != nil {
		t.Errorf("the bookkeeping after the lines didn't happen: %v", err)
	}
}
