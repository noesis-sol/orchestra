package dispatch

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

// A talk that ends while the ticket an answer freed runs puts its questions to the maintainer all the
// same: when the queue runs dry again, the run opens no talk on the question left open there, and ends
// naming it.
func TestTalkOverWhileAFreedTicketRunsIsNotReopened(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.beads.add("QA", "which way for A?", 1, HumanLabel)
		h.beads.add("QB", "which way for B?", 1, HumanLabel)
		h.beads.link("A", "QA", "blocks")
		h.beads.link("B", "QB", "blocks")
		h.worker("A", busyFor(time.Hour, "a.txt")) // the talk ends while A runs
		answers := &fakeAnswers{beads: h.beads, closeAfter: 10 * time.Minute, maintain: func([]Question) {
			time.Sleep(time.Minute)
			h.beads.set("QA", StatusClosed) // QB is left open
		}}
		o := h.loop()
		o.SetAnswers(answers)
		code := o.Run(context.Background())
		answers.done()
		want := "READY_EMPTY after 1 tickets; QUESTIONS_OPEN: QB (holding B): answer with orchestra answer, then run again"
		if code != ExitOK || o.Final() != want {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
		}
		if n := len(answers.talks); n != 1 {
			t.Errorf("%d talks, want 1: QB was put to the maintainer in the first\n%s", n, h.sink.text())
		}
	})
}
