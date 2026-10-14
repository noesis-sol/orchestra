package dispatch

import (
	"context"
	"strings"
	"testing"
)

// A ticket left unmerged whose branch is squash-merged by hand and kept is merged in the next run:
// main holds the branch's changes and a commit since names the ticket, though the branch isn't on
// main. A commit naming it whose changes aren't the branch's is not its merge.
func TestUnmergedBranchSquashMergedByHandIsMerged(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		byHand func(h *harness)
		merged bool
	}{
		{"squash-merged", func(h *harness) {
			h.git(h.repo, "merge", "-q", "--squash", "wt/A")
			h.git(h.repo, "commit", "-q", "-m", "A: add a.txt, squashed")
		}, true},
		{"only named", func(h *harness) { mentionOnMain(h, "other.txt", "A") }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.beads.add("A", "first", 1)
			h.beads.add("B", "second", 2)
			h.beads.link("B", "A", "blocks")
			h.worker("A", leavesUncommitted("a.txt", ".gitignore"))
			h.worker("B", finishes("b.txt"))
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if a, _ := h.beads.Show(context.Background(), "A"); !HasLabel(a, UnmergedLabel) {
				t.Fatalf("run 1 should leave A unmerged:\n%s", h.sink.text())
			}
			c.byHand(h)
			mentionOnMain(h, "later.txt", "C")
			if n := h.checkNothing(); (n == nil) != c.merged {
				t.Errorf("before run 2, the check found %+v, want B to run: %v", n, c.merged)
			}

			o, code := h.run()
			if code != ExitOK {
				t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			ev := h.sink.text()
			if got := strings.Contains(ev, "A, left unmerged by an earlier run, is on main now"); got != c.merged {
				t.Errorf("run 2 found A merged: %v, want %v:\n%s", got, c.merged, ev)
			}
			if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) == c.merged {
				t.Errorf("A's labels %v, want its %q label kept: %v", a.Labels, UnmergedLabel, !c.merged)
			}
			log := h.mainLog()
			if got := strings.Contains(log, "B: add b.txt"); got != c.merged {
				t.Errorf("B merged: %v, want %v:\n%s", got, c.merged, log)
			}
		})
	}
}
