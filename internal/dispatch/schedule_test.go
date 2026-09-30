package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPickNextSkipsRunningAndHeldTickets(t *testing.T) {
	ready := []Ticket{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	none := func(Ticket) bool { return false }
	if tk, q, _ := pickNext(ready, map[string]bool{"a": true}, none, 1, ""); tk == nil || tk.ID != "b" || q != 2 {
		t.Errorf("got %v, %d", tk, q)
	}
	if tk, _, _ := pickNext(ready, map[string]bool{"a": true, "b": true, "c": true, "d": true}, none, 4, ""); tk != nil {
		t.Errorf("everything is running, got %v", tk)
	}
	heldB := func(t Ticket) bool { return t.ID == "b" }
	if tk, q, _ := pickNext(ready, map[string]bool{"a": true}, heldB, 1, ""); tk == nil || tk.ID != "c" || q != 1 {
		t.Errorf("b is held, got %v, %d", tk, q)
	}
}

// A solo ticket runs alone: nothing starts beside it, and one first in line holds back the tickets
// behind it until nothing runs.
func TestPickNextRunsASoloTicketAlone(t *testing.T) {
	solo := []string{SoloLabel}
	none := func(Ticket) bool { return false }
	ready := []Ticket{{ID: "a"}, {ID: "b"}}
	if tk, q, next := pickNext(ready, map[string]bool{"s": true}, none, 1, "s"); tk != nil || q != 2 || next != "" {
		t.Errorf("s runs solo, got %v, %d, %q", tk, q, next)
	}
	ready = []Ticket{{ID: "s", Labels: solo}, {ID: "a"}, {ID: "b"}}
	if tk, q, next := pickNext(ready, map[string]bool{"r": true}, none, 1, ""); tk != nil || q != 3 || next != "s" {
		t.Errorf("s is next and r runs, got %v, %d, %q", tk, q, next)
	}
	if tk, q, next := pickNext(ready, nil, none, 0, ""); tk == nil || tk.ID != "s" || q != 2 || next != "" {
		t.Errorf("nothing runs, got %v, %d, %q", tk, q, next)
	}
	ready = []Ticket{{ID: "a"}, {ID: "s", Labels: solo}}
	if tk, q, next := pickNext(ready, map[string]bool{"r": true}, none, 1, ""); tk == nil || tk.ID != "a" || q != 1 || next != "" {
		t.Errorf("a comes before s, got %v, %d, %q", tk, q, next)
	}
	heldS := func(t Ticket) bool { return t.ID == "s" }
	ready = []Ticket{{ID: "s", Labels: solo}, {ID: "a"}}
	if tk, _, next := pickNext(ready, map[string]bool{"r": true}, heldS, 1, ""); tk == nil || tk.ID != "a" || next != "" {
		t.Errorf("a held s holds nothing back, got %v, %q", tk, next)
	}
}

// onceReady lists A once; every later 'bd ready' fails.
type onceReady struct {
	readyTickets
	calls *atomic.Int32
}

func (o onceReady) Ready() ([]Ticket, error) {
	if o.calls.Add(1) == 1 {
		return []Ticket{{ID: "A"}}, nil
	}
	return nil, fmt.Errorf("bd ready failed")
}

func TestDispatchTimeStopWithTicketsInFlightHolds(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	sink := &holdSink{release: release}
	// A is dispatched and runs until the HOLD is out; filling the second slot finds bd ready unreadable.
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 10, Concurrency: 2, WTRoot: "wts", LogPath: "log"},
		log, "", Deps{Tickets: onceReady{calls: new(atomic.Int32)}, Tabs: gatedTabs{gated: "A", release: release},
			Agents: noAgents{}, Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}})
	o.SetSink(sink)
	// Without the HOLD, A would wait forever; let it go so the test fails rather than hangs.
	late := time.AfterFunc(5*time.Second, func() { sink.once.Do(func() { close(release) }) })
	defer late.Stop()
	if code := o.Run(context.Background()); code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}

	var holds []string
	for _, ev := range sink.events {
		if ev.Kind == EvHold {
			holds = append(holds, ev.Ticket+"|"+ev.Text)
		}
	}
	want := "|HOLD: READY_UNREADABLE: could not read 'bd ready --json': bd ready failed; no new tickets while the 1 running finish"
	if len(holds) == 0 || holds[0] != want {
		t.Errorf("HOLD events:\n%s\nwant first:\n%s", strings.Join(holds, "\n"), want)
	}
	if logged := read(t, logPath); !strings.Contains(logged, want[1:]) {
		t.Errorf("log lacks %q:\n%s", want[1:], logged)
	}
}

