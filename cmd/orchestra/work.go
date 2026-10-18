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
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A run in a terminal that nothing has told what to work on asks first: the current tickets, as a
// run always did; a spec on this machine, run as --spec runs it (spec.go), refined with claude first
// if asked, as --refine has it; or a new feature, which the user describes and then talks through
// with claude, which files its tickets (feature_refine.go). It asks once the startup checks have passed
// and the run lock is held, so that an answer is never followed by "orchestra cannot start", but for
// a spec git can't commit where it would be kept.

// asksWork reports whether the run c asks what to work on. terminal says whether stdin and stdout
// are both a terminal, as the form is drawn on stdout. --feature, --spec (or ORCHESTRA_SPEC), --ticket
// (or ORCHESTRA_TICKET), --tickets (or ORCHESTRA_TICKETS=1) and --plain say already. So does a run
// whose --limit is reached: it starts nothing, and a feature would be filed with none of it run.
func asksWork(c options, terminal bool) bool {
	return terminal && c.Feature == "" && c.specFile == "" && c.Ticket == "" && !c.Tickets && !c.Plain &&
		c.DoneSoFar < c.Limit
}

// workQuestion is the question a run asks first.
type workQuestion struct {
	tickets interface { // the run's ready query, for the number of current tickets
		Ready(ctx context.Context, scope string) ([]dispatch.Ticket, error)
	}
	// nothing says why the run has nothing to run, none ready or every ready ticket held back, or nil
	// when it has (see dispatch.CheckNothingToRun): the current tickets' option says so
	nothing  func(ctx context.Context) (*dispatch.NothingToRun, error)
	repo     string // the main checkout, where a spec's relative path starts
	in       io.Reader
	out, err io.Writer
}

// askWork asks what the run c works on (see asksWork), reading the answers from stdin and drawing
// the form on stdout. It returns the answer and ExitOK; or the exit code when the run is not to go
// on, having said why. Whether the current tickets have anything to run is for the run to check once
// they are picked.
func askWork(
	ctx context.Context, stops *stopWatch, c options, stdin io.Reader, stdout, stderr io.Writer,
) (tui.Work, int) {
	// A stop signal ends the question as Ctrl+C does; until it is answered, nothing has changed.
	ctx, stop := stops.context(ctx)
	defer stop()
	tracker := beads.Tracker{Repo: c.Repo, ExcludeTypes: c.ExcludeTypes}
	return workQuestion{
		tickets: tracker,
		nothing: func(ctx context.Context) (*dispatch.NothingToRun, error) {
			return dispatch.CheckNothingToRun(ctx, c.Config, tracker, git.Git{}, git.Git{})
		},
		repo: c.Repo,
		in:   stdin,
		out:  stdout,
		err:  stderr,
	}.ask(ctx)
}

func (q workQuestion) ask(ctx context.Context) (tui.Work, int) {
	ready := -1 // bd can't say
	if ts, err := q.tickets.Ready(ctx, ""); err == nil {
		ready = len(ts)
	}
	// Ready tickets may all be held back (parents waiting for their subtickets, tickets waiting for a
	// blocker to merge), and with none, the run has something only from the last run's workers.
	var nothing *dispatch.NothingToRun
	if ready >= 0 {
		var err error
		if nothing, err = q.nothing(ctx); err != nil && ready == 0 {
			ready = -1 // bd can't say why none is
		}
	}
	work, err := tui.AskWork(ctx, q.in, q.out, q.repo, ready, nothing)
	switch {
	case ctx.Err() != nil || errors.Is(err, tui.ErrCancelled):
		fmt.Fprintln(q.err, "orchestra: stopped before the run started; nothing was changed.")
		return tui.Work{}, dispatch.ExitInterrupted
	case err != nil:
		fmt.Fprintf(q.err, "orchestra couldn't ask what to work on: %v. Say it with --tickets, "+
			"with --spec <file>, or with --feature \"<request>\"\n", err)
		return tui.Work{}, dispatch.ExitSetup
	}
	// The form is gone from the screen: what was chosen stays in sight, to run again should it not run.
	switch work.Kind {
	case tui.WorkFeature:
		fmt.Fprintln(q.out, "New feature:")
		for line := range strings.Lines(work.Description) {
			fmt.Fprintln(q.out, "  "+strings.TrimRight(line, "\n"))
		}
	case tui.WorkSpec:
		how := "run as it is"
		if work.Refine {
			how = "refined with claude first"
		}
		fmt.Fprintf(q.out, "Spec: %s, %s\n", tui.Printable(work.Spec), how)
	}
	return work, dispatch.ExitOK
}

// specChosenFrom is what gave the run its spec, for the spec's problems, when it was chosen at the
// question rather than given with --spec or ORCHESTRA_SPEC.
const specChosenFrom = "chosen at the start"

// plannedDefaults gives the run c, on a spec or a feature chosen at the question, what --spec or
// --feature would have: no ticket limit unless --limit or LIMIT gives one, and workers in auto
// permission mode unless the flag, its variable or the project's settings give one.
func plannedDefaults(c *options) {
	if !c.limitGiven {
		c.Limit = dispatch.NoLimit
	}
	// The mode given, checked already, kept; else a planned run's, as no one answers a permission prompt.
	if mode, err := project.ResolvePermissionMode(c.WorkerPermissionMode, project.Settings{}, true); err == nil {
		c.WorkerPermissionMode = mode
	}
}

// featureChosen has the run s run the feature typed at the question, work, as if --feature had
// given it: with a planned run's defaults (plannedDefaults), and the feature in the run lock from
// now on, as --feature's is.
func (s *session) featureChosen(work tui.Work) {
	s.cfg.Feature = work.Description
	plannedDefaults(&s.cfg)
	s.holder.Feature = work.Description
	if err := s.lock.Rewrite(s.holder); err != nil {
		s.log.Raw("", fmt.Errorf("cannot record the feature in the run lock: %w", err))
	}
}

// specChosen has the run s run the spec chosen at the question, work, as if --spec (and --refine)
// had given it: with a planned run's defaults (plannedDefaults), and the spec's path in the
// repository (Config.Spec), which the run lock records from now on. A spec git can't commit where
// it would be kept (see checkSpecPath) ends the run before it starts, having said why. It returns
// the exit code.
func (s *session) specChosen(ctx context.Context, work tui.Work) int {
	c := &s.cfg
	c.specFile, c.Refine = work.Spec, work.Refine
	if problems := checkSpecPath(ctx, c, specChosenFrom); len(problems) > 0 {
		fmt.Fprintln(s.e.stderr, "orchestra cannot run that spec:")
		for _, p := range problems {
			fmt.Fprintln(s.e.stderr, "  - "+p)
		}
		return dispatch.ExitSetup
	}
	plannedDefaults(c)
	s.holder.Spec = c.Spec
	if err := s.lock.Rewrite(s.holder); err != nil {
		s.log.Raw("", fmt.Errorf("cannot record the spec in the run lock: %w", err))
	}
	return dispatch.ExitOK
}
