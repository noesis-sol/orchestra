package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A worker a run leaves waiting on a question has no ForkKey, as its ticket isn't labelled
// unmerged: the run notes where its branch was cut in the state file, so that the next run takes
// only the branch's own commits for its merge.

// An asked worker whose branch was cut just after a commit naming its ticket, which closes the ticket
// with nothing committed between runs, is taken up by the next run rather than dropped as merged by
// hand, while one whose own commit is on main is dropped. Without the fork point noted, the branch's
// tip, that earlier commit, would pass for its merge.
func TestCarriedOverAskedWorkerIsDroppedOnlyForItsOwnCommits(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		fork    bool // the state file keeps the fork point, as a run saves it now
		byHand  bool // A's worker commits and its branch is merged by hand between runs
		dropped bool
	}{
		{"fork noted", true, false, false},
		{"fork not noted", false, false, true},
		{"merged by hand", true, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.mem.commit("main", "A: an earlier attempt", "a0.txt") // A was reopened since
				cut := h.mem.Head(t.Context(), h.repo, "main")
				h.beads.add("A", "first", 1)
				h.worker("A", asksAndStops(func(string, string) {}))
				if o, code := h.run(); code != ExitOK {
					t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				s, err := project.LoadState(h.repo)
				if err != nil {
					t.Fatal(err)
				}
				if len(s.Workers) != 1 || s.Workers[0].Fork != cut {
					t.Fatalf("saved %+v; want A with the fork point %s", s.Workers, cut)
				}
				if a, _ := h.beads.Show(context.Background(), "A"); forkOf(a) != "" {
					t.Fatalf("A, asked, should have no %s: %s", ForkKey, a.Metadata)
				}
				if !c.fork {
					s.Workers[0].Fork = ""
					if err := project.SaveState(h.repo, s); err != nil {
						t.Fatal(err)
					}
				}
				if c.byHand {
					if err := h.mem.commitIn(h.worktree("A"), "A: add a.txt", "a.txt"); err != nil {
						t.Fatal(err)
					}
					if _, err := h.mem.FastForward(t.Context(), h.repo, "wt/A"); err != nil {
						t.Fatal(err)
					}
					h.mem.commit("main", "C: add c.txt", "c.txt")
				}
				h.beads.set("Q", "closed")
				h.beads.set("A", "closed")

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				if got := strings.Contains(ev, "A, carried over from the last run, is dropped: wt/A is on main already"); got != c.dropped {
					t.Errorf("A dropped as merged: %v, want %v:\n%s", got, c.dropped, ev)
				}
				if !c.dropped && !strings.Contains(ev, "A closed with no change") {
					t.Errorf("A should be taken up and closed with nothing to merge:\n%s", ev)
				}
			})
		})
	}
}