// unreadableReady can't list the ready queue.
type unreadableReady struct{ brokenBd }

func (unreadableReady) Ready() ([]Ticket, error) { return nil, errBd }

func TestReadyUnreadableSaysWhy(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{Repo: "repo", Base: "main"}, log, "", Deps{Tickets: unreadableReady{}, Checkout: cleanCheckout{}})
	_, _, s := o.next(nil)
	want := "READY_UNREADABLE: could not read 'bd ready --json': bd defer A: exit status 1: Error: database is locked (another bd holds it)"
	if s == nil || s.text != want {
		t.Errorf("got %+v, want %q", s, want)
	}
}

// queueSizes returns the queue sizes the loop reported, in order.
func (s *runSink) queueSizes() []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []int
	for _, ev := range s.events {
		if ev.Kind == EvQueue {
			l = append(l, ev.Queued)
		}
	}
	return l
}

// A ticket that becomes ready while a worker runs goes to a free slot at the next poll, not once
// the running ticket finishes.
func TestTicketReadyMidRunTakesAFreeSlot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.beads.add("A", "first", 1)
	bStarted := make(chan struct{})
	h.worker("A", func(w *fakeWorker) string {
		w.claim()
		w.beads.add("B", "follow-up", 2) // the worker files a follow-up
		select {
		case <-bStarted:
		case <-time.After(5 * time.Second):
			t.Error("B waited for A to finish")
		}
		return finishes("a.txt")(w)
	})
	h.worker("B", func(w *fakeWorker) string { close(bStarted); return finishes("b.txt")(w) })
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
}

// With every slot taken, each poll brings the dashboard's queue count up to date, without a line
// in the log.
func TestQueueCountFollowsWhileSlotsAreFull(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) string {
		w.claim()
		w.beads.add("B", "second", 2)
		w.beads.add("C", "third", 3)
		eventually(t, "the queue count never reached 2", func() bool {
			q := h.sink.queueSizes()
			return len(q) > 0 && q[len(q)-1] == 2
		})
		return finishes("a.txt")(w)
	})
	h.worker("B", finishes("b.txt"))
	h.worker("C", finishes("c.txt"))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.queueSizes(); len(got) != 1 || got[0] != 2 {
		t.Errorf("queue sizes reported: %v, want [2]: dispatches carry the rest", got)
	}
	for _, line := range strings.Split(strings.TrimSpace(h.logged()), "\n") {
		if strings.Count(strings.TrimSpace(line), " ") < 2 {
			t.Errorf("log line without text: %q", line)
		}
	}
}

// overlap records which workers run at once.
type overlap struct {
	mu      sync.Mutex
	running map[string]bool
	beside  map[string][]string // each ticket, with the ones found running when it started
}

// runs wraps b to record id's worker as running while it works.
func (v *overlap) runs(id string, b behaviour) behaviour {
	return func(w *fakeWorker) string {
		v.mu.Lock()
		if v.running == nil {
			v.running, v.beside = map[string]bool{}, map[string][]string{}
		}
		for other := range v.running {
			v.beside[id] = append(v.beside[id], other)
			v.beside[other] = append(v.beside[other], id)
		}
		v.running[id] = true
		v.mu.Unlock()
		defer func() {
			v.mu.Lock()
			delete(v.running, id)
			v.mu.Unlock()
		}()
		return b(w)
	}
}

func (v *overlap) of(id string) []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.beside[id]
}

// dispatched returns the tickets dispatched, in order.
func (s *runSink) dispatched() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []string
	for _, ev := range s.events {
		if ev.Kind == EvDispatch {
			l = append(l, ev.Ticket)
		}
	}
	return l
}

// soloStates returns the solo states the loop reported to the dashboard, in order, leaving out
// repeats.
func (s *runSink) soloStates() []SoloState {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []SoloState
	for _, ev := range s.events {
		if (ev.Kind == EvDispatch || ev.Kind == EvQueue) && (len(l) == 0 || l[len(l)-1] != ev.Solo) {
			l = append(l, ev.Solo)
		}
	}
	return l
}

