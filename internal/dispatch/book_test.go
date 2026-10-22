package dispatch

import (
	"maps"
	"slices"
	"testing"
)

// checkBook has s check o's ticket book after every event it gets, failing t when a record breaks
// the book's invariants (see ticketBook.check): every scenario on the harness checks them so.
func (s *runSink) checkBook(t *testing.T, o *Loop) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.check = func() {
		if err := o.book.check(); err != nil {
			t.Errorf("after an event, the ticket book: %v", err)
		}
	}
}

// bookAfter is what the book says after a step of TestTicketBookTransitions.
type bookAfter struct {
	aside, keptOut, asked, left, setup []string
	unmerged                           map[string]string
}

// said is what b says of its tickets: those set aside in order, the rest by ID.
func said(b *ticketBook) bookAfter {
	s := b.snapshot()
	var a bookAfter
	a.aside, a.keptOut = b.asideList(), b.keptOut()
	a.asked, a.left = slices.Sorted(maps.Keys(s.asked)), slices.Sorted(maps.Keys(s.left))
	a.setup = b.ids(func(r *ticketRecord) bool { return r.disp == dispSetupFailed })
	if len(s.unmerged) > 0 {
		a.unmerged = s.unmerged
	}
	return a
}

// TestTicketBookTransitions moves tickets through the book's transitions and checks, after each, what
// pick skips, what the holds read and what the reviewer lists as set aside, and the invariants.
func TestTicketBookTransitions(t *testing.T) {
	t.Parallel()
	var b ticketBook
	lo := leftOpen{w: worker{workerRef: refOf("L"), id: "L"}}
	for _, step := range []struct {
		name string
		do   func()
		want bookAfter
	}{
		{"set aside twice, in order without repeats", func() { b.setAside("A"); b.setAside("B"); b.setAside("A") },
			bookAfter{aside: []string{"A", "B"}, keptOut: []string{"A", "B"}}},
		{"asked: set aside, yet back once answered", func() { b.ask("Q", workerSpot{workerRef: refOf("Q"), question: "Q-1"}) },
			bookAfter{aside: []string{"A", "B", "Q"}, keptOut: []string{"A", "B"}, asked: []string{"Q"}}},
		{"carried over: asked, not set aside", func() { b.carry("C", workerSpot{workerRef: refOf("C"), question: "C-1"}) },
			bookAfter{aside: []string{"A", "B", "Q"}, keptOut: []string{"A", "B"}, asked: []string{"C", "Q"}}},
		{"answered: running, still set aside", func() { b.answer("Q") },
			bookAfter{aside: []string{"A", "B", "Q"}, keptOut: []string{"A", "B", "Q"}, asked: []string{"C"}}},
		{"carried, answered: in the run", func() { b.answer("C") },
			bookAfter{aside: []string{"A", "B", "Q"}, keptOut: []string{"A", "B", "Q"}}},
		{"left open, followed", func() { b.setAside("L"); b.leaveOpen("L", lo) },
			bookAfter{aside: []string{"A", "B", "Q", "L"}, keptOut: []string{"A", "B", "L", "Q"}, left: []string{"L"}}},
		{"no longer followed, still set aside", func() { b.unfollow("L") },
			bookAfter{aside: []string{"A", "B", "Q", "L"}, keptOut: []string{"A", "B", "L", "Q"}}},
		{"left open, back in the queue: followed still", func() {
			b.setAside("R")
			b.leaveOpen("R", leftOpen{w: worker{workerRef: refOf("R"), id: "R"}})
			b.requeue("R")
		}, bookAfter{aside: []string{"A", "B", "Q", "L"}, keptOut: []string{"A", "B", "L", "Q"}, left: []string{"R"}}},
		{"dispatched again: no longer followed", func() { b.unfollow("R"); b.answer("R") },
			bookAfter{aside: []string{"A", "B", "Q", "L"}, keptOut: []string{"A", "B", "L", "Q"}}},
		{"back in the queue", func() { b.requeue("B") },
			bookAfter{aside: []string{"A", "Q", "L"}, keptOut: []string{"A", "L", "Q"}}},
		{"left unmerged", func() { b.leaveUnmerged("Q", string(asideConflict)) },
			bookAfter{aside: []string{"A", "Q", "L"}, keptOut: []string{"A", "L", "Q"},
				unmerged: map[string]string{"Q": string(asideConflict)}}},
		{"merged: neither set aside nor unmerged", func() { b.merged("Q") },
			bookAfter{aside: []string{"A", "L"}, keptOut: []string{"A", "L"}}},
		{"left unmerged by an earlier run: not set aside", func() { b.noteUnmerged("E", earlierRun) },
			bookAfter{aside: []string{"A", "L"}, keptOut: []string{"A", "L"}, unmerged: map[string]string{"E": earlierRun}}},
		{"asked, closed as the run ends: still asked", func() {
			b.carry("D", workerSpot{workerRef: refOf("D"), question: "D-1"})
			b.leaveUnmerged("D", string(asideAskedUnmerged))
		}, bookAfter{aside: []string{"A", "L", "D"}, keptOut: []string{"A", "L"}, asked: []string{"D"},
			unmerged: map[string]string{"D": string(asideAskedUnmerged), "E": earlierRun}}},
		// orchestra-3w84: a ticket back in the queue after its setup failed is no longer set aside.
		{"setup failed, reopened: not set aside", func() { b.setupFailed("A", failedSetup{output: "a.log"}) },
			bookAfter{aside: []string{"L", "D"}, keptOut: []string{"L"}, asked: []string{"D"}, setup: []string{"A"},
				unmerged: map[string]string{"D": string(asideAskedUnmerged), "E": earlierRun}}},
		{"setup failed, not reopened: stays set aside", func() {
			b.setAside("S")
			b.setupFailed("S", failedSetup{output: "s.log", notReopened: true})
		}, bookAfter{aside: []string{"L", "D", "S"}, keptOut: []string{"L", "S"}, asked: []string{"D"},
			setup: []string{"A", "S"}, unmerged: map[string]string{"D": string(asideAskedUnmerged), "E": earlierRun}}},
		{"setups retried", func() { b.retrySetups() },
			bookAfter{aside: []string{"L", "D", "S"}, keptOut: []string{"L", "S"}, asked: []string{"D"},
				unmerged: map[string]string{"D": string(asideAskedUnmerged), "E": earlierRun}}},
	} {
		step.do()
		if got := said(&b); !equalAfter(got, step.want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", step.name, got, step.want)
		}
		if err := b.check(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
	}
	if _, ok := b.get("A"); ok {
		t.Error("A, back in the queue with its setup retried, still has a record")
	}
}

func equalAfter(a, b bookAfter) bool {
	return slices.Equal(a.aside, b.aside) && slices.Equal(a.keptOut, b.keptOut) && slices.Equal(a.asked, b.asked) &&
		slices.Equal(a.left, b.left) && slices.Equal(a.setup, b.setup) && maps.Equal(a.unmerged, b.unmerged)
}

// TestTicketBookCheckFindsBrokenRecords pins that the invariants the scenario tests check catch a
// record no transition makes.
func TestTicketBookCheckFindsBrokenRecords(t *testing.T) {
	t.Parallel()
	for name, r := range map[string]ticketRecord{
		"empty":                         {},
		"in, yet set aside":             {disp: dispIn, aside: 1},
		"aside, yet not set aside":      {disp: dispAside},
		"running, yet not set aside":    {disp: dispRunning},
		"unmerged with no reason":       {disp: dispUnmerged, aside: 1},
		"reopened, yet set aside":       {disp: dispSetupFailed, aside: 1, setup: failedSetup{output: "x"}},
		"asked worker kept when aside":  {disp: dispAside, aside: 1, spot: workerSpot{question: "Q"}},
		"left open with another worker": {disp: dispLeftOpen, aside: 1, left: leftOpen{w: worker{id: "other"}}},
		"failed setup kept when in":     {disp: dispIn, unmerged: earlierRun, setup: failedSetup{output: "x"}},
		"set aside past the count":      {disp: dispAside, aside: 2},
		"asked with no tab": {disp: dispAsked, aside: 1,
			spot: workerSpot{workerRef: workerRef{wt: "/wt/T", agent: "T"}, question: "Q"}},
		"left open with no worktree": {disp: dispLeftOpen, aside: 1,
			left: leftOpen{w: worker{workerRef: workerRef{tab: "tab-T", agent: "T"}, id: "T"}}},
		"placed with no name": {disp: dispIn, placed: workerSpot{workerRef: workerRef{tab: "tab-T", wt: "/wt/T"}}},
	} {
		b := ticketBook{recs: map[string]*ticketRecord{"T": &r}, asides: 1}
		if err := b.check(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	b := ticketBook{recs: map[string]*ticketRecord{"T": {disp: dispAside, aside: 1}, "U": {disp: dispAside, aside: 1}},
		asides: 1}
	if err := b.check(); err == nil {
		t.Error("two tickets in one place: no error")
	}
}
