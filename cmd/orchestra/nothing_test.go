package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The current tickets' option says how many are ready, or, with none, why: everything is done, or
// how many tickets are left that aren't ready. Workers carried over from the last run are something
// to run, so the count stays; where bd can't say, the option gives no count. Either way, picking it
// returns to the run, which checks again.
func TestWorkQuestionSaysWhyNoneIsReady(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ready  fakeReady
		option string
	}{
		{"all done", fakeReady{nothing: &dispatch.NothingToRun{AllDone: true, StillOpen: []string{"k-e"}}},
			"> Current tickets: none, all done"},
		{"none ready", fakeReady{nothing: &dispatch.NothingToRun{Questions: 1, Waiting: 2, Deferred: 1, Unmerged: 3}},
			"> Current tickets: none ready (4 open)"},
		{"workers carried over", fakeReady{}, "> Current tickets: 0 ready"},
		{"some ready", fakeReady{n: 2, checkErr: errors.New("not asked")}, "> Current tickets: 2 ready"},
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

func TestWorkQuestionCountsNothingWhenTheCheckCantSay(t *testing.T) {
	term, answer := askWorkOn(context.Background(), t, fakeReady{checkErr: errors.New("bd: database is locked")})
	term.waitFor(t, "> Current tickets")
	term.typeKeys(t, keyEnter, true)
	a := answer()
	if a.description != "" || a.code != dispatch.ExitOK {
		t.Errorf("got %q, exit %d", a.description, a.code)
	}
	if strings.Contains(a.out, "Current tickets:") {
		t.Errorf("want the option without a count:\n%s", a.out)
	}
}
