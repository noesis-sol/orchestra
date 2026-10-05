//go:build unix

package command

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitForFile waits until the file exists, as a command writes it once it has started.
func waitForFile(t *testing.T, p string) {
	t.Helper()
	for deadline := time.Now().Add(hung); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(p); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", p)
		}
	}
}

// An interactive command runs in dir, reads stdin and writes stdout and stderr as it goes, and
// stays in orchestra's process group, where the terminal lets it read.
func TestInteractiveRunsInOrchestrasGroup(t *testing.T) {
	dir := t.TempDir()
	in, keys := io.Pipe()
	var out, errOut strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Interactive(context.Background(), dir, in, &out, &errOut, "sh", "-c",
			`echo $$ > pid; read line; echo "got $line"; echo "in $(pwd)" >&2`)
	}()
	waitForFile(t, filepath.Join(dir, "pid"))
	pgid, err := syscall.Getpgid(childPID(t, filepath.Join(dir, "pid")))
	if err != nil {
		t.Fatal(err)
	}
	if pgid != syscall.Getpgrp() {
		t.Errorf("the command runs in process group %d, orchestra in %d", pgid, syscall.Getpgrp())
	}
	if _, err := keys.Write([]byte("an answer\n")); err != nil {
		t.Fatal(err)
	}
	_ = keys.Close() // only ends the input
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if out.String() != "got an answer\n" || errOut.String() != "in "+dir+"\n" {
		t.Errorf("stdout %q, stderr %q", out.String(), errOut.String())
	}
}

// Stopped, an interactive command gets SIGTERM, to put the terminal back before it goes, and fails
// with an *Error saying it was stopped; one that fails by itself is an *Error with its exit status.
func TestInteractiveStopsWithSIGTERM(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- Interactive(ctx, dir, nil, &out, io.Discard, "sh", "-c",
			`trap 'echo terminal restored; exit 3' TERM; echo $$ > pid; while :; do sleep 0.05; done`)
	}()
	waitForFile(t, filepath.Join(dir, "pid"))
	cancel()
	var cmdErr *Error
	select {
	case err := <-done:
		if !errors.As(err, &cmdErr) || !cmdErr.Stopped || !errors.Is(err, context.Canceled) {
			t.Errorf("error %v, want a stopped *Error", err)
		}
	case <-time.After(hung):
		t.Fatal("the command wasn't stopped")
	}
	if out.String() != "terminal restored\n" {
		t.Errorf("stdout %q, want the command's own way out", out.String())
	}

	err := Interactive(context.Background(), dir, nil, io.Discard, io.Discard, "sh", "-c", "exit 3")
	var exit *exec.ExitError
	if !errors.As(err, &cmdErr) || cmdErr.Stopped || !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("error %v, want exit status 3", err)
	}
}
