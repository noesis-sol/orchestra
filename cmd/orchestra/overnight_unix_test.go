//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// An overnight run (--overnight) runs the current tickets as --tickets does, after its pre-flight:
// its checklist printed, a failed check refusing to start unless --force-overnight overrides it.

// fakeCheck is a pre-flight check that finds r, counting its runs in ran and the fixes it makes in fixed.
type fakeCheck struct {
	r          preflightResult
	ran, fixed atomic.Int32
	fixErr     error
}

func (f *fakeCheck) check(context.Context, *session) preflightResult {
	f.ran.Add(1)
	return f.r
}

// limitFix is a failed check offering a fix, as the ticket limit's offers to run without one.
func limitFix() *fakeCheck {
	f := &fakeCheck{}
	f.r = preflightResult{verdict: tui.CheckFailed, text: "LIMIT=10 is set: the run would stop after 10 tickets",
		fix: &preflightFix{question: "Run without a limit?", done: "the run has no ticket limit",
			apply: func(s *session) error {
				f.fixed.Add(1)
				s.cfg.Limit = dispatch.NoLimit
				return f.fixErr
			}}}
	return f
}

// passing is a check that passes.
func passing() *fakeCheck {
	return &fakeCheck{r: preflightResult{verdict: tui.CheckPassed, text: "claude starts"}}
}

// startRecord is the start record of the run in repo, which fails the test when there is none.
func startRecord(t *testing.T, repo string) map[string]any {
	t.Helper()
	recs := streamRecords(t, repo)
	if len(recs) == 0 || recs[0]["kind"] != "start" {
		t.Fatalf("no start record: %v", recs)
	}
	return recs[0]
}

func TestOvernightWithEveryCheckPassingRunsTheCurrentTickets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  string // ORCHESTRA_OVERNIGHT
		args []string
	}{
		{"flag", "", []string{"--overnight"}},
		{"variable", "1", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok := passing()
			preflightWith(t, ok.check)
			setenv(t, "ORCHESTRA_OVERNIGHT", tc.env)
			dir := nothingTools(t, backlog{})
			repo, stdout, stderr, code := runNothing(t, func(string) {}, tc.args...)
			if code != dispatch.ExitOK || stderr != "" {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			if ok.ran.Load() != 1 {
				t.Errorf("the check ran %d times, want once", ok.ran.Load())
			}
			out := unstamped(stdout)
			want := "Overnight pre-flight: every check passed\n  passed: claude starts\n✓ All done\n"
			if out != want {
				t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
			}
			if rec := startRecord(t, repo); rec["overnight"] != true {
				t.Errorf("the start record doesn't say overnight: %v", rec)
			}
			if lines := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(lines,
				"overnight pre-flight: passed: claude starts\n") {
				t.Errorf("the log lacks the checklist:\n%s", lines)
			}
			noCalls(t, dir)
		})
	}
}

// Without a terminal nothing is asked: a failed check is listed, its fix not made, and the run
// doesn't start, exit code 2, as a setup problem's.
func TestAFailedPreflightCheckRefusesToStart(t *testing.T) {
	t.Parallel()
	limit, ok := limitFix(), passing()
	preflightWith(t, ok.check, limit.check)
	nothingTools(t, backlog{})
	repo, stdout, stderr, code := runNothing(t, func(string) {}, "--overnight", "--limit", "10")
	if code != dispatch.ExitSetup {
		t.Fatalf("exit %d, want %d; stderr:\n%s", code, dispatch.ExitSetup, stderr)
	}
	const refusal = "orchestra --overnight cannot start: 1 check failed. Fix what the checklist says, " +
		"or start anyway with --force-overnight.\n"
	if stderr != refusal {
		t.Errorf("stderr:\n%s\nwant:\n%s", stderr, refusal)
	}
	want := "Overnight pre-flight: 1 failed, 1 passed\n  passed: claude starts\n" +
		"  failed: LIMIT=10 is set: the run would stop after 10 tickets\n"
	if out := unstamped(stdout); out != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", out, want)
	}
	if limit.fixed.Load() != 0 {
		t.Error("the fix was made without being asked")
	}
	if _, err := os.Stat(filepath.Join(repo, project.RunPath(dispatch.EventsName))); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the run was recorded as started (%v)", err)
	}
}

// --force-overnight starts the run with its failed checks left, and says so.
func TestForceOvernightStartsWithAFailedCheck(t *testing.T) {
	t.Parallel()
	limit := limitFix()
	preflightWith(t, limit.check)
	nothingTools(t, backlog{})
	repo, stdout, stderr, code := runNothing(t, func(string) {}, "--overnight", "--force-overnight")
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if want := "overnight pre-flight: starting with 1 check failed (--force-overnight)\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
	if rec := startRecord(t, repo); rec["overnight"] != true {
		t.Errorf("the start record doesn't say overnight: %v", rec)
	}
}

// A run that isn't overnight has no pre-flight, and its start record says nothing of it.
func TestARunNotOvernightHasNoPreflight(t *testing.T) {
	t.Parallel()
	limit := limitFix()
	preflightWith(t, limit.check)
	nothingTools(t, backlog{})
	repo, stdout, _, code := runNothing(t, func(string) {}, "--tickets")
	if code != dispatch.ExitOK || limit.ran.Load() != 0 || strings.Contains(stdout, "pre-flight") {
		t.Errorf("exit %d, the check ran %d times; stdout:\n%s", code, limit.ran.Load(), stdout)
	}
	if rec := startRecord(t, repo); rec["overnight"] != nil {
		t.Errorf("the start record says overnight: %v", rec)
	}
}
