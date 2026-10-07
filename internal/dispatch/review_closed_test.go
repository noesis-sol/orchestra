package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// closesWith claims the ticket, commits file unless it is "", and closes it with reason, as bd close
// --reason does.
func closesWith(file, reason string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		if file != "" {
			w.commit(file)
		}
		w.beads.mu.Lock()
		w.beads.tickets[w.id].CloseReason = reason
		w.beads.mu.Unlock()
		w.close()
		return "idle"
	}
}

// The reviewer is given each ticket closed in the run, merged or with no change to merge, with its
// title and its close reason cut short, so it can tell a ticket that closed as it meant to from one
// that needs the maintainer. orchestra's own request still ends the input, after the evidence.
func TestReviewInputGivesTheClosedTicketsCloseReasons(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		long := "Measured it: nothing needed changing. " + strings.Repeat("More detail. ", 100)
		h.beads.add("A", "Watch for a problem and fix it if needed", 1)
		h.beads.add("B", "Cap the subagents if workers overuse them", 2)
		h.worker("A", closesWith("a.txt", "The rewording didn't help; the schema change did."))
		h.worker("B", closesWith("", long))

		o := h.loop()
		code := o.Run(t.Context())
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "B closed with no change") ||
			!strings.Contains(ev, "A closed (") {
			t.Fatalf("want A merged and B closed with no change:\n%s", ev)
		}
		in := o.reviewInput(t.Context(), code, o.Final())
		_, section, ok := strings.Cut(in, "## Tickets closed in this run")
		if !ok {
			t.Fatalf("no section of closed tickets:\n%s", in)
		}
		section, _, _ = strings.Cut(section, "\n## ")
		for _, want := range []string{
			"A: Watch for a problem and fix it if needed\nClose reason: The rewording didn't help; the schema change did.",
			"B: Cap the subagents if workers overuse them\nClose reason: Measured it: nothing needed changing. More detail.",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("the closed tickets' section lacks %q:\n%s", want, section)
			}
		}
		if strings.Contains(section, long) || !strings.Contains(section, "…") {
			t.Errorf("B's long close reason should be cut short:\n%s", section)
		}
		if outside := taggedBody.ReplaceAllString(section, ""); strings.Contains(outside, "Close reason") {
			t.Errorf("close reasons are outside the evidence tags:\n%s", outside)
		}
		if !strings.HasSuffix(in, "Write its report.\n") {
			t.Errorf("the input should end with orchestra's request:\n%s", in[max(0, len(in)-300):])
		}
	})
}
