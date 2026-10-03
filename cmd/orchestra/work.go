package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A run in a terminal that nothing has told what to work on asks first: the current tickets, as a
// run always did, or a new feature, which the user describes and then talks through with claude,
// which files its tickets (interview.go). It asks once the startup checks have passed and the run
// lock is held, so that an answer is never followed by "orchestra cannot start".

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
	// nothing says, when none is ready, why the run has nothing to run, or nil when it has (see
	// dispatch.CheckNothingToRun): the current tickets' option says so
	nothing  func(ctx context.Context) (*dispatch.NothingToRun, error)
	in       io.Reader
	out, err io.Writer
}

// askWork asks what the run c works on (see asksWork), reading the answers from stdin and drawing
// the form on stdout. It returns the new feature's description, "" for the current tickets, and
// ExitOK; or "" and the exit code when the run is not to go on, having said why. Whether the
// current tickets have anything to run is for the run to check once they are picked.
func askWork(
	ctx context.Context, stops *stopWatch, c options, stdin io.Reader, stdout, stderr io.Writer,
) (string, int) {
	// A stop signal ends the question as Ctrl+C does; until it is answered, nothing has changed.
	ctx, stop := stops.context(ctx)
	defer stop()
	tracker := beads.Tracker{Repo: c.Repo, ExcludeTypes: c.ExcludeTypes}
	return workQuestion{
		tickets: tracker,
		nothing: func(ctx context.Context) (*dispatch.NothingToRun, error) {
			return dispatch.CheckNothingToRun(ctx, c.Config, tracker, git.Git{}, git.Git{})
		},
		in:  stdin,
		out: stdout,
		err: stderr,
	}.ask(ctx)
}

func (q workQuestion) ask(ctx context.Context) (string, int) {
	ready := -1 // bd can't say
	if ts, err := q.tickets.Ready(ctx, ""); err == nil {
		ready = len(ts)
	}
	var nothing *dispatch.NothingToRun // with none ready, the run has something only from the last run's workers
	if ready == 0 {
		var err error
		if nothing, err = q.nothing(ctx); err != nil {
			ready = -1 // bd can't say why none is
		}
	}
	description, err := tui.AskWork(ctx, q.in, q.out, ready, nothing)
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
