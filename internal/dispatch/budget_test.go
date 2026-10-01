package dispatch

import (
	"strings"
	"testing"
	"time"
)

// With a ticket limit, a worker's prompt says how long it has; without one, it says nothing of time.
func TestWorkerPromptStatesTheTicketLimit(t *testing.T) {
	t.Parallel()
	for _, limit := range []time.Duration{0, 2 * time.Hour} {
		h := newHarness(t)
		h.cfg.TicketLimit = limit
		h.beads.add("A", "first", 1)
		p := &prompted{got: map[string]string{}}
		h.worker("A", p.then(finishes("a.txt")))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("limit %s: exit %d, final %q\n%s", limit, code, o.Final(), h.sink.text())
		}
		got, said := p.got["A"], strings.Contains(p.got["A"], "You have about")
		switch {
		case limit == 0 && said:
			t.Errorf("no limit, yet the prompt states one:\n%s", got)
		case limit > 0 && !strings.Contains(got, "You have about 2h for this ticket"):
			t.Errorf("limit %s: the prompt doesn't state it:\n%s", limit, got)
		}
	}
}
