package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// gatedTabs fails to open a tab; for the ticket gated it waits until release is closed first.
type gatedTabs struct {
	gated   string
	release chan struct{}
}

func (g gatedTabs) CreateTab(ctx context.Context, workspace, cwd, label string) (string, string, error) {
	if label == g.gated {
		<-g.release
	}
	return "", "", fmt.Errorf("no such workspace")
}
func (gatedTabs) CloseTab(ctx context.Context, tab string) error { return nil }
func (gatedTabs) TabLabel(ctx context.Context, tab string) (string, bool, error) {
	return "", false, nil // it opens none
}

// holdSink records events and closes release on the first HOLD.
type holdSink struct {
	recordSink
	release chan struct{}
	once    sync.Once
}

func (h *holdSink) Event(ev Event) {
	h.recordSink.Event(ev)
	if ev.Kind == EvHold {
		h.once.Do(func() { close(h.release) })
	}
}

func TestEveryWorkersStopReasonIsReported(t *testing.T) {
	noLeaks(t)
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	sink := &holdSink{release: release}
	// Both workers stop for want of a tab: A first, while B runs; B only once A's HOLD is out.
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 10, Concurrency: 2, WTRoot: "wts", LogPath: "log"},
		log, "", Deps{Tickets: readyTickets{{ID: "A"}, {ID: "B"}}, Tabs: gatedTabs{gated: "B", release: release},
			Agents: noAgents{}, Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}})
	o.SetSink(sink)
	code := o.Run(context.Background())
	if code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}

	var holds []string
	for _, ev := range sink.events {
		if ev.Kind == EvHold {
			holds = append(holds, ev.Ticket+" "+ev.Text)
		}
	}
	wantHolds := []string{
		"A HOLD: TAB_FAILED for A (is 'ws' a valid workspace?); no new tickets while the 1 running finish",
		"B HOLD: TAB_FAILED for B (is 'ws' a valid workspace?)",
	}
	if strings.Join(holds, "\n") != strings.Join(wantHolds, "\n") {
		t.Errorf("HOLD events:\n%s\nwant:\n%s", strings.Join(holds, "\n"), strings.Join(wantHolds, "\n"))
	}
	final := "TAB_FAILED for A (is 'ws' a valid workspace?); also TAB_FAILED for B (is 'ws' a valid workspace?)"
	if o.Final() != final {
		t.Errorf("final line %q, want %q", o.Final(), final)
	}
	logged := read(t, logPath)
	for _, want := range []string{wantHolds[0][2:], wantHolds[1][2:], final} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
}

type deferringTickets struct {
	entered, release chan struct{}
	once             *sync.Once
}

func (d deferringTickets) Ready(context.Context, string) ([]Ticket, error) {
	return []Ticket{{ID: "A", Title: "a"}}, nil
}
func (deferringTickets) Unclosed(context.Context) ([]Ticket, error) { return nil, nil }
func (deferringTickets) Descendants(context.Context, string) ([]Ticket, error) {
	return nil, nil
}
func (deferringTickets) Show(ctx context.Context, id string) (Ticket, error) {
	return Ticket{ID: id, Status: "deferred"}, nil
}
func (deferringTickets) Status(ctx context.Context, id string) (string, error) {
	return "deferred", nil
}
func (deferringTickets) Closed(ctx context.Context, label string) ([]Ticket, error) { return nil, nil }
func (d deferringTickets) Describe(ctx context.Context, id string) string {
	d.once.Do(func() { close(d.entered) })
	<-d.release
	return id
}

// goneSink records events and closes gone when a worker's status is removed, its last act.
type goneSink struct {
	recordSink
	gone chan struct{}
	once sync.Once
}

func (g *goneSink) Status(st Status) {
	if st.Gone {
		g.once.Do(func() { close(g.gone) })
	}
}

func newDeferringLoop(t *testing.T) (*Loop, deferringTickets, *goneSink) {
	t.Helper()
	noLeaks(t)
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	tk := deferringTickets{entered: make(chan struct{}), release: make(chan struct{}), once: &sync.Once{}}
	sink := &goneSink{gone: make(chan struct{})}
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 1, Concurrency: 1, WTRoot: "wts", LogPath: "log",
		AgentKind: "claude"}, log, "", Deps{Tickets: tk, Tabs: noTabs{}, Starter: okStarter{}, Agents: noAgents{},
		Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}, History: quietHistory{},
		Advisor: organ.Client{Bin: filepath.Join(t.TempDir(), "no-claude")}, AdviceCtx: context.Background()})
	o.SetSink(sink)
	o.StartTriage()
	return o, tk, sink
}

