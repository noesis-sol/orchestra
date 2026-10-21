package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// An overnight run sets aside a ticket that needs the maintainer for itself alone, rather than stop
// for it at 1am: deferred with a note saying why, its worktree and tab left for review, and the run
// goes on with the next ticket. The run's report lists it with the tickets set aside.

// overnightAside runs A, whose worker behaves as a says, then B, which finishes, in an overnight run
// on h, and checks that A is set aside for why, of kind, and the run goes on to merge B.
func overnightAside(t *testing.T, h *harness, kind stopKind, why string, a ...behaviour) *Loop {
	t.Helper()
	h.cfg.Overnight = true
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", a...)
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q; want the run to go on past A\n%s", code, o.Final(), h.sink.text())
	}
	if got := closedIDs(h); !equal(got, []string{"B"}) {
		t.Errorf("merged %v; want B", got)
	}
	if st := h.statusOf("A"); st != StatusDeferred {
		t.Errorf("A is %s; want deferred", st)
	}
	if got := o.asideList(); !equal(got, []string{"A"}) {
		t.Errorf("set aside: %v; want A", got)
	}
	note := "Orchestra: " + why + "; deferred for review rather than stopping the overnight run (" + string(kind) + ": "
	if notes := h.beads.notesOf("A"); !strings.Contains(notes, note) {
		t.Errorf("A's notes lack %q:\n%s", note, notes)
	}
	line := "  A: " + why + " -> noted and deferred, the run being an overnight one (" + string(kind) + "); "
	if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.Contains(got[0], line) {
		t.Errorf("deferred %q; want a line with %q", got, line)
	}
	if h.sink.stopped() != "" || len(h.sink.of(EvHold)) != 0 {
		t.Errorf("the run stopped or held:\n%s", h.sink.text())
	}
	if !exists(h.worktree("A")) {
		t.Error("A's worktree should be left for review")
	}
	if in := o.reviewInput(t.Context(), ExitOK, o.Final()); !strings.Contains(in, "A: first [deferred]") {
		t.Errorf("the report's evidence doesn't list A set aside:\n%s", in)
	}
	return o
}

func TestOvernightSetsAsideAWorkerIdleWithItsTicketInProgress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		overnightAside(t, h, stopPaused, "its worker went idle with the ticket still in_progress",
			func(w *fakeWorker) AgentState { w.claim(); return StateIdle })
		if closed := h.herdr.tabsClosed(); slices.Contains(closed, "tab1") {
			t.Errorf("closed tabs %v; want A's, tab1, left open", closed)
		}
	})
}

func TestOvernightSetsAsideAWorkerBlockedTooLong(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		o := overnightAside(t, h, stopBlocked, "its worker was blocked, waiting on a dialog, for over 4min",
			func(w *fakeWorker) AgentState { w.claim(); return StateBlocked })
		if _, left := o.leftOpenSpot("A"); !left {
			t.Error("A's worker should be left in its tab, followed there")
		}
	})
}

func TestOvernightSetsAsideAWorkerUnknownTooLong(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		overnightAside(t, h, stopUnknown, "the terminal could not tell what its worker was doing for over 5min",
			func(w *fakeWorker) AgentState { w.claim(); return StateUnknown })
	})
}

func TestOvernightSetsAsideAWorkerPastTheTicketLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.TicketLimit = 4 * time.Minute
		h.herdr.showsAs["A"] = StateWorking // from the start: the limit may pass before the worker returns
		overnightAside(t, h, stopTicketLimit, "its worker was still working after the 4m ticket limit",
			func(w *fakeWorker) AgentState { w.claim(); return StateWorking })
	})
}

// A worker gone from its tab with no session to resume is set aside, as is one gone a second time.
func TestOvernightSetsAsideAWorkerGoneWithoutASession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.reporter = &hookReporter{}
		overnightAside(t, h, stopPaused, "its worker is gone from tab tab1 with the ticket still in_progress", vanishes)
	})
}

func TestOvernightSetsAsideAWorkerGoneASecondTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		overnightAside(t, h, stopPaused, "its worker is gone from tab tab2 with the ticket still in_progress",
			vanishes, vanishes)
		if n := len(h.herdr.argsFor("A")); n != 2 {
			t.Errorf("started %d workers on A; want 2, its session resumed once", n)
		}
	})
}

// An asked ticket whose worker, claiming it again after its question, is gone from its tab with no
// session to resume is set aside in an overnight run, rather than stopping it.
func TestOvernightSetsAsideAnAskedTicketWhoseWorkerIsGone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Overnight = true
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			w.claim()
			return StateGone // its tab closed by hand, say
		}))
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitOK || h.sink.stopped() != "" || len(h.sink.of(EvHold)) != 0 {
			t.Fatalf("exit %d, final %q; want the run to go on past A\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B"}) {
			t.Errorf("merged %v; want B", got)
		}
		if st := h.statusOf("A"); st != StatusDeferred {
			t.Errorf("A is %s; want deferred", st)
		}
		why := "its worker is gone from tab tab1 with the ticket still in_progress, after its question Q"
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "Orchestra: "+why+
			"; deferred for review rather than stopping the overnight run (PAUSED: worktree "+h.worktree("A")) {
			t.Errorf("A's notes:\n%s", notes)
		}
		if _, asked := o.asked("A"); asked {
			t.Error("A should no longer be followed as asked")
		}
	})
}

// A stop that isn't over one ticket alone stops an overnight run as any other: git failing to say
// what a branch holds in the main checkout, here.
func TestOvernightStopsWhenGitFailsOnTheMerge(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Overnight = true
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", finishes("b.txt")) // started as A's merge goes on
		g := &mergeGitFails{fakeGit: h.mem, fail: "Head", times: -1}
		o := h.loop()
		o.checkout, o.merger = g, g
		code := o.Run(t.Context())
		if code != ExitTool || !strings.HasPrefix(o.Final(), "GIT_FAILED: git can't tell ") {
			t.Fatalf("exit %d, final %q; want GIT_FAILED\n%s", code, o.Final(), h.sink.text())
		}
		if st := h.statusOf("A"); st != StatusClosed || strings.Contains(h.beads.notesOf("A"), "overnight") {
			t.Errorf("A is %s, notes %q; want it left closed and unmerged, not set aside", st, h.beads.notesOf("A"))
		}
	})
}

// Workers failing at once still hold an overnight run for the environment: carrying on would fail
// every ticket.
func TestOvernightHoldsForTheEnvironment(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Overnight = true
		h.holdForEnvironment(time.Minute)
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3) // no behaviour: a worker on it fails the test
		h.worker("A", givesUp)
		h.worker("B", givesUp)
		o, code := h.run()
		if code != ExitEnvironment || !strings.HasPrefix(o.Final(), "ENVIRONMENT: ") {
			t.Fatalf("exit %d, final %q; want ENVIRONMENT\n%s", code, o.Final(), h.logged())
		}
	})
}

// A worker past the ticket limit of an overnight run has its ticket set aside, as its budget says.
func TestOvernightBudgetSaysTheTicketIsSetAside(t *testing.T) {
	t.Parallel()
	for _, overnight := range []bool{false, true} {
		o := New(Config{TicketLimit: 2 * time.Hour, Overnight: overnight}, nil, "", Deps{})
		want := "a worker still going after that stops the run"
		if overnight {
			want = "a worker still going after that has its ticket set aside"
		}
		if got := o.budgetNote(); !strings.Contains(got, want) {
			t.Errorf("overnight %v: budget %q; want %q", overnight, got, want)
		}
	}
}
