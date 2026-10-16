package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket set aside for a failed check isn't checked again while its own merge is still under way
// (see recheckTickets).

// slowLabel is Notes whose AddLabel takes delay, as bd's does on a loaded machine, the first time
// ticket id is labelled UnmergedLabel, calling then first, after the delay.
type slowLabel struct {
	Notes
	id    string
	delay time.Duration
	then  func()
	once  sync.Once
}

func (n *slowLabel) AddLabel(ctx context.Context, id, label string) error {
	if id == n.id && label == UnmergedLabel {
		n.once.Do(func() {
			time.Sleep(n.delay)
			n.then()
		})
	}
	return n.Notes.AddLabel(ctx, id, label)
}

// orchestra-o93l: A's check failed on main, it was handed back to its worker and B merged
// meanwhile, and A's check failed again on the branch as first rebased. As A was set aside, still
// in bd labelling it, Run woke on its poll and took A up to be checked again, B having moved main
// on, with A's own merge still in flight: two goroutines for one ticket, the first's end erasing
// the second's slot and dashboard row, so the run could end under it. A is checked again only
// once its merge has returned.
func TestATicketIsNotCheckedAgainWhileItsOwnMergeRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = fmt.Sprintf(`echo run >> '%s'/"$(basename "$PWD")".runs; test "$(basename "$PWD")" != A`, dir)
		h.cfg.CheckHandBacks = 1
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", landsMeanwhile("a.txt"), func(w *fakeWorker) AgentState {
			time.Sleep(10 * time.Minute) // B merges meanwhile
			return commitsFix(dir, "try1.txt", false)(w)
		}, commitsFix(dir, "try2.txt", false)) // as its check fails once more
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute) // A's check is handed back meanwhile
			return finishes("b.txt")(w)
		})
		o := h.loop()
		o.notes = &slowLabel{Notes: o.notes, id: "A", delay: 2 * readyPoll, then: func() {
			if strings.Contains(h.sink.text(), "RECHECK: A") {
				t.Error("A was taken up to be checked again while its own merge was still under way")
			}
		}}
		code := o.Run(context.Background())
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		failed, recheck := strings.Index(ev, "CHECKS_FAILED: A"), strings.Index(ev, "RECHECK: A")
		if failed < 0 || recheck < failed {
			t.Errorf("want A set aside, then checked again:\n%s", ev)
		}
		if n := checkRuns(t, dir, "A"); n != 4 {
			t.Errorf("A's check ran %d times, want 4: before and after its fix, as it merged and as it was checked once more\n%s", n, ev)
		}
		if got := closedIDs(h); !equal(got, []string{"B"}) {
			t.Errorf("merged %v, want B", got)
		}
		if aside := o.asideList(); !equal(aside, []string{"A"}) {
			t.Errorf("set aside %v, want A", aside)
		}
	})
}
