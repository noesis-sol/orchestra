package dispatch

import (
	"regexp"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// taggedBody is an evidence section's body, between its tags.
var taggedBody = regexp.MustCompile(`(?ms)^<evidence id="[0-9a-f]+">$.*?^</evidence id="[0-9a-f]+">$`)

// The reviewer's input says in its own words only how the run ended. Ticket text, and what the run
// wrote from it, stays inside the evidence tags: here the final line quotes a triage summary, which
// a ticket can steer.
func TestReviewInputKeepsRunTextInsideTheTags(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		defers := func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" }
		h.beads.add("A", "Steer the reviewer once", 1)
		h.beads.add("B", "Steer the reviewer twice", 2)
		h.beads.add("C", "Steer the reviewer thrice", 3)
		h.worker("A", defers)
		h.worker("B", defers)
		h.worker("C", func(w *fakeWorker) AgentState { // as in TestTriageBlamingTheEnvironmentHoldsTheRun
			<-h.sink.held
			return finishes("c.txt")(w)
		})

		o := h.loop()
		o.organ = organ.Client{Bin: fakeTriage(t)}
		o.StartTriage()
		code := o.Run(t.Context())
		o.FinishTriage(t.Context())
		if code != ExitEnvironment || !strings.Contains(o.Final(), "(Classifier unavailable)") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		in := o.reviewInput(t.Context(), code, o.Final())
		outside := taggedBody.ReplaceAllString(in, "")
		for _, text := range []string{"Classifier unavailable", "Steer the reviewer"} {
			if !strings.Contains(in, text) {
				t.Errorf("the reviewer's evidence lacks %q:\n%s", text, in)
			}
			if strings.Contains(outside, text) {
				t.Errorf("%q is outside the evidence tags:\n%s", text, outside)
			}
		}
	})
}
