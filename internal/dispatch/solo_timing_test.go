package dispatch

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A solo ticket starts where draining the slots costs nothing: first, when no ticket runs, and
// otherwise once no other ticket can start. One at P0, or one other tickets wait on, keeps its
// place in priority order and drains the slots as before.

// The solo ticket's place, as pickNext gives it: ahead of the others when nothing runs, behind them
// while tickets run, unless it drains or is next already.
func TestPickNextStartsASoloTicketWhereDrainingCostsNothing(t *testing.T) {
	t.Parallel()
	solo := []string{SoloLabel}
	none := func(Ticket) holdKind { return notHeld }
	drainsD := soloPick{drains: func(t Ticket) bool { return t.ID == "d" }}
	running := map[string]bool{"r": true}
	cases := []struct {
		name     string
		ready    []Ticket
		skip     map[string]bool
		running  int
		solo     soloPick
		start    string
		queued   int
		wantNext string
	}{
		{name: "nothing runs: the solo ticket first", ready: []Ticket{{ID: "a"}, {ID: "b"}, {ID: "s", Labels: solo}},
			solo: drainsD, start: "s", queued: 2},
		{name: "nothing runs: a draining one in priority order",
			ready: []Ticket{{ID: "a"}, {ID: "d", Labels: solo}}, solo: drainsD, start: "a", queued: 1},
		{name: "nothing runs: the one that doesn't drain ahead of one that does",
			ready: []Ticket{{ID: "d", Labels: solo}, {ID: "s", Labels: solo}}, solo: drainsD, start: "s", queued: 1},
		{name: "tickets run: the others go ahead", ready: []Ticket{{ID: "s", Labels: solo}, {ID: "a"}, {ID: "b"}},
			skip: running, running: 1, solo: drainsD, start: "a", queued: 2},
		{name: "tickets run: next once nothing else can start", ready: []Ticket{{ID: "s", Labels: solo}},
			skip: running, running: 1, solo: drainsD, queued: 1, wantNext: "s"},
		{name: "tickets run: a draining one first in line holds the others back",
			ready: []Ticket{{ID: "d", Labels: solo}, {ID: "a"}}, skip: running, running: 1, solo: drainsD,
			queued: 2, wantNext: "d"},
		{name: "tickets run: a draining one behind a solo one that doesn't",
			ready: []Ticket{{ID: "s", Labels: solo}, {ID: "d", Labels: solo}, {ID: "a"}}, skip: running, running: 1,
			solo: drainsD, queued: 3, wantNext: "d"},
		{name: "tickets run: once next, it holds back the others", ready: []Ticket{{ID: "a"}, {ID: "s", Labels: solo}},
			skip: running, running: 1, solo: soloPick{next: "s", drains: drainsD.drains}, queued: 2, wantNext: "s"},
		{name: "nothing runs: the one next starts", ready: []Ticket{{ID: "t", Labels: solo}, {ID: "s", Labels: solo}},
			solo: soloPick{next: "s", drains: drainsD.drains}, start: "s", queued: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk, q, next := pickNext(c.ready, c.skip, none, c.running, c.solo)
			got := ""
			if tk != nil {
				got = tk.ID
			}
			if got != c.start || q.queued != c.queued || next != c.wantNext {
				t.Errorf("got %q, %+v, next %q; want %q, %d queued, next %q", got, q, next, c.start, c.queued,
					c.wantNext)
			}
		})
	}
}

// A solo ticket once next stays next through a pick with every slot taken: the others stay held
// back for it.
func TestSoloQueueKeepsTheTicketNextWithoutASlot(t *testing.T) {
	t.Parallel()
	const next = "solo ticket s is next: no new tickets start until the running ones finish, then it runs alone"
	q := soloQueue{next: "s", first: "s", said: next}
	if why := q.update("s", false, nil, 2); why != "" {
		t.Errorf("why %q, want nothing new to say", why)
	}
	if q.state() != (SoloState{Ticket: "s", Next: true}) {
		t.Errorf("state %+v, want s next", q.state())
	}
}

