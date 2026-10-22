package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestPickNextSkipsRunningAndHeldTickets(t *testing.T) {
	t.Parallel()
	ready := []Ticket{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	none := func(Ticket) holdKind { return notHeld }
	var sp soloPick // no solo ticket
	if tk, q, _ := pickNext(ready, map[string]bool{"a": true}, none, 1, sp); tk == nil || tk.ID != "b" || q.queued != 2 {
		t.Errorf("got %v, %+v", tk, q)
	}
	if tk, _, _ := pickNext(ready, map[string]bool{"a": true, "b": true, "c": true, "d": true}, none, 4, sp); tk != nil {
		t.Errorf("everything is running, got %v", tk)
	}
	heldB := func(t Ticket) holdKind {
		if t.ID == "b" {
			return heldBlocked
		}
		return notHeld
	}
	if tk, q, _ := pickNext(ready, map[string]bool{"a": true}, heldB, 1, sp); tk == nil || tk.ID != "c" || q.queued != 1 {
		t.Errorf("b is held, got %v, %+v", tk, q)
	}
}

// A solo ticket runs alone: nothing starts beside it, and one first in line holds back the tickets
// behind it until nothing runs.
func TestPickNextRunsASoloTicketAlone(t *testing.T) {
	t.Parallel()
	solo := []string{SoloLabel}
	none := func(Ticket) holdKind { return notHeld }
	var sp soloPick // every solo ticket drains, as with one slot
	ready := []Ticket{{ID: "a"}, {ID: "b"}}
	running := soloPick{running: "s"}
	if tk, q, next := pickNext(ready, map[string]bool{"s": true}, none, 1, running); tk != nil || q.queued != 2 || next != "" {
		t.Errorf("s runs solo, got %v, %+v, %q", tk, q, next)
	}
	ready = []Ticket{{ID: "s", Labels: solo}, {ID: "a"}, {ID: "b"}}
	if tk, q, next := pickNext(ready, map[string]bool{"r": true}, none, 1, sp); tk != nil || q.queued != 3 || next != "s" {
		t.Errorf("s is next and r runs, got %v, %+v, %q", tk, q, next)
	}
	if tk, q, next := pickNext(ready, nil, none, 0, sp); tk == nil || tk.ID != "s" || q.queued != 2 || next != "" {
		t.Errorf("nothing runs, got %v, %+v, %q", tk, q, next)
	}
	ready = []Ticket{{ID: "a"}, {ID: "s", Labels: solo}}
	tk, q, next := pickNext(ready, map[string]bool{"r": true}, none, 1, sp)
	if tk == nil || tk.ID != "a" || q.queued != 1 || next != "" {
		t.Errorf("a comes before s, got %v, %+v, %q", tk, q, next)
	}
	heldS := func(t Ticket) holdKind {
		if t.ID == "s" {
			return heldBlocked
		}
		return notHeld
	}
	ready = []Ticket{{ID: "s", Labels: solo}, {ID: "a"}}
	if tk, _, next := pickNext(ready, map[string]bool{"r": true}, heldS, 1, sp); tk == nil || tk.ID != "a" || next != "" {
		t.Errorf("a held s holds nothing back, got %v, %q", tk, next)
	}
}

// onceReady lists A once; every later 'bd ready' fails.
type onceReady struct {
	readyTickets
	calls *atomic.Int32
}

func (o onceReady) Ready(context.Context, string) ([]Ticket, error) {
	if o.calls.Add(1) == 1 {
		return []Ticket{{ID: "A"}}, nil
	}
	return nil, fmt.Errorf("bd ready failed")
}

func TestDispatchTimeStopWithTicketsInFlightHolds(t *testing.T) {
	t.Parallel()
	noLeaks(t)
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
	o.poll = time.Millisecond // bd ready is read readyTries times, a poll apart
	// Without the HOLD, A would wait forever; let it go so the test fails rather than hangs.
	late := time.AfterFunc(patience, func() { sink.once.Do(func() { close(release) }) })
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

func (unreadableReady) Ready(context.Context, string) ([]Ticket, error) { return nil, errBd }

func TestReadyUnreadableSaysWhy(t *testing.T) {
	t.Parallel()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{Repo: "repo", Base: "main"}, log, "", Deps{Tickets: unreadableReady{}, Checkout: cleanCheckout{}})
	o.poll = time.Millisecond // bd ready is read readyTries times, a poll apart
	_, _, s := newRunState(context.Background(), o).next()
	want := "READY_UNREADABLE: could not read 'bd ready --json': bd defer A: exit status 1: Error: database is locked (another bd holds it)"
	if s == nil || s.kind != stopReadyUnreadable || !errors.Is(s, errBd) || s.Error() != want {
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
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.beads.add("B", "follow-up", 2)    // the worker files a follow-up
			time.Sleep(readyPoll + time.Second) // past the next poll
			return finishes("a.txt")(w)
		}))
		h.worker("B", v.runs("B", finishes("b.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := v.of("B"); !equal(got, []string{"A"}) {
			t.Errorf("B ran beside %v: it waited for A to finish", got)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
	})
}

