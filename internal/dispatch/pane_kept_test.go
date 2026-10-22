package dispatch

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// orchestra-xj6z.1: a worker's pane is kept where the run records it as its ticket waits on a question
// or is set aside with it left in its tab, so a worker whose terminal drops its name (see unnamed) is
// still looked for there, as it is while it works, and not taken for gone.

// A's worker asks Q; answered in its tab, it claims A again and its name is dropped there (tutti's
// run of 2026-10-18). The run finds it unnamed in its pane, names it again and adopts it: its session
// isn't resumed in a new tab beside it, and A merges.
func TestAskedWorkerThatLosesItsNameAfterItsAnswerIsFoundInItsPane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.ask("Q", "which way?")
			w.shows("idle") // asked, and stopped
			<-answered
			w.shows("working")
			w.claim()
			unnamed(h.herdr, "A", nil)
			time.Sleep(5 * time.Minute) // the run polls A meanwhile
			w.commit("a.txt")
			w.close()
			return "idle"
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			for len(h.sink.eventsOf(EvAsked)) == 0 { // A is set aside, asked
				time.Sleep(time.Second)
			}
			w.beads.set("Q", "closed")
			close(answered)
			return busyFor(10*time.Minute, "b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		tab := tabOf(h, "A")
		ev := h.sink.text()
		if want := "  A's worker was without its name in tab " + tab + "; named it A again\n"; !strings.Contains(ev, want) {
			t.Errorf("no %q in events:\n%s", want, ev)
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 1 {
			t.Errorf("started %d workers on A; want its one, not its session resumed beside it: %q", len(starts), starts)
		}
		if got := closedIDs(h); !slices.Contains(got, "A") || !slices.Contains(got, "B") {
			t.Errorf("merged %v; want A and B", got)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
			t.Errorf("main lacks A's commit:\n%s", log)
		}
	})
}

// A, with an earlier attempt on its branch, fails at once and is set aside, its worker left idle in
// its tab; so does B, and the hold for the environment reopens both. A's earlier worker's name is
// dropped meanwhile: A, dispatched again, finds it unnamed in its pane and closes its tab, rather
// than take it for gone and start a new worker beside it.
func TestLeftOpenWorkerThatLosesItsNameIsFoundInItsPaneWhenTakenUpAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.cfg.Limit = 3
		withSession(h, "A")
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		earlierAttempt(h, "A")
		earlierAttempt(h, "B")
		tabs := -1
		h.worker("A", givesUp, func(w *fakeWorker) AgentState {
			tabs = openTabsOf(h, "A")
			return finishes("a.txt")(w)
		})
		h.worker("B", givesUp)
		h.worker(probeID, func(w *fakeWorker) AgentState {
			unnamed(h.herdr, "A", nil)
			return runsProbe(w)
		})

		o, code := h.run()
		if code != ExitOK || o.Final() != "LIMIT_REACHED at 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		if got := dispatches(h, "A"); got != 2 {
			t.Fatalf("A dispatched %d times, want twice:\n%s", got, h.sink.text())
		}
		h.herdr.mu.Lock()
		var earlier string
		for _, p := range h.herdr.panes {
			if p.ticket == "A" && (earlier == "" || p.tab < earlier) {
				earlier = p.tab
			}
		}
		h.herdr.mu.Unlock()
		if want := "  A's worker was without its name in tab " + earlier + "; named it A again\n"; !strings.Contains(
			h.sink.text(), want) {
			t.Errorf("no %q in events:\n%s", want, h.sink.text())
		}
		if !slices.Contains(h.herdr.tabsClosed(), earlier) {
			t.Errorf("A's earlier tab %s was not closed; tabs closed %v\n%s", earlier, h.herdr.tabsClosed(), h.logged())
		}
		if tabs != 1 {
			t.Errorf("A's new worker ran beside %d tabs labelled A, want its own alone", tabs)
		}
	})
}

// A's worker settles with A open and is left in its tab; closed there, A is merged as a late close,
// and Ctrl+C comes as the run waits for its worker, at work again, to settle. A is left running, and
// the state file keeps its worker's pane, for the next run to look in should its name be dropped.
func TestLateCloseCutShortSavesItsWorkersPane(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "left open", 1)
		h.beads.add("C", "closes A", 2)
		h.worker("A", leavesOpen)
		h.worker("C", func(w *fakeWorker) AgentState {
			time.Sleep(startGrace + 2*time.Minute) // A is set aside meanwhile
			closesInTab(h, "A", "a.txt")
			return finishes("c.txt")(w)
		})
		o := h.loop()
		ctx, cancel := context.WithCancelCause(t.Context())
		var armed atomic.Bool
		o.agents = cutAtStatus{Agents: o.agents, id: "A", armed: &armed, cut: func() {
			cancel(InterruptedError("with Ctrl+C"))
		}}
		o.SetSink(cutSink{runSink: h.sink, on: func(ev Event) {
			if strings.Contains(ev.Text, "LATE_CLOSE: A") {
				h.herdr.mu.Lock()
				h.herdr.agent("A").status = StateWorking // it carries on as A merges
				h.herdr.mu.Unlock()
				armed.Store(true)
			}
		}})
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		pane := ""
		h.herdr.mu.Lock()
		for id, p := range h.herdr.panes {
			if p.ticket == "A" {
				pane = id
			}
		}
		h.herdr.mu.Unlock()
		got := saved(t, h)
		i := slices.IndexFunc(got, func(e project.LeftWorker) bool { return e.Ticket == "A" })
		if i < 0 || got[i].Pane != pane || pane == "" {
			t.Errorf("saved %+v; want A's worker in pane %q\n%s", got, pane, h.sink.text())
		}
	})
}
