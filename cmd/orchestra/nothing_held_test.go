package main

import (
	"context"
	"errors"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Ready tickets the run would hold back from its start are none to run: the current tickets'
// option says none is ready, with how many are open. When the check can't say, the option counts
// what bd ready lists.
func TestWorkQuestionCountsHeldTicketsAsNoneReady(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ready  fakeReady
		option string
	}{
		{"all held", fakeReady{n: 2, nothing: &dispatch.NothingToRun{HeldParents: 1, HeldBlocked: 1, Deferred: 1}},
			"> Current tickets: none ready (3 open)"},
		{"check failed", fakeReady{n: 2, checkErr: errors.New("bd show: database is locked")},
			"> Current tickets: 2 ready"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			term, answer := askWorkOn(context.Background(), t, tc.ready)
			term.waitFor(t, tc.option)
			term.typeKeys(t, keyEnter, true)
			if a := answer(); a.description != "" || a.code != dispatch.ExitOK || a.err != "" {
				t.Errorf("got %q, exit %d, stderr %q", a.description, a.code, a.err)
			}
		})
	}
}
