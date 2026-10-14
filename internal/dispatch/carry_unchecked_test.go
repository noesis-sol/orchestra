package dispatch

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// unshowableTickets is the tracker, but bd show fails for ticket id, as when another bd holds the
// database for longer than showTries tries.
type unshowableTickets struct {
	Tickets
	id  string
	err error
}

func (u unshowableTickets) Show(ctx context.Context, id string) (Ticket, error) {
	if id == u.id {
		return Ticket{ID: id, Status: StatusUnknown}, u.err
	}
	return u.Tickets.Show(ctx, id)
}

// unlistedWorktrees is the worktrees, but git can't list them.
type unlistedWorktrees struct {
	Worktrees
	err error
}

func (u unlistedWorktrees) WorktreeOf(context.Context, string, string) (string, error) {
	return "", u.err
}

// orchestra-74b2.16: a worker the last run left behind that the next run can't check, as bd can't
// show its ticket or git can't list the worktrees, isn't gone for that: the run keeps it, saying
// why, and saves it again as it was, and the run after carries it over.
func TestCarriedOverWorkerThatCantBeCheckedIsKept(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		fail  func(o *Loop, err error)
		why   string
		cause string
	}{
		{
			"bd can't show it",
			func(o *Loop, err error) { o.tickets = unshowableTickets{Tickets: o.tickets, id: "A", err: err} },
			"bd can't show it",
			"bd show A: database is locked",
		},
		{
			"git can't list the worktrees",
			func(o *Loop, err error) { o.worktrees = unlistedWorktrees{Worktrees: o.worktrees, err: err} },
			"git can't list the worktrees, to find its own",
			"git worktree list: index.lock exists",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				h.worker("A", asksAndStops(hooks.report), finishes("a.txt"))
				if _, code := h.run(); code != ExitOK {
					t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
				}
				first := saved(t, h)
				if len(first) != 1 {
					t.Fatalf("run 1 left %+v; want A", first)
				}

				o := h.loop()
				c.fail(o, errors.New(c.cause))
				if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				if want := "  A, carried over from the last run, is kept for the next run: " + c.why + ": " + c.cause; !strings.Contains(ev, want) {
					t.Errorf("run 2 events lack %q:\n%s", want, ev)
				}
				if strings.Contains(ev, "is dropped") || strings.Contains(ev, "carried over from the last run: A") {
					t.Errorf("A was dropped or carried over:\n%s", ev)
				}
				if got := saved(t, h); !reflect.DeepEqual(got, first) {
					t.Errorf("run 2 saved %+v\nwant A kept as run 1 left it: %+v", got, first)
				}

				h.beads.set("Q", "closed")
				if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := closedIDs(h); !equal(got, []string{"A"}) {
					t.Errorf("merged %v; want A's worker resumed and A merged", got)
				}
			})
		})
	}
}

// orchestra-74b2.16: quitting at once (a further stop signal, see cmd/orchestra) skips the run's
// own saving as it ends, so SaveLeft saves the workers it leaves: those carried over, and those
// still at work, as left running, INTERRUPTED. Before the run has carried over the last run's
// workers it saves nothing, which would forget those it has yet to check.
func TestQuittingAtOnceSavesTheWorkersLeft(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report))
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
		}
		first := saved(t, h)

		o := h.loop()
		o.SaveLeft() // before Run has carried A over
		if got := saved(t, h); !reflect.DeepEqual(got, first) {
			t.Fatalf("saved %+v before the run carried over; want it as run 1 left it: %+v", got, first)
		}
		h.beads.add("B", "second", 2)
		var quit []project.LeftWorker
		h.worker("B", func(w *fakeWorker) AgentState {
			w.claim()
			o.SaveLeft() // as orchestra quits at once
			quit = saved(t, h)
			w.commit("b.txt")
			w.close()
			return "idle"
		})
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := append(slices.Clip(first), project.LeftWorker{Ticket: "B", Agent: "B", Tab: "tab2",
			Pane: "pane2", Worktree: h.worktree("B"), Hooks: true, Left: "INTERRUPTED"})
		if !reflect.DeepEqual(quit, want) {
			t.Errorf("quitting at once saved %+v\nwant %+v", quit, want)
		}
		if got := saved(t, h); !reflect.DeepEqual(got, first) {
			t.Errorf("run 2 saved %+v as it ended; want A as run 1 left it, B merged: %+v", got, first)
		}
	})
}