// Ctrl+C while a worker gathers a deferral for triage: Run waits for the worker before returning,
// naming it once it has waited a while, so triage closes and the reviewer reads the loop only once
// it has stopped changing.
func TestInterruptedRunWaitsForItsWorkers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, tk, sink := newDeferringLoop(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		codes := make(chan int, 1)
		go func() { codes <- o.Run(ctx) }()
		<-tk.entered
		cancel()
		time.Sleep(settleSay)
		synctest.Wait()
		select {
		case <-codes:
			t.Fatal("Run returned while its worker was still running")
		default:
		}
		if !strings.Contains(sink.text(), "'s last command to finish…") {
			t.Errorf("the worker still out should be named:\n%s", sink.text())
		}
		close(tk.release)
		if code := <-codes; code != ExitInterrupted {
			t.Errorf("exit code %d, want %d", code, ExitInterrupted)
		}
		select {
		case <-sink.gone:
		default:
			t.Error("Run returned before its worker did")
		}
		o.FinishTriage(t.Context())
		if strings.Contains(sink.text(), "TRIAGE") {
			t.Errorf("a deferral after Ctrl+C should not be triaged:\n%s", sink.text())
		}
	})
}

func TestRunMergesEachFinishedTicket(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", finishes("a.txt"))
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got, want := h.sink.of(EvDispatch), []string{"A [1/10] A dispatching: first", "B [2/10] B dispatching: second"}; !equal(got, want) {
		t.Errorf("dispatched:\n%s", strings.Join(got, "\n"))
	}
	if got := h.sink.of(EvClosed); len(got) != 2 || !strings.HasPrefix(got[0], "A ") || !strings.HasPrefix(got[1], "B ") {
		t.Errorf("closed:\n%s", strings.Join(got, "\n"))
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
	if exists(h.worktree("A")) || exists(h.worktree("B")) {
		t.Error("merged tickets' worktrees should be removed")
	}
	if br := h.git(h.repo, "branch", "--list", "wt/*"); br != "" {
		t.Errorf("merged tickets' branches should be deleted: %q", br)
	}
	if got := h.herdr.tabsClosed(); !equal(got, []string{"tab1", "tab2"}) {
		t.Errorf("tabs closed: %v", got)
	}
	if got := h.herdr.pastedTo(); len(got) != 0 {
		t.Errorf("the prompt was given at launch, yet pasted to %v", got)
	}
	if got := activeIDs(o); len(got) != 0 {
		t.Errorf("still active: %v", got)
	}
}

// With two workers, one stopping the run holds it: nothing new starts, and the other finishes and
// merges.
func TestHoldLetsTheRunningWorkerFinish(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		h.worker("B", func(w *fakeWorker) AgentState { <-h.sink.held; return finishes("b.txt")(w) })
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") || strings.Contains(o.Final(), "also") {
			t.Fatalf("exit %d, final %q", code, o.Final())
		}
		holds := h.sink.of(EvHold)
		if len(holds) != 1 || !strings.HasPrefix(holds[0], "A HOLD: PAUSED: A still in_progress") ||
			!strings.HasSuffix(holds[0], "; no new tickets while the 1 running finish") {
			t.Errorf("holds:\n%s", strings.Join(holds, "\n"))
		}
		if !strings.Contains(h.mainLog(), "B: add b.txt") {
			t.Error("B should finish and merge during the hold")
		}
		for _, d := range h.sink.of(EvDispatch) {
			if strings.HasPrefix(d, "C ") {
				t.Errorf("C started during the hold: %s", d)
			}
		}
		if got := activeIDs(o); !equal(got, []string{"A"}) {
			t.Errorf("active: %v", got)
		}
	})
}

func TestBothWorkersStopReasonsAreReported(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		h.worker("B", func(w *fakeWorker) AgentState { <-h.sink.held; w.claim(); return "blocked" })
		o, code := h.run()
		if code != ExitStuck {
			t.Errorf("exit %d, want %d: the first reason decides", code, ExitStuck)
		}
		final := o.Final()
		if !strings.HasPrefix(final, "PAUSED: A still in_progress") || !strings.Contains(final, "; also BLOCKED >4min: tab ") ||
			!strings.HasSuffix(final, " (B) needs attention") {
			t.Errorf("final %q", final)
		}
		holds := h.sink.of(EvHold)
		if len(holds) != 2 || !strings.HasPrefix(holds[0], "A HOLD: PAUSED") || !strings.HasPrefix(holds[1], "B HOLD: BLOCKED") {
			t.Errorf("holds:\n%s", strings.Join(holds, "\n"))
		}
		if got := activeIDs(o); len(got) != 2 {
			t.Errorf("active: %v, want both left for the reviewer", got)
		}
	})
}

// Ctrl+C while a worker works: the run stops at once and leaves the worker, its tab and worktree.
func TestInterruptLeavesAWorkingWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		started, release := make(chan struct{}), make(chan struct{})
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); close(started); <-release; return "idle" })
		defer close(release)
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		codes := make(chan int, 1)
		go func() { codes <- o.Run(ctx) }()
		<-started
		cancel(InterruptedError("by SIGHUP"))
		stopped := time.Now()
		code := <-codes
		want := "INTERRUPTED: stopped by SIGHUP while A (tab " + o.Running()[0].Tab + ") were running; their tabs and worktrees are left open"
		if code != ExitInterrupted || o.Final() != want || time.Since(stopped) != 0 {
			t.Errorf("exit %d, final %q after %s, want %q at once", code, o.Final(), time.Since(stopped), want)
		}
		if got := activeIDs(o); !equal(got, []string{"A"}) {
			t.Errorf("active: %v", got)
		}
		if got := h.sink.goneIDs(); !equal(got, []string{"A"}) {
			t.Errorf("workers returned before Run: %v, want A's", got)
		}
		if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("A")) {
			t.Error("the worker's tab and worktree should be left")
		}
	})
}

