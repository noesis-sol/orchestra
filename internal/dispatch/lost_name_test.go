package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker's terminal can drop its agent name while it works: a Claude Code the worker starts in its
// pane, with the pane's Herdr variables, reports itself as the pane's agent through Herdr's hook, and
// Herdr clears the name (tutti's run of 2026-10-18). Such a worker is looked for in its pane and named
// again, rather than taken for gone and its session resumed beside it.

// unnamed has the terminal drop the name of ticket id's worker, still of the configured kind and at
// work in its pane, then does to it what also does (nil: nothing more).
func unnamed(h *fakeHerdr, id string, also func(h *fakeHerdr, a *fakeAgent)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.agent(id)
	if a == nil {
		h.t.Errorf("no agent named %s to drop the name of", id)
		return
	}
	a.name = ""
	if also != nil {
		also(h, a)
	}
}

// losesName claims the ticket and works on, its name dropped a minute in (see unnamed), and a minute
// later goes on as then does.
func losesName(h *fakeHerdr, also func(h *fakeHerdr, a *fakeAgent), then behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		time.Sleep(time.Minute)
		unnamed(h, w.id, also)
		time.Sleep(time.Minute)
		return then(w)
	}
}

// quits is a worker whose Claude Code quits.
func quits(*fakeWorker) AgentState { return StateGone }

// orchestra-6m70: a worker whose name is dropped twice as it works in its tab is named again each
// time and followed to the end of its ticket, which merges: its session isn't resumed in a new tab,
// and the run doesn't stop.
func TestWorkerThatLosesItsNameIsNamedAgainAndFollowed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", losesName(h.herdr, nil, losesName(h.herdr, nil, finishes("a.txt"))))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 1 {
			t.Errorf("started %d workers on A; want its one: %q", len(starts), starts)
		}
		ev := h.sink.text()
		const named = "  A's worker lost its name in tab tab1; named it A again\n"
		if n := strings.Count(ev, named); n != 2 {
			t.Errorf("events say %d times %q; want twice:\n%s", n, named, ev)
		}
		if got := warnings(h, "A"); len(got) != 0 {
			t.Errorf("A warned about:\n%s", strings.Join(got, "\n"))
		}
		if got := h.sink.of(EvHold); len(got) != 0 {
			t.Errorf("held for %q", got)
		}
	})
}

// A worker that loses its name while the terminal can't say what its tab holds isn't taken for gone:
// its status is unread until the terminal answers again, and it is then named again.
func TestWorkerThatLosesItsNameWhileTheTerminalCannotSayIsNotTakenForGone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			time.Sleep(time.Minute)
			unnamed(h.herdr, "A", func(h *fakeHerdr, _ *fakeAgent) { h.labelFails = true })
			time.Sleep(30 * time.Second) // fewer failed reads than stop the run
			h.herdr.mu.Lock()
			h.herdr.labelFails = false
			h.herdr.mu.Unlock()
			time.Sleep(time.Minute)
			return finishes("a.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 1 {
			t.Errorf("started %d workers on A; want its one: %q", len(starts), starts)
		}
		const unread = "cannot read the status of A's worker; still waiting on it: cannot tell whether tab tab1 is still A's"
		if log := h.logged(); !strings.Contains(log, unread) {
			t.Errorf("log lacks %q:\n%s", unread, log)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "  A's worker lost its name in tab tab1; named it A again\n") {
			t.Errorf("A's worker wasn't named again:\n%s", ev)
		}
	})
}

// A worker that the terminal has under no name, and whose pane holds nothing of it any longer, is
// gone, as before: its session is resumed once, and gone again it stops the run (PAUSED).
func TestWorkerGoneFromUnderItsNameAndFromItsPaneIsResumed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		also func(h *fakeHerdr, a *fakeAgent)
	}{
		{"its tab relabelled", func(h *fakeHerdr, a *fakeAgent) { h.labels[h.panes[a.pane].tab] = "B" }},
		{"its tab closed", func(h *fakeHerdr, a *fakeAgent) { delete(h.labels, h.panes[a.pane].tab) }},
		{"its pane empty", func(h *fakeHerdr, a *fakeAgent) { a.status = StateGone }},
		{"a named agent in its pane", func(h *fakeHerdr, a *fakeAgent) { a.name = "tutti-test" }},
		{"an agent of another kind in its pane", func(h *fakeHerdr, a *fakeAgent) { a.kind = "codex" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				withSession(h, "A")
				h.beads.add("A", "first", 1)
				h.worker("A", losesName(h.herdr, c.also, quits), losesName(h.herdr, c.also, quits))
				o, code := h.run()
				want := "PAUSED: A still in_progress, its worker gone from tab tab2 (worktree " + h.worktree("A") +
					"); its session " + testSession + " was resumed once in this run already, and the next run " +
					"resumes it again; stopping so it can be looked at"
				if code != ExitStuck || o.Final() != want {
					t.Fatalf("exit %d, final %q\nwant %q\n%s", code, o.Final(), want, h.sink.text())
				}
				if starts := h.herdr.argsFor("A"); len(starts) != 2 || !resumedWith(starts[1], testSession) {
					t.Errorf("starts %q; want a new worker, then one resuming %s", starts, testSession)
				}
				if ev := h.sink.text(); strings.Contains(ev, "lost its name") {
					t.Errorf("a worker was named again:\n%s", ev)
				}
			})
		})
	}
}

// renameRefused is the terminal refusing every new name for an agent.
type renameRefused struct{ *fakeHerdr }

func (h renameRefused) RenameAgent(ctx context.Context, name, to string) error { return errNameRefused }

// A worker that lost its name in its tab, which the terminal then refuses to name again, is taken for
// gone, the refusal logged: its session is resumed in a new tab, and that worker finishes the ticket.
func TestWorkerThatLosesItsNameAndCannotBeNamedAgainIsResumed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", losesName(h.herdr, nil, quits), finishes("a.txt"))
		o := h.loop()
		o.namer = renameRefused{h.herdr}
		if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Errorf("starts %q; want a new worker, then one resuming %s", starts, testSession)
		}
		const refused = "A's worker lost its name in tab tab1 and can't be named A again, so it is taken for gone: " +
			"herdr agent start: exit status 1"
		if log := h.logged(); !strings.Contains(log, refused) {
			t.Errorf("log lacks %q:\n%s", refused, log)
		}
		want := "A   RESUMED: A's worker is gone from tab tab1 with the ticket in progress; resuming its session " +
			testSession + " in a new tab (worktree " + h.worktree("A") + ")"
		if got := warnings(h, "A"); !equal(got, []string{want}) {
			t.Errorf("warnings %q\nwant %q", got, want)
		}
	})
}
