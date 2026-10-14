package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// launch recovers a worker's panic as PANIC and leaves its ticket active, as it does a ticket whose
// work returns a reason to stop; one whose work returns none is no longer active.
func TestLaunchRecoversAPanicAndClearsAFinishedTicket(t *testing.T) {
	stopped := halt(ExitTool, stopTerminalFailed, ": the terminal is gone")
	for _, tc := range []struct {
		name       string
		work       func(ctx context.Context, how *settling) *stopReason
		kind       stopKind
		stillThere bool
	}{
		{"finished", func(context.Context, *settling) *stopReason { return nil }, "", false},
		{"stopped", func(context.Context, *settling) *stopReason { return stopped }, stopTerminalFailed, true},
		{"panicked", func(context.Context, *settling) *stopReason { panic("no tab for A") }, stopPanic, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logPath := filepath.Join(t.TempDir(), "orchestra.log")
			log, err := OpenLog(logPath, false, "t")
			if err != nil {
				t.Fatal(err)
			}
			o := New(Config{LogPath: logPath, Concurrency: 1}, log, "", Deps{})
			o.setActive(Status{Ticket: "A", Tab: "w1"})
			r := newRunState(context.Background(), o)
			r.launch("A", true, tc.work)
			res := <-r.results
			if res.id != "A" || !res.counted {
				t.Errorf("result for %q, counted %v; want A, counted", res.id, res.counted)
			}
			if kind := stopKindOf(res.stop); kind != tc.kind {
				t.Errorf("stop %v (kind %q), want kind %q", res.stop, kind, tc.kind)
			}
			if _, ok := o.active["A"]; ok != tc.stillThere {
				t.Errorf("A active: %v, want %v", ok, tc.stillThere)
			}
			if tc.kind == stopPanic {
				want := "PANIC in A: no tab for A; its worktree and tab w1 are left for review (the stack is in " + logPath + ")"
				if res.stop.Error() != want {
					t.Errorf("stop %q, want %q", res.stop.Error(), want)
				}
				if logged := read(t, logPath); !strings.Contains(logged, "panic in A: no tab for A") ||
					!strings.Contains(logged, "TestLaunchRecoversAPanicAndClearsAFinishedTicket") {
					t.Errorf("the log should have the panic with its stack:\n%s", logged)
				}
			}
		})
	}
}

// stopKindOf is s's kind, or "" for no stop.
func stopKindOf(s *stopReason) stopKind {
	if s == nil {
		return ""
	}
	return s.kind
}
