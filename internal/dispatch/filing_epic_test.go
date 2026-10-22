package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The scope's epic stays open while the scope is being filed, though the first ticket, its only
// subticket so far, has merged: the rest of its tree is still to be filed under it.

// specFilingHarness is a timed run of the spec tree R as a first breakdown files it: the epic R and
// its environment ticket R.1 alone, the rest of the tree to be filed before done is closed.
func specFilingHarness(t *testing.T) (h *harness, done chan struct{}) {
	t.Helper()
	h = scoped(newTimedHarness(t), "R")
	h.cfg.Concurrency = 2
	h.beads.epic("R", "")
	h.beads.sub("R.1", "R", "set up the project", 0, SoloLabel)
	done = make(chan struct{})
	h.cfg.Filing = &Filing{First: "R.1", Done: done}
	return h, done
}

// The environment ticket merges long before the breakdown has filed the rest: R stays open, and
// closes only once all the tickets filed under it have merged.
func TestFilingKeepsTheScopeEpicOpen(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, done := specFilingHarness(t)
		h.worker("R.1", finishes("setup.txt"))
		h.worker("A", finishes("a.txt"))
		h.worker("B", slowly(time.Hour, "b.txt"))
		go func() {
			time.Sleep(20 * time.Minute) // the breakdown goes on long after R.1 has merged
			if st, _ := h.beads.Status(context.Background(), "R"); st == StatusClosed {
				t.Errorf("R closed while its tree was being filed\n%s", h.sink.text())
			}
			h.beads.sub("A", "R", "filed later", 1)
			h.beads.sub("B", "R", "filed last", 2)
			close(done)
		}()
		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 3 tickets") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if st, _ := h.beads.Status(context.Background(), "R"); st != StatusClosed {
			t.Fatalf("R is %s, want closed\n%s", st, h.sink.text())
		}
		if got, want := h.beads.notesOf("R"), "closed: All 3 subtickets are merged; closed by orchestra after B"; got != want {
			t.Errorf("R's notes %q, want %q", got, want)
		}
		ev := h.sink.text()
		if strings.Count(ev, "closed epic R") != 1 || !before(ev, "B closed (", "closed epic R") {
			t.Errorf("R should close once, after B merged:\n%s", ev)
		}
	})
}

// A filing that adds nothing to the first ticket, merged before it is over, closes the epic as it
// ends.
func TestFilingOverClosesTheScopeEpicItsFirstTicketMerged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, done := specFilingHarness(t)
		h.worker("R.1", finishes("setup.txt"))
		go func() {
			time.Sleep(20 * time.Minute)
			close(done)
		}()
		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 1 ticket") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got, want := h.beads.notesOf("R"), "closed: Its 1 subticket is merged; closed by orchestra after R.1"; got != want {
			t.Errorf("R's notes %q, want %q\n%s", got, want, h.sink.text())
		}
		ev := h.sink.text()
		if !before(ev, "  the rest of R is filed: its tickets join the run", "closed epic R: its 1 subticket is merged") {
			t.Errorf("R should close once the filing is over:\n%s", ev)
		}
	})
}
