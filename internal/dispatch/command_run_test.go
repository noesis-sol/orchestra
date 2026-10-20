package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A command run says how it failed: it fails, or did not finish within its time limit.
func TestCommandRunHow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("exit status 1"), "fails"},
		{fmt.Errorf("%w: stopped after 5m (signal: killed)", errCheckTimedOut), "did not finish within 5m"},
		{context.Canceled, "fails"},
	}
	for _, tc := range cases {
		r := commandRun{err: tc.err, limit: 5 * time.Minute}
		if got := r.how(); got != tc.want {
			t.Errorf("how() of %v = %q, want %q", tc.err, got, tc.want)
		}
		if got, want := r.timedOut(), errors.Is(tc.err, errCheckTimedOut); got != want {
			t.Errorf("timedOut() of %v = %v, want %v", tc.err, got, want)
		}
	}
}

// What a failed command said is the lines of its output that say what failed, or else its last lines,
// or nothing when it printed nothing.
func TestCommandRunSaid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, output string
		want         []string
		end          bool
	}{
		{"failures", "ok  \texample.com/m/a\t0.1s\nFAIL\texample.com/m/b\t0.2s\n", []string{"FAIL\texample.com/m/b\t0.2s"},
			false},
		{"last lines", "building\nsomething went wrong\n", []string{"building", "something went wrong"}, true},
		{"nothing", "", nil, true},
		{"blank", "\n\n", nil, true},
	}
	for _, tc := range cases {
		out := &checkOutput{}
		_, _ = out.Write([]byte(tc.output)) // it never fails
		out.end()
		lines, end := commandRun{out: out}.said()
		if !slices.Equal(lines, tc.want) || end != tc.end {
			t.Errorf("%s: said() = %q, %v, want %q, %v", tc.name, lines, end, tc.want, tc.end)
		}
	}
}

// runLimited runs its commands one after another until one fails, and says which, within one time
// limit for them all. Only the case that times out has a short limit: the others' is one that starting
// their shells under load (several workers' race checks at once) can't reach.
func TestRunLimited(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	cases := []struct {
		name     string
		cmds     []string
		limit    time.Duration
		failed   int
		timedOut bool
		last     string // the end of the last output kept
		second   bool   // the second command ran
	}{
		{"both pass", []string{"echo one", "echo two; touch ran"}, time.Minute, -1, false, "two", true},
		{"the first fails", []string{"echo one; exit 1", "touch ran"}, time.Minute, 0, false, "one", false},
		{"the second fails", []string{"echo one", "echo two; touch ran; exit 1"}, time.Minute, 1, false, "two", true},
		{"the first times out", []string{"sleep 5", "touch ran"}, 200 * time.Millisecond, 0, true, "", false},
	}
	for _, tc := range cases {
		_ = os.Remove(ran) // absent at first
		run, failed := runLimited(t.Context(), dir, tc.limit, tc.cmds...)
		if failed != tc.failed || (run.err != nil) != (tc.failed >= 0) || run.timedOut() != tc.timedOut {
			t.Errorf("%s: runLimited = %v, %d, want %d, timed out %v", tc.name, run.err, failed, tc.failed, tc.timedOut)
		}
		if got := run.out.lastLines(1); got != tc.last {
			t.Errorf("%s: its output ends %q, want %q", tc.name, got, tc.last)
		}
		if _, err := os.Stat(ran); (err == nil) != tc.second {
			t.Errorf("%s: the second command ran: %v, want %v", tc.name, err == nil, tc.second)
		}
		run.out.close()
	}
}

// A command stopped by its caller has not timed out: the time limit did not stop it.
func TestRunLimitedCancelledIsNotTimedOut(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	run, failed := runLimited(ctx, t.TempDir(), time.Minute, "sleep 5")
	defer run.out.close()
	if failed != 0 || run.err == nil || run.timedOut() {
		t.Errorf("runLimited cancelled = %v, %d, want an error that is not a timeout, at 0", run.err, failed)
	}
}
