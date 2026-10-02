package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// windingLoop is a stopped loop winding down: the workers left running, and what is under way
// that a stop doesn't cut short.
type windingLoop struct {
	running   []dispatch.Status
	finishing []dispatch.Finishing
}

func (l *windingLoop) Running() []dispatch.Status      { return l.running }
func (l *windingLoop) Finishing() []dispatch.Finishing { return l.finishing }

// quitRig is a stop watch that quits as orchestra does, with what it showed, its log and the exit
// codes it exited with.
type quitRig struct {
	stops   *stopWatch
	loop    *windingLoop
	out     *strings.Builder
	log     *dispatch.Log
	logPath string
	exits   []int
}

// newQuitRig returns a rig whose stop watch knows how to quit from the start, as with -plain.
func newQuitRig(t *testing.T, loop *windingLoop) *quitRig {
	t.Helper()
	r := quitRigFor(t, loop)
	r.stops.quitWith(r.quitter())
	return r
}

// quitRigFor returns a rig whose stop watch doesn't know how to quit yet, as with the dashboard open.
func quitRigFor(t *testing.T, loop *windingLoop) *quitRig {
	t.Helper()
	r := &quitRig{stops: catchStops(), loop: loop, out: &strings.Builder{},
		logPath: filepath.Join(t.TempDir(), "orchestra.log")}
	t.Cleanup(r.stops.release)
	var err error
	if r.log, err = dispatch.OpenLog(r.logPath, false, "t"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.log.Close() }) // closed already once it quits
	return r
}

func (r *quitRig) quitter() quitter {
	return quitter{loop: r.loop, log: r.log, out: tui.Printer{Out: r.out},
		exit: func(code int) { r.exits = append(r.exits, code) }}
}

// stopLoop has the loop listen for its stop signal, delivers sig and returns whether the loop took it.
func (r *quitRig) stopLoop(sig os.Signal) bool {
	var took os.Signal
	r.stops.on(func(s os.Signal) { took = s })
	r.stops.deliver(sig)
	return took == sig
}

// logged is the log's text, read from its file.
func (r *quitRig) logged(t *testing.T) string {
	t.Helper()
	return read(t, r.logPath)
}

// quitOnce checks that orchestra quit once, with exit code 130, showing and logging want, and
// closed its log.
func (r *quitRig) quitOnce(t *testing.T, want string) {
	t.Helper()
	if len(r.exits) != 1 || r.exits[0] != dispatch.ExitInterrupted {
		t.Errorf("exits: %v, want one with %d", r.exits, dispatch.ExitInterrupted)
	}
	if !strings.Contains(r.out.String(), want) {
		t.Errorf("shown:\n%s\nwant %q", r.out, want)
	}
	if !strings.Contains(r.logged(t), want) {
		t.Errorf("logged:\n%s\nwant %q", r.logged(t), want)
	}
	if err := r.log.CloseNow(); !errors.Is(err, os.ErrClosed) {
		t.Errorf("the log is still open: closing it again gave %v", err)
	}
}

// While a stopped loop winds down with nothing under way that must finish, a second stop signal,
// SIGTERM and SIGHUP as Ctrl+C, ends orchestra at once, naming the workers left running.
func TestASecondStopSignalQuitsWhenNothingMustFinish(t *testing.T) {
	for _, tc := range []struct {
		sigs []os.Signal
		how  string
	}{
		{[]os.Signal{os.Interrupt, os.Interrupt}, "with Ctrl+C"},
		{[]os.Signal{syscall.SIGTERM, syscall.SIGTERM}, "by SIGTERM"},
		{[]os.Signal{os.Interrupt, syscall.SIGHUP}, "by SIGHUP"},
	} {
		t.Run(tc.how, func(t *testing.T) {
			r := newQuitRig(t, &windingLoop{running: []dispatch.Status{{Ticket: "a-1", Tab: "w1:2"}}})
			if !r.stopLoop(tc.sigs[0]) {
				t.Fatal("the loop didn't take the first signal")
			}
			if len(r.exits) > 0 || r.out.Len() > 0 {
				t.Fatalf("the first signal did more than stop the loop: exits %v, shown\n%s", r.exits, r.out)
			}
			r.stops.deliver(tc.sigs[1])
			r.quitOnce(t, "INTERRUPTED: quit at once "+tc.how+", leaving a-1 (tab w1:2) running with its tab and worktree open")
		})
	}
}

