package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket whose worker has settled frees its slot as it goes to merge: the next ticket starts while
// it waits for its turn and merges. It keeps the rest of what a running ticket has: the tickets it
// blocks wait for its merge, and those overlapping its footprint for it to be done. Up to Concurrency
// tickets merge or wait to merge without a slot; beyond that, a settled worker keeps its slot until
// a merge ends.

// slowTabClose is Herdr taking a while to close ticket's tab, as its merge's last step: its merge
// holds the merge queue meanwhile, with the main repository let go of.
type slowTabClose struct {
	*fakeHerdr
	ticket string
	takes  time.Duration
}

func (s slowTabClose) CloseTab(ctx context.Context, tab string) error {
	if label, _, _ := s.TabLabel(ctx, tab); label == s.ticket {
		time.Sleep(s.takes)
	}
	return s.fakeHerdr.CloseTab(ctx, tab)
}

func TestASettledTicketFreesItsSlotAsItMerges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.commitFiles("internal/a.go")
		h.cfg.Concurrency = 2
		h.beads.add("B", "merges slowly", 1)
		h.beads.add("A", "settles while B merges", 2)
		h.beads.describe("A", "Change internal/a.go.")
		h.beads.add("X", "builds on A", 3)
		h.beads.link("X", "A", "blocks")
		h.beads.add("O", "overlaps A", 4)
		h.beads.describe("O", "Also change internal/a.go.")
		h.beads.add("C", "takes B's slot", 5)
		h.beads.add("D", "takes A's slot", 6)
		h.beads.add("E", "waits past the cap", 7)
		h.worker("B", finishes("b.txt"))
		h.worker("A", busyFor(time.Minute, "a.txt"))
		h.worker("C", busyFor(2*time.Minute, "c.txt")) // the third to merge: past the cap of 2
		h.worker("D", busyFor(3*time.Hour, "d.txt"))   // at work until long after the merges
		for _, id := range []string{"X", "O", "E"} {
			h.worker(id, finishes(id+".txt"))
		}
		o := h.loop()
		o.tabs = slowTabClose{fakeHerdr: h.herdr, ticket: "B", takes: time.Hour}
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 7 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		at := func(text string) int {
			t.Helper()
			i := strings.Index(ev, text)
			if i < 0 {
				t.Fatalf("events lack %q:\n%s", text, ev)
			}
			return i
		}
		before := func(first, then, why string) {
			t.Helper()
			if at(first) > at(then) {
				t.Errorf("%s: %q came after %q:\n%s", why, first, then, ev)
			}
		}
		before("B settled", "C dispatching", "C should take B's slot only once B's worker has settled")
		before("C dispatching", "B closed", "C should take B's slot while B merges")
		before("A settled", "D dispatching", "D should take A's slot only once A's worker has settled")
		before("D dispatching", "A closed", "D should take A's slot while A waits for B's merge")
		before("A closed", "X dispatching", "X, which A blocks, should wait for A's merge")
		before("X waits: waiting for A to merge", "A closed", "X should be said to wait for A's merge")
		before("A closed", "O dispatching", "O, overlapping A's footprint, should wait for A")
		before("skipping O: touches internal/a.go, like running A", "A closed", "O should be skipped for A")
		before("C settled", "B closed", "C should settle while B merges")
		before("B closed", "E dispatching", "E should wait for a merge to end, with B, A and C merging and D at work")
		if got := closedIDs(h); !equal(got[:3], []string{"B", "A", "C"}) {
			t.Errorf("merged %v, want B, A and C first, in the order their workers settled", got)
		}
	})
}
