package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestIdleWorkerWithTicketInProgressGetsGrace(t *testing.T) {
	cases := []struct {
		status string
		idle   time.Duration
		wait   bool
	}{
		{"in_progress", time.Minute, true},              // probably waiting on its own background command
		{"in_progress", idleGrace + time.Second, false}, // long enough: it needs someone
		{"closed", 0, false}, {"deferred", 0, false}, {"open", 0, false},
	}
	for _, c := range cases {
		if got := keepWaiting(c.status, c.idle, idleGrace); got != c.wait {
			t.Errorf("keepWaiting(%s, %s) = %v, want %v", c.status, c.idle, got, c.wait)
		}
	}
}

// scriptedAgents reports the statuses in its script in turn, then gone; "unreadable" fails as a
// Herdr call does.
type scriptedAgents struct {
	noAgents
	script  []string
	reads   int
	screens []string // the status each screen read was given
}

func (a *scriptedAgents) Screen(name, status string) string {
	a.screens = append(a.screens, status)
	return ""
}

func (a *scriptedAgents) Status(name string) (string, error) {
	a.reads++
	if len(a.script) == 0 {
		return "gone", nil
	}
	st := a.script[0]
	a.script = a.script[1:]
	if st == "unreadable" {
		return st, errors.New("herdr agent get: server busy")
	}
	return st, nil
}

func newSettleLoop(t *testing.T, script ...string) (*Loop, *scriptedAgents, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	a := &scriptedAgents{script: script}
	return &Loop{log: log, sink: &recordSink{}, agents: a, tickets: readyTickets{}, wait: timing{poll: time.Millisecond}}, a, logPath
}

// A status Herdr fails to read once says nothing about the worker: the wait goes on.
func TestFailedStatusReadDoesNotEndTheWait(t *testing.T) {
	o, a, logPath := newSettleLoop(t, "working", "unreadable", "working")
	if stop := o.waitSettled(context.Background(), "A", "A", "tab", "wt", time.Now(), nil); stop != nil {
		t.Fatalf("stopped: %s", stop.text)
	}
	if a.reads != 4 {
		t.Errorf("read the status %d times, want 4: the wait should last until the worker is gone", a.reads)
	}
	if logged := read(t, logPath); !strings.Contains(logged, "server busy") {
		t.Errorf("the failed read should be logged:\n%s", logged)
	}
}

// Each poll of a busy worker reads its status once, and its screen once, knowing the status: two
// Herdr calls, where a second status read for the dashboard and a failed scrollback read made four.
func TestBusyWorkerCostsTwoHerdrCallsAPoll(t *testing.T) {
	o, a, _ := newSettleLoop(t, "working", "blocked", "working")
	w := o.newWatcher("wt", Status{Ticket: "A"})
	if stop := o.waitSettled(context.Background(), "A", "A", "tab", "wt", time.Now(), w.report); stop != nil {
		t.Fatalf("stopped: %s", stop.text)
	}
	if a.reads != 4 {
		t.Errorf("read the status %d times, want 4: once a poll", a.reads)
	}
	if want := []string{"working", "blocked", "working"}; !slices.Equal(a.screens, want) {
		t.Errorf("screen reads were given %q, want %q: once a poll with the worker, none once it is gone", a.screens, want)
	}
}

func TestStatusUnreadableForLongStopsTheRun(t *testing.T) {
	script := make([]string, maxFailedReads+5)
	for i := range script {
		script[i] = "unreadable"
	}
	o, a, _ := newSettleLoop(t, script...)
	stop := o.waitSettled(context.Background(), "A", "A", "tab", "wt", time.Now(), nil)
	if stop == nil || stop.code != ExitTool || !strings.Contains(stop.text, "HERDR_FAILED") {
		t.Fatalf("stop = %+v, want HERDR_FAILED", stop)
	}
	if a.reads != maxFailedReads {
		t.Errorf("read the status %d times, want %d", a.reads, maxFailedReads)
	}
}

