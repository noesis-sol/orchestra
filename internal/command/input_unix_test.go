//go:build unix

package command

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A command run with input reads it on its stdin.
func TestOutputWithInputFeedsTheCommand(t *testing.T) {
	out, err := OutputWithInput(context.Background(), ReadLimit, "", nil, "the evidence\n", "sh", "-c", "echo got; cat")
	if err != nil || out != "got\nthe evidence\n" {
		t.Errorf("got %q, %v", out, err)
	}
}

// A command run with input that succeeds but leaves a process behind holding its output succeeds, as
// with Output.
func TestOutputWithInputDoesNotWaitForALeftoverHoldingTheOutput(t *testing.T) {
	start := time.Now()
	out, err := OutputWithInput(context.Background(), ReadLimit, "", nil, "in", "sh", "-c", "sleep 30 & cat")
	if err != nil || out != "in" {
		t.Errorf("got %q, %v", out, err)
	}
	if took := time.Since(start); took > soon {
		t.Errorf("returned after %s", took)
	}
}

// A command stopped at its time limit fails with an error that names the limit and that errors.Is
// takes for a deadline, while a cancelled one doesn't.
func TestATimedOutCommandIsADeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: waits out two stops and their grace")
	}
	_, err := OutputWithInput(context.Background(), 200*time.Millisecond, "", nil, "in", "sh", "-c", "exec sleep 5")
	if err == nil || err.Error() != "sh -c exec sleep 5: timed out after 200ms" {
		t.Errorf("error %v, want it to name the limit", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := Output(ctx, ReadLimit, "", "sh", "-c", "exec sleep 5"); errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a cancelled command reads as a deadline: %v", err)
	}
}

// An error without arguments names the command alone.
func TestErrorWithoutArgsNamesTheCommand(t *testing.T) {
	err := &Error{Name: "claude", Err: errors.New("exit status 1"), Stderr: "not logged in"}
	if got := err.Error(); got != "claude: exit status 1: not logged in" {
		t.Errorf("got %q", got)
	}
	err = &Error{Name: "claude", Err: timedOut(10 * time.Minute), Stopped: true}
	if got := err.Error(); got != "claude: timed out after 10m" {
		t.Errorf("got %q", got)
	}
}
