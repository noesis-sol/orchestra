package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestIdleWorkerWithTicketInProgressGetsGrace(t *testing.T) {
	cases := []struct {
		status TicketStatus
		idle   time.Duration
		wait   bool
	}{
		{"in_progress", time.Minute, true},              // probably waiting on its own background command
		{"in_progress", idleGrace + time.Second, false}, // long enough: it needs someone
		{"closed", 0, false}, {"deferred", 0, false}, {"open", 0, false},
	}
	o := &Loop{}
	for _, c := range cases {
		if settled, _ := o.idleSettled(c.status, "wt", false, time.Time{}, c.idle, startGrace); settled == c.wait {
			t.Errorf("idleSettled(%s, idle %s) = %v, want %v", c.status, c.idle, settled, !c.wait)
		}
	}
}

// scriptedAgents reports the states in its script in turn, then gone; "unreadable" in the script
// fails as a Herdr call does.
type scriptedAgents struct {
	noAgents
	script  []string
	reads   int
	screens []AgentState // the state each screen read was given
}

func (a *scriptedAgents) Screen(ctx context.Context, name string, status AgentState) string {
	a.screens = append(a.screens, status)
	return ""
}

func (a *scriptedAgents) Status(ctx context.Context, name string) (AgentState, error) {
	a.reads++
	if len(a.script) == 0 {
		return "gone", nil
	}
	st := a.script[0]
	a.script = a.script[1:]
	if st == "unreadable" {
		return "", errors.New("herdr agent get: server busy")
	}
	return AgentState(st), nil
}

// newSettleLoop is a loop waiting on a worker whose statuses are script, on the real poll: the
// tests call it inside synctest.Test.
func newSettleLoop(t *testing.T, script ...string) (*Loop, *scriptedAgents, string) {
	t.Helper()
	noLeaks(t)
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	a := &scriptedAgents{script: script}
	return &Loop{log: log, sink: &recordSink{}, agents: a, tickets: readyTickets{}}, a, logPath
}

// A status Herdr fails to read once says nothing about the worker: the wait goes on.
func TestFailedStatusReadDoesNotEndTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, a, logPath := newSettleLoop(t, "working", "unreadable", "working")
		if _, stop := o.waitSettled(t.Context(), worker{id: "A", agent: "A", tab: "tab", wt: "wt", started: time.Now()}, time.Now(), nil); stop != nil {
			t.Fatalf("stopped: %s", stop)
		}
		if a.reads != 4 {
			t.Errorf("read the status %d times, want 4: the wait should last until the worker is gone", a.reads)
		}
		if logged := read(t, logPath); !strings.Contains(logged, "server busy") {
			t.Errorf("the failed read should be logged:\n%s", logged)
		}
	})
}

// Each poll of a busy worker reads its status once, and its screen once, knowing the status: two
// Herdr calls, where a second status read for the dashboard and a failed scrollback read made four.
func TestBusyWorkerCostsTwoHerdrCallsAPoll(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, a, _ := newSettleLoop(t, "working", "blocked", "working")
		w := o.newWatcher("wt", Status{Ticket: "A"})
		start := time.Now()
		if _, stop := o.waitSettled(t.Context(), worker{id: "A", agent: "A", tab: "tab", wt: "wt", started: start}, start, w.report); stop != nil {
			t.Fatalf("stopped: %s", stop)
		}
		if a.reads != 4 || time.Since(start) != 3*statusPoll {
			t.Errorf("read the status %d times in %s, want 4 in %s: once a poll", a.reads, time.Since(start), 3*statusPoll)
		}
		if want := []AgentState{"working", "blocked", "working"}; !slices.Equal(a.screens, want) {
			t.Errorf("screen reads were given %q, want %q: once a poll with the worker, none once it is gone", a.screens, want)
		}
	})
}

func TestStatusUnreadableForLongStopsTheRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		script := make([]string, maxFailedReads+5)
		for i := range script {
			script[i] = "unreadable"
		}
		o, a, _ := newSettleLoop(t, script...)
		start := time.Now()
		_, stop := o.waitSettled(t.Context(), worker{id: "A", agent: "A", tab: "tab", wt: "wt", started: start}, start, nil)
		if stop == nil || stop.code != ExitTool || stop.kind != stopHerdrFailed {
			t.Fatalf("stop = %+v, want HERDR_FAILED", stop)
		}
		if a.reads != maxFailedReads || time.Since(start) != (maxFailedReads-1)*statusPoll {
			t.Errorf("read the status %d times in %s, want %d in %s: a minute's worth",
				a.reads, time.Since(start), maxFailedReads, (maxFailedReads-1)*statusPoll)
		}
	})
}