// With a merge under way, the second stop signal says so and how to abandon it, and still skips
// the organs once the merge is done; a third abandons it, naming it.
func TestAStopSignalDuringAMergeWarnsAndTheNextQuits(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sig       os.Signal
		again     string
		how       string
		abandoned bool
	}{
		{"Ctrl+C", os.Interrupt, "press Ctrl+C again", "with Ctrl+C", true},
		{"SIGTERM", syscall.SIGTERM, "send SIGTERM again", "by SIGTERM", true},
		{"merged before the third", os.Interrupt, "press Ctrl+C again", "with Ctrl+C", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := &windingLoop{running: []dispatch.Status{{Ticket: "a-1", Tab: "w1:2"}},
				finishing: []dispatch.Finishing{{Ticket: "a-1", What: "merge"}}}
			r := newQuitRig(t, loop)
			r.stopLoop(tc.sig)
			r.stops.deliver(tc.sig)
			warning := "a-1's merge is under way; " + tc.again + " to abandon it (the repository may be left half merged)"
			if !strings.Contains(r.out.String(), warning) || !strings.Contains(r.logged(t), warning) {
				t.Errorf("shown:\n%s\nlogged:\n%s\nwant %q", r.out, r.logged(t), warning)
			}
			if len(r.exits) > 0 {
				t.Fatalf("quit with the merge under way: %v", r.exits)
			}
			if !tc.abandoned {
				loop.finishing = nil
				r.stops.on(nil) // the loop has wound down
				if out := organRun(t, r.stops); out != "" {
					t.Errorf("after the warning the organ phase printed\n%s", out)
				}
				if len(r.exits) > 0 {
					t.Errorf("quit after the loop had wound down: %v", r.exits)
				}
				return
			}
			r.stops.deliver(tc.sig)
			r.quitOnce(t, "INTERRUPTED: quit at once "+tc.how+", leaving a-1 (tab w1:2) running with its tab and worktree open; "+
				"abandoned a-1's merge: git may still finish it, so check git status before starting another run")
		})
	}
}

// One stop signal only stops the loop: the merge under way finishes, and the organs run after it.
func TestOneStopSignalKeepsTheMergeAndTheOrgans(t *testing.T) {
	r := newQuitRig(t, &windingLoop{finishing: []dispatch.Finishing{{Ticket: "a-1", What: "merge"}}})
	r.stopLoop(os.Interrupt)
	r.stops.on(nil) // the loop has wound down
	if out := organRun(t, r.stops); !strings.Contains(out, "ALL MERGED") {
		t.Errorf("the organ phase printed\n%s", out)
	}
	if len(r.exits) > 0 || r.out.Len() > 0 {
		t.Errorf("exits %v, shown\n%s", r.exits, r.out)
	}
}

// skippedOrgans gets two stop signals while it finishes triage.
type skippedOrgans struct {
	fakeOrgans
	stops    *stopWatch
	skipped  *bool
	reviewed *bool
}

func (o skippedOrgans) FinishTriage(ctx context.Context) {
	o.stops.deliver(os.Interrupt)
	*o.skipped = ctx.Err() != nil
	o.stops.deliver(os.Interrupt)
}

func (o skippedOrgans) Review(ctx context.Context, code int, final string) (string, error) {
	*o.reviewed = true
	return o.fakeOrgans.Review(ctx, code, final)
}

// In the organ phase a stop signal skips the organs, as before, and the one after it quits.
func TestTheSignalAfterTheOneThatSkipsTheOrgansQuits(t *testing.T) {
	r := newQuitRig(t, &windingLoop{})
	var skipped, reviewed bool
	log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "organs.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	organPhase(skippedOrgans{stops: r.stops, skipped: &skipped, reviewed: &reviewed}, options{Triage: true, Review: true},
		r.stops, log, dispatch.ExitOK, "", tui.Printer{Out: &b}, func() {})
	if !skipped || reviewed {
		t.Errorf("the first signal should skip the organs: skipped %v, reviewed %v", skipped, reviewed)
	}
	r.quitOnce(t, "INTERRUPTED: quit at once with Ctrl+C")
}

// The dashboard closes before the loop winds down. A signal while it closes, which owns the
// terminal, waits, and counts once it has: here, with nothing to finish, orchestra quits.
func TestASignalWhileTheDashboardClosesCountsOnceItHas(t *testing.T) {
	r := quitRigFor(t, &windingLoop{})
	r.stopLoop(syscall.SIGTERM) // closes the dashboard
	r.stops.deliver(syscall.SIGTERM)
	if len(r.exits) > 0 {
		t.Fatal("quit while the dashboard had the terminal")
	}
	r.stops.on(nil)
	r.stops.quitWith(r.quitter())
	r.stops.windDown()
	r.quitOnce(t, "INTERRUPTED: quit at once by SIGTERM")
}

// Ctrl+C in the dashboard stops the loop without a signal: the first signal after it is the
// second stop.
func TestASignalAfterTheDashboardsCtrlCQuits(t *testing.T) {
	r := newQuitRig(t, &windingLoop{})
	r.stops.on(func(os.Signal) { t.Error("the dashboard's handler got a signal after it closed") })
	r.stops.on(nil)
	r.stops.windDown()
	if len(r.exits) > 0 {
		t.Fatal("quit with no signal")
	}
	r.stops.deliver(os.Interrupt)
	r.quitOnce(t, "INTERRUPTED: quit at once with Ctrl+C")
}
