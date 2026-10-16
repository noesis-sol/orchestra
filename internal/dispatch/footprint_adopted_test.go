package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The footprint of a ticket whose worker the next run adopts as it starts names the repository's
// files, as a ready ticket's does: a ticket naming the same file by another part of its path waits
// for the adopted worker, rather than running beside it.
func TestAdoptedTicketsFootprintIsResolvedInTheRepository(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.commitFiles("internal/dispatch/loop.go")
		h.cfg.Concurrency = 2
		h.cfg.TicketLimit = 30 * time.Minute
		h.beads.add("A", "fix the loop", 1)
		h.beads.describe("A", "Fix loop.go.")
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			w.claim()
			time.Sleep(45 * time.Minute) // past the limit, and well within it again once adopted
			return finishes("a.txt")(w)
		}))
		if o, code := h.run(); code != ExitStuck || !strings.HasPrefix(o.Final(), "TICKET_LIMIT: A still working") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}

		h.beads.add("B", "change the loop", 2)
		h.beads.describe("B", "Change internal/dispatch/loop.go.")
		h.worker("B", v.runs("B", finishes("b.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := v.of("B"); slices.Contains(got, "A") {
			t.Errorf("B ran beside A's adopted worker, whose ticket names the file it names")
		}
		log := h.logged()
		for _, want := range []string{
			"A footprint: internal/dispatch/loop.go\n",
			"skipping B: touches internal/dispatch/loop.go, like running A",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("the log lacks %q:\n%s", want, log)
			}
		}
	})
}
