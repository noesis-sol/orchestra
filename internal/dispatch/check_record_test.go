package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Each check or setup orchestra runs ends with a check record in the event stream: its suite, the
// ticket, what ran on which commit, for how long and how it ended, and where its output is when it was
// kept. The log has no line for it: its lines are as they were.

// recordedRun runs the loop o as orchestra does, the event stream open, then the full check when it is
// due, and returns the exit code and the stream's check records.
func recordedRun(ctx context.Context, h *harness, o *Loop) (int, []map[string]any) {
	h.t.Helper()
	o.log.Begin(h.repo, RunStart{Started: time.Now(), Repo: h.repo, Branch: "main", Concurrency: h.cfg.Concurrency})
	code := o.Run(ctx)
	if o.FullCheckDue(code) {
		o.FullCheck(context.WithoutCancel(ctx))
	}
	o.log.End(code)
	recs := records(h.t, h.repo)
	logged := len(o.log.RunLines())
	var checks []map[string]any
	texts := 0
	for _, r := range recs {
		if r["kind"] == "check" {
			checks = append(checks, r)
			if text, ok := r["text"]; ok {
				h.t.Errorf("a check record has a text, %q: the log has no line for it", text)
			}
		}
		if _, ok := r["text"]; ok {
			texts++
		}
	}
	if texts != logged {
		h.t.Errorf("%d records with a text, %d lines logged: the log should be as it was", texts, logged)
	}
	return code, checks
}

// checkRecordSays checks that check record r is of suite for ticket, ran command and ended with result,
// with seconds and a commit (want's, when it is set), an output ending in output when that is set, else
// none, and a train only when train is.
func checkRecordSays(t *testing.T, r map[string]any, want map[string]any, output string, train bool) {
	t.Helper()
	absent := []string{"text", "detail"}
	if !train {
		absent = append(absent, "train")
	} else {
		want["train"] = true
	}
	if output == "" {
		absent = append(absent, "output")
	} else if o, _ := r["output"].(string); !strings.HasSuffix(o, output) {
		t.Errorf("check record: output %q, want one ending in %s\n%v", o, output, r)
	}
	if want["ticket"] == "" {
		delete(want, "ticket")
		absent = append(absent, "ticket")
	}
	has(t, r, want, absent...)
	if s, ok := r["seconds"].(float64); !ok || s < 0 {
		t.Errorf("check record: seconds %#v\n%v", r["seconds"], r)
	}
	if c, _ := r["commit"].(string); c == "" || len(c) > 10 {
		t.Errorf("check record: commit %#v, want a short hash\n%v", r["commit"], r)
	}
}

// A passing merge check, a failing one and that ticket's check once more after Base moved on, which
// fails again.
func TestMergeChecksRecordTheirRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.cfg.Check = failsOnlyFor("B", "false")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", busyFor(time.Minute, "a.txt")) // merges after B's check failed: B is checked once more
		h.worker("B", finishes("b.txt"))
		code, checks := recordedRun(t.Context(), h, h.loop())
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if len(checks) != 3 {
			t.Fatalf("%d check records, want B's, A's and B's once more: %v", len(checks), checks)
		}
		checkRecordSays(t, checks[0], map[string]any{"suite": "merge", "ticket": "B", "command": h.cfg.Check,
			"result": "failed"}, checkLogName, false)
		checkRecordSays(t, checks[1], map[string]any{"suite": "merge", "ticket": "A", "command": h.cfg.Check,
			"result": "passed", "commit": short(commitOf(h.mem, "main", "A: add a.txt"))}, "", false)
		checkRecordSays(t, checks[2], map[string]any{"suite": "recheck", "ticket": "B", "command": h.cfg.Check,
			"result": "failed"}, checkLogName, false)
	})
}

// The setup in a ticket's new worktree, before its worker starts, is a check record of its own.
func TestASetupRecordsItsRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check, h.cfg.Setup = "true", "echo installed"
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		code, checks := recordedRun(t.Context(), h, h.loop())
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if len(checks) != 2 {
			t.Fatalf("%d check records, want A's setup and its merge check: %v", len(checks), checks)
		}
		checkRecordSays(t, checks[0], map[string]any{"suite": "setup", "ticket": "A", "command": "echo installed",
			"result": "passed"}, "", false)
		checkRecordSays(t, checks[1], map[string]any{"suite": "merge", "ticket": "A", "command": "true",
			"result": "passed"}, "", false)
	})
}

