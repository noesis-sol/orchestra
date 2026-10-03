package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A run in a terminal that nothing has told what to work on asks first: the current tickets, as a
// run always did, or a new feature, which the user describes and orchestra plans into tickets as
// with --feature. It asks once the startup checks have passed and the run lock is held, so that an
// answer is never followed by "orchestra cannot start".

// asksWork reports whether the run c asks what to work on. terminal says whether stdin and stdout
// are both a terminal, as the form is drawn on stdout. --feature, --ticket (or ORCHESTRA_TICKET),
// --tickets (or ORCHESTRA_TICKETS=1) and -plain say already. So does a run whose -limit is reached:
// it starts nothing, and a feature would be filed with none of it run.
func asksWork(c options, terminal bool) bool {
	return terminal && c.Feature == "" && c.Ticket == "" && !c.Tickets && !c.Plain && c.DoneSoFar < c.Limit
}

// workQuestion is the question a run asks first.
type workQuestion struct {
	tickets interface { // the run's ready query, for the number of current tickets
		Ready(ctx context.Context, scope string) ([]dispatch.Ticket, error)
	}
	in       io.Reader
	out, err io.Writer
}

// askWork asks what the run c works on (see asksWork), reading the answers from stdin and drawing
// the form on stdout. It returns the new feature's description, "" for the current tickets, and
// ExitOK; or "" and the exit code when there is nothing to run, having said why.
func askWork(
	ctx context.Context, stops *stopWatch, c options, stdin io.Reader, stdout, stderr io.Writer,
) (string, int) {
	// A stop signal ends the question as Ctrl+C does; until it is answered, nothing has changed.
	ctx, stop := stops.context(ctx)
	defer stop()
	return workQuestion{
		tickets: beads.Tracker{Repo: c.Repo, ExcludeTypes: c.ExcludeTypes},
		in:      stdin,
		out:     stdout,
		err:     stderr,
	}.ask(ctx)
}

func (q workQuestion) ask(ctx context.Context) (string, int) {
	ready := -1 // bd can't say
	if ts, err := q.tickets.Ready(ctx, ""); err == nil {
		ready = len(ts)
	}
	description, err := tui.AskWork(ctx, q.in, q.out, ready)
	switch {
	case ctx.Err() != nil || errors.Is(err, tui.ErrCancelled):
		fmt.Fprintln(q.err, "orchestra: stopped before the run started; nothing was changed.")
		return "", dispatch.ExitInterrupted
	case err != nil:
		fmt.Fprintf(q.err, "orchestra couldn't ask what to work on: %v. Say it with --tickets, "+
			"or with --feature \"<request>\"\n", err)
		return "", dispatch.ExitSetup
	case description != "":
		// The form is gone from the screen: the request stays in sight, to run again if it isn't planned.
		fmt.Fprintln(q.out, "New feature:")
		for line := range strings.Lines(description) {
			fmt.Fprintln(q.out, "  "+strings.TrimRight(line, "\n"))
		}
	}
	return description, dispatch.ExitOK
}