// A solo ticket ready at the start runs first, whatever the priority of the others, and alone.
func TestASoloTicketReadyAtTheStartRunsFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 3
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("S", "split the loop", 3, SoloLabel)
		var v overlap
		h.worker("A", v.runs("A", finishes("a.txt")))
		h.worker("B", v.runs("B", finishes("b.txt")))
		h.worker("S", v.runs("S", busyFor(3*readyPoll, "s.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"S", "A", "B"}) {
			t.Errorf("dispatched %v, want S first", got)
		}
		if got := v.of("S"); len(got) != 0 {
			t.Errorf("S ran beside %v", got)
		}
		if got, want := h.sink.soloStates(), []SoloState{{Ticket: "S"}, {}}; !slices.Equal(got, want) {
			t.Errorf("solo states %v, want %v", got, want)
		}
	})
}

// A solo ticket that becomes ready mid-run holds nothing back: S and B are ready once X merges, and
// B takes the slot X left ahead of S. Once nothing else can start, S is next, and from then on
// nothing starts until it has run: F, ready once B merges, waits for S, which starts when A
// finishes.
func TestASoloTicketReadyMidRunLetsTheOthersGoFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("X", "unblock the split", 1)
		h.beads.add("A", "slow", 1)
		h.beads.add("S", "split the loop", 2, SoloLabel)
		h.beads.add("B", "second", 3)
		h.beads.add("F", "after B", 3)
		h.beads.link("S", "X", "blocks")
		h.beads.link("B", "X", "blocks")
		h.beads.link("F", "B", "blocks")
		var v overlap
		h.worker("X", finishes("x.txt"))
		h.worker("A", v.runs("A", busyFor(30*time.Minute, "a.txt")))
		h.worker("S", v.runs("S", finishes("s.txt")))
		h.worker("B", finishes("b.txt"))
		h.worker("F", finishes("f.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 5 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"X", "A", "B", "S", "F"}) {
			t.Errorf("dispatched %v, want B ahead of S, and F after it\n%s", got, h.sink.text())
		}
		if got := v.of("S"); len(got) != 0 {
			t.Errorf("S ran beside %v", got)
		}
		const next = "solo ticket S is next: no new tickets start until the running ones finish"
		if n := strings.Count(h.logged(), next); n != 1 {
			t.Errorf("the hold was logged %d times, want once:\n%s", n, h.logged())
		}
		want := []SoloState{{}, {Ticket: "S", Next: true}, {Ticket: "S"}, {}}
		if got := h.sink.soloStates(); !slices.Equal(got, want) {
			t.Errorf("solo states %v, want %v", got, want)
		}
	})
}

// A solo ticket that drains, ready mid-run with B once X merges, is next at once in priority order:
// B waits behind it while A runs, and starts after it.
func TestADrainingSoloTicketHoldsBackTheOthers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		prio int  // the solo ticket's
		dep  bool // another ticket, D, waits on it
	}{
		{name: "at P0", prio: 0},
		{name: "another ticket waits on it", prio: 2, dep: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.cfg.Concurrency = 2
				h.beads.add("X", "unblock the split", 1)
				h.beads.add("A", "slow", 1)
				h.beads.add("S", "split the loop", c.prio, SoloLabel)
				h.beads.add("B", "second", 3)
				h.beads.link("S", "X", "blocks")
				h.beads.link("B", "X", "blocks")
				tickets := 4
				if c.dep {
					h.beads.add("D", "after the split", 4)
					h.beads.link("D", "S", "blocks")
					h.worker("D", finishes("d.txt"))
					tickets++
				}
				var v overlap
				h.worker("X", finishes("x.txt"))
				h.worker("A", v.runs("A", busyFor(30*time.Minute, "a.txt")))
				h.worker("S", v.runs("S", finishes("s.txt")))
				h.worker("B", finishes("b.txt"))
				o, code := h.run()
				if want := fmt.Sprintf("READY_EMPTY after %d tickets", tickets); code != ExitOK || o.Final() != want {
					t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
				}
				if got := h.sink.dispatched(); len(got) < 4 || !equal(got[:4], []string{"X", "A", "S", "B"}) {
					t.Errorf("dispatched %v, want S ahead of B\n%s", got, h.sink.text())
				}
				if got := v.of("S"); len(got) != 0 {
					t.Errorf("S ran beside %v", got)
				}
				const next = "solo ticket S is next: no new tickets start until the running ones finish"
				if n := strings.Count(h.logged(), next); n != 1 {
					t.Errorf("the hold was logged %d times, want once:\n%s", n, h.logged())
				}
			})
		})
	}
}
