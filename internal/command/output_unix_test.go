//go:build unix

package command

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// hang is a command that never finishes by itself. The shell runs sleep as its child rather than
// in its place, so sleep goes on holding the output after the shell is stopped, as a git hook can.
var hang = []string{"-c", "sleep 5; echo late"}

// Ctrl+C stops a hung command within a second, and the error says why it stopped.
func TestOutputStopsAHungCommandWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(errors.New("stopped with Ctrl+C")) })
	start := time.Now()
	_, err := Output(ctx, ReadLimit, "", "sh", hang...)
	if took := time.Since(start); took > time.Second {
		t.Errorf("returned %s after the cancel", took-100*time.Millisecond)
	}
	if err == nil || err.Error() != "sh -c sleep 5; echo late: stopped with Ctrl+C" {
		t.Errorf("error %v, want it to name the command and why it stopped", err)
	}
}

// A hung command is stopped at its time limit, and the error names the limit.
func TestOutputStopsAHungCommandAtItsLimit(t *testing.T) {
	start := time.Now()
	_, err := Output(context.Background(), 200*time.Millisecond, "", "sh", hang...)
	if took := time.Since(start); took > time.Second {
		t.Errorf("returned after %s", took)
	}
	if err == nil || err.Error() != "sh -c sleep 5; echo late: timed out after 200ms" {
		t.Errorf("error %v, want it to name the command and the limit", err)
	}
}

// A stopped command's stderr stays in the error.
func TestOutputStoppedKeepsStderr(t *testing.T) {
	_, err := Output(context.Background(), 200*time.Millisecond, "", "sh", "-c", "echo waiting on the lock >&2; sleep 5; echo late")
	if err == nil || !strings.HasSuffix(err.Error(), ": timed out after 200ms (waiting on the lock)") {
		t.Errorf("error %v, want the limit and stderr", err)
	}
}

// A command that succeeds but leaves a process behind holding its output succeeds, without waiting
// for that process.
func TestOutputDoesNotWaitForALeftoverHoldingTheOutput(t *testing.T) {
	start := time.Now()
	out, err := Output(context.Background(), ReadLimit, "", "sh", "-c", "sleep 5 & echo ok")
	if err != nil || out != "ok\n" {
		t.Errorf("got %q, %v", out, err)
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("returned after %s", took)
	}
}

// A command's own failure is reported as before: its error and stderr, and no limit.
func TestOutputReportsAFailure(t *testing.T) {
	_, err := Output(context.Background(), ReadLimit, "", "sh", "-c", "echo locked >&2; exit 3")
	if err == nil || err.Error() != "sh -c echo locked >&2; exit 3: exit status 3: locked" {
		t.Errorf("error %v", err)
	}
}
