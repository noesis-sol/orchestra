package main

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// orchestra-74b2.16: quitting at once skips the loop's own saving of the workers it leaves for the
// next run, as it ends, so orchestra has it save them before it exits. A signal that only warns of
// a merge under way saves nothing: the loop goes on to its own saving.
func TestQuittingAtOnceSavesTheWorkersLeft(t *testing.T) {
	loop := &windingLoop{running: []dispatch.Status{{Ticket: "a-1", Tab: "w1:2"}},
		finishing: []dispatch.Finishing{{Ticket: "a-1", What: "merge"}}}
	r := newQuitRig(t, loop)
	r.stopLoop(os.Interrupt)
	r.stops.deliver(os.Interrupt) // warns of the merge
	if loop.saves != 0 {
		t.Errorf("saved %d times on the warning; want none", loop.saves)
	}
	r.stops.deliver(os.Interrupt)
	if len(r.exits) != 1 || loop.saves != 1 {
		t.Errorf("exits %v, saved %d times; want one of each", r.exits, loop.saves)
	}
}