// Without a ticket limit, a worker going on for long is reported once, and the wait goes on.
func TestLongRunningWorkerIsReportedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		script := make([]string, 2*longRunning/statusPoll) // working for four hours
		for i := range script {
			script[i] = "working"
		}
		o, _, logPath := newSettleLoop(t, script...)
		shown := recordAlerts(o.log)
		if _, stop := o.waitSettled(t.Context(), worker{id: "A", agent: "A", tab: "tab", wt: "wt", started: time.Now()}, time.Now(), nil); stop != nil {
			t.Fatalf("stopped: %s", stop)
		}
		var warned []string
		for _, ev := range o.sink.(*recordSink).events {
			if ev.Kind == EvWarn {
				warned = append(warned, ev.Text)
			}
			if ev.Aside {
				t.Errorf("the ticket is still running, not set aside: %q", ev.Text)
			}
		}
		if len(warned) != 1 || !strings.HasPrefix(warned[0], "  LONG_RUNNING: A still working after 2h in tab tab") {
			t.Errorf("warnings: %q", warned)
		}
		if got := shown.list(); len(got) != 0 {
			t.Errorf("notified %q: the warning needs nothing from the maintainer, it is for the log", got)
		}
		if !strings.Contains(read(t, logPath), "LONG_RUNNING") {
			t.Error("the warning should be logged")
		}
	})
}

// A worker idle with its ticket in progress gets the idle grace, then stops the run.
func TestRunStopsForAWorkerIdleWithItsTicketInProgress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		start := time.Now()
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress in tab tab1") {
			t.Fatalf("exit %d, final %q", code, o.Final())
		}
		if took := time.Since(start); took < idleGrace || took > idleGrace+2*statusPoll {
			t.Errorf("paused after %s, want the %s idle grace", took, idleGrace)
		}
		if got := h.sink.of(EvDispatch); len(got) != 1 {
			t.Errorf("nothing should start after a pause:\n%s", strings.Join(got, "\n"))
		}
		if !strings.Contains(h.beads.notesOf("A"), "went idle with the ticket still in_progress") {
			t.Errorf("notes: %q", h.beads.notesOf("A"))
		}
		if got := activeIDs(o); !equal(got, []string{"A"}) {
			t.Errorf("active: %v, want A left for the reviewer", got)
		}
		if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("A")) {
			t.Error("the paused worker's tab and worktree should be left open")
		}
	})
}

func TestRunStopsForAWorkerBlockedTooLong(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "blocked" })
		start := time.Now()
		o, code := h.run()
		if code != ExitStuck || o.Final() != "BLOCKED >4min: tab tab1 (A) needs attention" {
			t.Fatalf("exit %d, final %q", code, o.Final())
		}
		if took := time.Since(start); took <= blockedLimit || took > blockedLimit+2*statusPoll { // seen blocked at the second poll
			t.Errorf("stopped after %s, want just past %s", took, blockedLimit)
		}
		if got := activeIDs(o); !equal(got, []string{"A"}) {
			t.Errorf("active: %v", got)
		}
	})
}

// A worker whose status Herdr can't tell stops the run once it has stayed that way too long.
func TestRunStopsForAWorkerUnknownTooLong(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "unknown" })
		start := time.Now()
		o, code := h.run()
		want := "UNKNOWN >5min: Herdr can't tell what the worker in tab tab1 (A) is doing; it needs attention"
		if code != ExitStuck || o.Final() != want {
			t.Fatalf("exit %d, final %q", code, o.Final())
		}
		if took := time.Since(start); took <= unknownLimit || took > unknownLimit+2*statusPoll {
			t.Errorf("stopped after %s, want just past %s", took, unknownLimit)
		}
		if !h.alerts.has("Stopped: UNKNOWN on A") || !strings.Contains(read(t, h.logPath), want) {
			t.Errorf("the stop should be logged and notified: %q", h.alerts.list())
		}
		if got := activeIDs(o); !equal(got, []string{"A"}) {
			t.Errorf("active: %v", got)
		}
	})
}

// A worker still going after the ticket limit, whatever Herdr says it is doing, stops the run like
// a paused one: noted on the ticket, its tab and worktree left open. The limit here comes before
// the one on an unknown status.
func TestRunStopsForAWorkerPastTheTicketLimit(t *testing.T) {
	t.Parallel()
	for _, st := range []AgentState{StateWorking, StateUnknown} {
		t.Run(string(st), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.cfg.TicketLimit = 4 * time.Minute
				h.beads.add("A", "first", 1)
				h.beads.add("B", "second", 2)
				h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return st })
				h.herdr.showsAs["A"] = st // from the start: the limit may pass before the worker returns
				o, code := h.run()
				want := "TICKET_LIMIT: A still " + string(st) + " after 4m in tab tab1 (worktree " + h.worktree("A") +
					"); stopping so it can be looked at"
				if code != ExitStuck || o.Final() != want {
					t.Fatalf("exit %d, final %q, want %q", code, o.Final(), want)
				}
				if !h.alerts.has("Stopped: TICKET_LIMIT on A") || !strings.Contains(read(t, h.logPath), want) {
					t.Errorf("the stop should be logged and notified: %q", h.alerts.list())
				}
				if got := h.sink.of(EvDispatch); len(got) != 1 {
					t.Errorf("nothing should start after the stop:\n%s", strings.Join(got, "\n"))
				}
				if !strings.Contains(h.beads.notesOf("A"), "after the 4m ticket limit") {
					t.Errorf("notes: %q", h.beads.notesOf("A"))
				}
				if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("A")) {
					t.Error("the worker's tab and worktree should be left open")
				}
			})
		})
	}
}