// A solo ticket runs alone: with free slots and tickets ready, nothing starts beside it, and the
// wait is logged once.
func TestSoloTicketNeverRunsAlongsideAnother(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 3
	h.beads.add("S", "split the loop", 1, SoloLabel)
	h.beads.add("A", "first", 2)
	h.beads.add("B", "second", 3)
	var v overlap
	h.worker("S", v.runs("S", func(w *fakeWorker) string {
		eventually(t, "the loop never said A and B wait for S", func() bool {
			return strings.Contains(h.sink.text(), "waiting for solo ticket S to finish")
		})
		time.Sleep(20 * time.Millisecond) // a few more polls
		return finishes("s.txt")(w)
	}))
	h.worker("A", v.runs("A", finishes("a.txt")))
	h.worker("B", v.runs("B", finishes("b.txt")))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := v.of("S"); len(got) != 0 {
		t.Errorf("S ran beside %v", got)
	}
	if got := h.sink.dispatched(); !equal(got, []string{"S", "A", "B"}) {
		t.Errorf("dispatched %v", got)
	}
	if n := strings.Count(h.logged(), "waiting for solo ticket S to finish"); n != 1 {
		t.Errorf("the wait was logged %d times, want once:\n%s", n, h.logged())
	}
	if d := h.sink.of(EvDispatch); len(d) == 0 || d[0] != "S [1/10] S dispatching solo: split the loop" {
		t.Errorf("dispatches:\n%s", strings.Join(d, "\n"))
	}
	if got, want := h.sink.soloStates(), []SoloState{{Ticket: "S"}, {}}; !slices.Equal(got, want) {
		t.Errorf("solo states %v, want %v", got, want)
	}
}

// A solo ticket next in priority while others run holds back the tickets behind it, and starts
// once the running ones finish.
func TestSoloTicketNextInLineHoldsBackNewStarts(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 3
	h.beads.add("A", "first", 1)
	h.beads.add("S", "split the loop", 2, SoloLabel)
	h.beads.add("B", "second", 3)
	var v overlap
	h.worker("A", v.runs("A", func(w *fakeWorker) string {
		eventually(t, "the loop never said S is next", func() bool {
			return strings.Contains(h.sink.text(), "solo ticket S is next")
		})
		time.Sleep(20 * time.Millisecond) // a few more polls
		return finishes("a.txt")(w)
	}))
	h.worker("S", v.runs("S", finishes("s.txt")))
	h.worker("B", v.runs("B", finishes("b.txt")))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	for _, id := range []string{"A", "S", "B"} {
		if got := v.of(id); len(got) != 0 {
			t.Errorf("%s ran beside %v", id, got)
		}
	}
	if got := h.sink.dispatched(); !equal(got, []string{"A", "S", "B"}) {
		t.Errorf("dispatched %v", got)
	}
	if n := strings.Count(h.logged(), "solo ticket S is next: no new tickets start until the running ones finish"); n != 1 {
		t.Errorf("the hold was logged %d times, want once:\n%s", n, h.logged())
	}
	if got, want := h.sink.soloStates(), []SoloState{{}, {Ticket: "S", Next: true}, {Ticket: "S"}, {}}; !slices.Equal(got, want) {
		t.Errorf("solo states %v, want %v", got, want)
	}
}

// With one worker at a time a solo label changes nothing: tickets run in priority order, and
// nothing is said about waiting.
func TestSoloTicketWithOneWorkerChangesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("S", "split the loop", 2, SoloLabel)
	h.beads.add("B", "second", 3)
	h.worker("A", func(w *fakeWorker) string {
		time.Sleep(20 * time.Millisecond) // a few polls with S next
		return finishes("a.txt")(w)
	})
	h.worker("S", func(w *fakeWorker) string {
		time.Sleep(20 * time.Millisecond) // a few polls with S running
		return finishes("s.txt")(w)
	})
	h.worker("B", finishes("b.txt"))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.dispatched(); !equal(got, []string{"A", "S", "B"}) {
		t.Errorf("dispatched %v", got)
	}
	if log := h.logged(); strings.Contains(log, "waiting for solo") || strings.Contains(log, "is next") {
		t.Errorf("a solo wait was logged with one worker:\n%s", log)
	}
	for _, s := range h.sink.soloStates() {
		if s.Next {
			t.Errorf("the dashboard was told a solo ticket waits: %v", h.sink.soloStates())
		}
	}
}