// With every slot taken, each poll brings the dashboard's queue count up to date, without a line
// in the log.
func TestQueueCountFollowsWhileSlotsAreFull(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.beads.mu.Lock() // both at once: a poll between them would report a queue of 1 first
			w.beads.addLocked("B", "second", 2)
			w.beads.addLocked("C", "third", 3)
			w.beads.mu.Unlock()
			time.Sleep(readyPoll + time.Second) // past the next poll
			return finishes("a.txt")(w)
		})
		h.worker("B", finishes("b.txt"))
		h.worker("C", finishes("c.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		// Dispatches carry the rest, but for the last: none wait, and none are left not closed.
		if got := h.sink.queueSizes(); !slices.Equal(got, []int{2, 0}) {
			t.Errorf("queue sizes reported: %v, want [2 0]: dispatches carry the rest", got)
		}
		for line := range strings.SplitSeq(strings.TrimSpace(h.logged()), "\n") {
			if strings.Count(strings.TrimSpace(line), " ") < 2 {
				t.Errorf("log line without text: %q", line)
			}
		}
	})
}

// overlap records which workers run at once.
type overlap struct {
	mu      sync.Mutex
	running map[string]bool
	beside  map[string][]string // each ticket, with the ones found running when it started
}

// runs wraps b to record id's worker as running while it works.
func (v *overlap) runs(id string, b behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
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

// awaits waits, a poll at a time and for at most polls of them, until other's worker has run beside
// id's, and reports whether it has: a ticket's worker that is to run beside another's then does,
// however long it takes to start.
func (v *overlap) awaits(id, other string, polls int) bool {
	for range polls {
		if slices.Contains(v.of(id), other) {
			return true
		}
		time.Sleep(readyPoll)
	}
	return false
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
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 3
		h.beads.add("S", "split the loop", 1, SoloLabel)
		h.beads.add("A", "first", 2)
		h.beads.add("B", "second", 3)
		var v overlap
		h.worker("S", v.runs("S", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls with A and B waiting
			return finishes("s.txt")(w)
		}))
		h.worker("A", v.runs("A", finishes("a.txt")))
		h.worker("B", v.runs("B", finishes("b.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
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
	})
}

// A solo ticket next in priority while others run, one another ticket waits on, holds back the
// tickets behind it, and starts once the running ones finish.
func TestSoloTicketNextInLineHoldsBackNewStarts(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 3
		h.beads.add("A", "first", 1)
		h.beads.add("S", "split the loop", 2, SoloLabel)
		h.beads.add("B", "second", 3)
		h.beads.add("D", "after the split", 4)
		h.beads.link("D", "S", "blocks")
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls with S next
			return finishes("a.txt")(w)
		}))
		h.worker("S", v.runs("S", finishes("s.txt")))
		h.worker("B", v.runs("B", finishes("b.txt")))
		h.worker("D", finishes("d.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 4 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for _, id := range []string{"A", "S"} {
			if got := v.of(id); len(got) != 0 {
				t.Errorf("%s ran beside %v", id, got)
			}
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A", "S", "B", "D"}) {
			t.Errorf("dispatched %v", got)
		}
		if n := strings.Count(h.logged(), "solo ticket S is next: no new tickets start until the running ones finish"); n != 1 {
			t.Errorf("the hold was logged %d times, want once:\n%s", n, h.logged())
		}
		want := []SoloState{{}, {Ticket: "S", Next: true}, {Ticket: "S"}, {}}
		if got := h.sink.soloStates(); !slices.Equal(got, want) {
			t.Errorf("solo states %v, want %v", got, want)
		}
	})
}

// With one worker at a time a solo label changes nothing while a worker is at work: tickets run in
// priority order, and nothing is said about waiting. Only once a ticket merges, its slot free, may
// a solo ticket next in line be said to wait for it, and the ticket after a solo one for its merge.
func TestSoloTicketWithOneWorkerWaitsOnlyForMerges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("S", "split the loop", 2, SoloLabel)
		h.beads.add("B", "second", 3)
		h.worker("A", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls with S next
			return finishes("a.txt")(w)
		})
		h.worker("S", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls with S running
			return finishes("s.txt")(w)
		})
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A", "S", "B"}) {
			t.Errorf("dispatched %v", got)
		}
		log := h.logged()
		if next := strings.Index(log, "is next"); next >= 0 && next < strings.Index(log, "A settled") {
			t.Errorf("S was said to be next while A's worker was at work:\n%s", log)
		}
		if waits := strings.Index(log, "waiting for solo"); waits >= 0 && waits < strings.Index(log, "S settled") {
			t.Errorf("B was said to wait for S while S's worker was at work:\n%s", log)
		}
	})
}