// Without a ticket limit, a worker going on for long is reported once, and the wait goes on.
func TestLongRunningWorkerIsReportedOnce(t *testing.T) {
	script := make([]string, 60)
	for i := range script {
		script[i] = "working"
	}
	o, _, logPath := newSettleLoop(t, script...)
	o.wait.longRun = 5 * time.Millisecond
	shown := recordAlerts(o.log)
	if stop := o.waitSettled(context.Background(), "A", "A", "tab", "wt", time.Now(), nil); stop != nil {
		t.Fatalf("stopped: %s", stop.text)
	}
	var warned []string
	for _, ev := range o.sink.(*recordSink).events {
		if ev.Kind == EvWarn {
			warned = append(warned, ev.Text)
		}
	}
	if len(warned) != 1 || !strings.HasPrefix(warned[0], "  LONG_RUNNING: A still working after 5ms in tab tab") || !shown.has(warned[0]) {
		t.Errorf("warnings: %q", warned)
	}
	if !strings.Contains(read(t, logPath), "LONG_RUNNING") {
		t.Error("the warning should be logged")
	}
}

func TestRunStopsForAWorkerIdleWithItsTicketInProgress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "idle" })
	o, code := h.run()
	if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress in tab tab1") {
		t.Fatalf("exit %d, final %q", code, o.Final())
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
}

func TestRunStopsForAWorkerBlockedTooLong(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "blocked" })
	o, code := h.run()
	if code != ExitStuck || o.Final() != "BLOCKED >4min: tab tab1 (A) needs attention" {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	if got := activeIDs(o); !equal(got, []string{"A"}) {
		t.Errorf("active: %v", got)
	}
}

// A worker whose status Herdr can't tell stops the run once it has stayed that way too long.
func TestRunStopsForAWorkerUnknownTooLong(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) string { w.claim(); return "unknown" })
	o := h.loop()
	o.wait.unknown = 30 * time.Millisecond
	code := o.Run(context.Background())
	want := "UNKNOWN >5min: Herdr can't tell what the worker in tab tab1 (A) is doing; it needs attention"
	if code != ExitStuck || o.Final() != want {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	if !h.alerts.has(o.Final()) || !strings.Contains(read(t, h.logPath), want) {
		t.Error("the stop should be logged and notified")
	}
	if got := activeIDs(o); !equal(got, []string{"A"}) {
		t.Errorf("active: %v", got)
	}
}

// A worker still going after the ticket limit, whatever Herdr says it is doing, stops the run like
// a paused one: noted on the ticket, its tab and worktree left open.
func TestRunStopsForAWorkerPastTheTicketLimit(t *testing.T) {
	t.Parallel()
	for _, st := range []string{"working", "unknown"} {
		t.Run(st, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.cfg.TicketLimit = 50 * time.Millisecond
			h.beads.add("A", "first", 1)
			h.beads.add("B", "second", 2)
			h.worker("A", func(w *fakeWorker) string { w.claim(); return st })
			o := h.loop()
			o.wait.unknown = time.Hour
			code := o.Run(context.Background())
			want := "TICKET_LIMIT: A still " + st + " after 50ms in tab tab1 (worktree " + h.worktree("A") + "); stopping so it can be looked at"
			if code != ExitStuck || o.Final() != want {
				t.Fatalf("exit %d, final %q, want %q", code, o.Final(), want)
			}
			if !h.alerts.has(o.Final()) || !strings.Contains(read(t, h.logPath), want) {
				t.Error("the stop should be logged and notified")
			}
			if got := h.sink.of(EvDispatch); len(got) != 1 {
				t.Errorf("nothing should start after the stop:\n%s", strings.Join(got, "\n"))
			}
			if !strings.Contains(h.beads.notesOf("A"), "after the 50ms ticket limit") {
				t.Errorf("notes: %q", h.beads.notesOf("A"))
			}
			if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("A")) {
				t.Error("the worker's tab and worktree should be left open")
			}
		})
	}
}