// The second ticket of the merge train, checked ahead of its turn on the branch before it, says so.
func TestATrainsSecondCheckRecordsItsRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "A.passes"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		h, _, o := trainLoop(t, 1, trainCheck(dir, true))
		code, checks := recordedRun(t.Context(), h, o)
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if len(checks) != 2 {
			t.Fatalf("%d check records, want A's and B's beside it: %v", len(checks), checks)
		}
		byTicket := map[string]map[string]any{}
		for _, r := range checks {
			byTicket[r["ticket"].(string)] = r
		}
		checkRecordSays(t, byTicket["A"], map[string]any{"suite": "merge", "ticket": "A", "result": "passed"}, "", false)
		checkRecordSays(t, byTicket["B"], map[string]any{"suite": "merge", "ticket": "B", "result": "passed",
			"commit": short(commitOf(h.mem, "main", "B: add b.txt"))}, "", true)
	})
}

// The full check is a check record, suite full, and its full_check record gives the same commit,
// seconds and result.
func TestTheFullCheckRecordsItsRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, check, result, output string
	}{
		{"passing", "echo 'SUITE: unit tests'", "passed", ""},
		{"failing", failingFullCheck, "failed", fullCheckLogName},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.cfg.Check, h.cfg.CheckFull = "true", tc.check
				h.beads.add("A", "first", 1)
				h.worker("A", finishes("a.txt"))
				code, checks := recordedRun(t.Context(), h, h.loop())
				if code != ExitOK {
					t.Fatalf("exit %d\n%s", code, h.sink.text())
				}
				if len(checks) != 2 {
					t.Fatalf("%d check records, want A's merge check and the full check: %v", len(checks), checks)
				}
				head := short(commitOf(h.mem, "main", "A: add a.txt"))
				checkRecordSays(t, checks[1], map[string]any{"suite": "full", "ticket": "", "command": tc.check,
					"result": tc.result, "commit": head}, tc.output, false)
				var full map[string]any
				for _, r := range records(t, h.repo) {
					if r["kind"] == "full_check" {
						full = r
					}
				}
				has(t, full, map[string]any{"commit": head, "result": tc.result, "seconds": checks[1]["seconds"]})
			})
		})
	}
}

// A merge check stopped at its time limit, and one a stop cuts short. Both run on the real clock: a
// check is a real process, which the bubble's clock doesn't stop.
func TestMergeChecksStoppedRecordHowTheyEnded(t *testing.T) {
	if testing.Short() {
		t.Skip("a run on the real clock takes seconds")
	}
	t.Parallel()
	t.Run("timed out", func(t *testing.T) {
		t.Parallel()
		h := newTimedHarness(t)
		h.cfg.Check, h.cfg.CheckTimeout = "sleep 30", 300*time.Millisecond
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		code, checks := recordedRun(t.Context(), h, h.loop())
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if len(checks) != 1 {
			t.Fatalf("%d check records, want A's: %v", len(checks), checks)
		}
		checkRecordSays(t, checks[0], map[string]any{"suite": "merge", "ticket": "A", "command": "sleep 30",
			"result": "timed_out"}, checkLogName, false)
		if s, _ := checks[0]["seconds"].(float64); s < 0.3 || s >= 30 {
			t.Errorf("seconds %v, want the time limit's, 0.3, or a little more", s)
		}
	})
	t.Run("cut short", func(t *testing.T) {
		t.Parallel()
		h := newTimedHarness(t)
		started := filepath.Join(t.TempDir(), "started")
		h.cfg.Check = "touch " + started + "; sleep 30"
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		var wg sync.WaitGroup
		defer wg.Wait()
		wg.Go(func() { // Ctrl+C once the check has started
			for !exists(started) && ctx.Err() == nil {
				time.Sleep(20 * time.Millisecond)
			}
			cancel(InterruptedError("with Ctrl+C"))
		})
		code, checks := recordedRun(ctx, h, h.loop())
		if code != ExitInterrupted {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if len(checks) != 1 {
			t.Fatalf("%d check records, want A's: %v", len(checks), checks)
		}
		checkRecordSays(t, checks[0], map[string]any{"suite": "merge", "ticket": "A", "command": h.cfg.Check,
			"result": "cut_short"}, checkLogName, false)
	})
}