// Ctrl+C while a worker waits on a Herdr that doesn't answer: the call is cancelled, and Run
// returns at once, once the worker has.
func TestInterruptStopsAWorkerWaitingOnAHungCommand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.herdr.statusHangs["A"] = true
		started, release := make(chan struct{}), make(chan struct{})
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); close(started); <-release; return "idle" })
		defer close(release)
		o := h.loop()
		ctx, cancel := context.WithCancelCause(t.Context())
		codes := make(chan int, 1)
		go func() { codes <- o.Run(ctx) }()
		<-started
		time.Sleep(time.Minute) // the settle loop is waiting on Herdr
		cancel(InterruptedError("with Ctrl+C"))
		stopped := time.Now()
		if code := <-codes; code != ExitInterrupted || time.Since(stopped) != 0 {
			t.Errorf("exit %d after %s, want %d at once", code, time.Since(stopped), ExitInterrupted)
		}
		if got := h.sink.goneIDs(); !equal(got, []string{"A"}) {
			t.Errorf("workers returned before Run: %v, want A's", got)
		}
		if got := activeIDs(o); !equal(got, []string{"A"}) {
			t.Errorf("active: %v, want A left running", got)
		}
	})
}

// interruptingMerger is git, but Ctrl+C comes as a finished ticket's branch is fast-forwarded,
// which then takes slow.
type interruptingMerger struct {
	Merger
	t      *testing.T
	cancel context.CancelCauseFunc
	slow   time.Duration
}

func (m interruptingMerger) FastForward(ctx context.Context, repo, branch string) (string, error) {
	m.cancel(InterruptedError("with Ctrl+C"))
	time.Sleep(m.slow)
	if ctx.Err() != nil {
		m.t.Error("the merge was cut short by Ctrl+C")
	}
	return m.Merger.FastForward(ctx, repo, branch)
}

// Ctrl+C during a merge: the merge and its cleanup finish, so the repository isn't left half
// merged, and then the run stops.
func TestMergeUnderWayFinishesAfterInterrupt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		o := h.loop()
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.merger = interruptingMerger{Merger: o.merger, t: t, cancel: cancel}
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("exit %d, want %d\n%s", code, ExitInterrupted, h.sink.text())
		}
		if got := h.sink.of(EvClosed); len(got) != 1 || !strings.Contains(got[0], "A closed (") {
			t.Errorf("closed:\n%s", strings.Join(got, "\n"))
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("A should be merged:\n%s", h.mainLog())
		}
		if exists(h.worktree("A")) || slices.Contains(h.mem.branchList(), "wt/A") || !equal(h.herdr.tabsClosed(), []string{"tab1"}) {
			t.Error("A's worktree, branch and tab should be removed")
		}
		if got := h.sink.of(EvDispatch); len(got) != 1 {
			t.Errorf("dispatched after Ctrl+C:\n%s", strings.Join(got, "\n"))
		}
		if got := activeIDs(o); len(got) != 0 {
			t.Errorf("active: %v", got)
		}
		if got := waiting(h.sink); len(got) != 0 {
			t.Errorf("a merge done within a second should go unmentioned:\n%s", strings.Join(got, "\n"))
		}
	})
}

// Ctrl+C during a merge that takes a while: the run says what it waits for, rather than sit
// silent until the merge is done.
func TestInterruptNamesTheMergeItWaitsFor(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o := h.loop()
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.merger = interruptingMerger{Merger: o.merger, t: t, cancel: cancel, slow: 2 * settleSay}
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("exit %d, want %d\n%s", code, ExitInterrupted, h.sink.text())
		}
		if got := waiting(h.sink); !equal(got, []string{"A   waiting for A's merge to finish…"}) {
			t.Errorf("waiting:\n%s", strings.Join(got, "\n"))
		}
		if got := h.sink.of(EvClosed); len(got) != 1 {
			t.Errorf("A should merge:\n%s", h.sink.text())
		}
	})
}

// waiting is what the run said it waits for after Ctrl+C.
func waiting(s *runSink) []string {
	var l []string
	for _, e := range s.of(EvInfo) {
		if strings.Contains(e, "waiting for") {
			l = append(l, e)
		}
	}
	return l
}

func TestInterruptLineNamesTheWorkersLeftRunning(t *testing.T) {
	if got := InterruptLine("with Ctrl+C", nil); got != "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree" {
		t.Errorf("no workers: %q", got)
	}
	got := InterruptLine("by SIGTERM", []Status{{Ticket: "a-1", Tab: "w1:2"}, {Ticket: "a-2", Tab: "w1:3"}})
	if got != "INTERRUPTED: stopped by SIGTERM while a-1 (tab w1:2), a-2 (tab w1:3) were running; their tabs and worktrees are left open" {
		t.Errorf("two workers: %q", got)
	}
}
