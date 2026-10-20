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
// run always did; a spec on this machine, run as --spec runs it (spec.go), refined with claude first
// if asked, as --refine has it; or a new feature, which the user describes and then talks through
// with claude, which files its tickets (feature_refine.go). It asks once the startup checks have passed
// and the run lock is held, so that an answer is never followed by "orchestra cannot start", but for
// a spec git can't commit where it would be kept.

// workKind is what a run works on, as its flags or the start question say.
type workKind int

const (
	workUnsaid  workKind = iota // nothing said: the current tickets, unless the run asks (asksWork)
	workTickets                 // the current tickets (--tickets, ORCHESTRA_TICKETS=1, or the question)
	workTicket                  // one ticket and its subtickets (--ticket, ORCHESTRA_TICKET)
	workSpec                    // what a spec describes, broken down first (--spec, ORCHESTRA_SPEC, or the question)
	workFeature                 // a feature request, talked through and filed first (--feature, or the question)
)

// work is what a run works on, its kind and what that kind needs. The flags keep it in the options'
// fields they are bound to (specFile, Refine, Feature, Ticket and Tickets), which only work reads for
// it, and putWork writes for the question's answer; the rest of the run asks work. Ticket is also the
// loop's scope, a planned run's epic once filed.
type work struct {
	kind    workKind
	ticket  string // workTicket's ticket
	spec    string // workSpec's file, as given, made absolute once checkSpecFile has checked it
	refine  bool   // workSpec's: talk the spec through with the user before it is broken down (--refine)
	feature string // workFeature's request
}

// planned reports whether the run plans its tickets before it runs them, filing an epic from a spec
// or a feature request, and is then scoped to that epic.
func (w work) planned() bool { return w.kind == workSpec || w.kind == workFeature }

// work is what the run c works on. Where its flags give more than one (a setup problem, see
// checkWorkFlags), a spec comes first, then a feature, then a ticket. A planned run's Ticket, once it
// has filed its epic, is its scope, and the run still works on the spec or the feature.
func (c options) work() work {
	switch {
	case c.specFile != "":
		return work{kind: workSpec, spec: c.specFile, refine: c.Refine}
	case c.Feature != "":
		return work{kind: workFeature, feature: c.Feature}
	case c.Ticket != "":
		return work{kind: workTicket, ticket: c.Ticket}
	case c.Tickets:
		return work{kind: workTickets}
	}
	return work{}
}

// putWork has the run c work on w, in the fields its flags would have set for it.
func (c *options) putWork(w work) {
	c.specFile, c.Refine, c.Feature, c.Ticket = w.spec, w.refine, w.feature, w.ticket
	c.Tickets = w.kind == workTickets
}

// answered is the work the start question's answer a chose.
func answered(a tui.Work) work {
	switch a.Kind {
	case tui.WorkSpec:
		return work{kind: workSpec, spec: a.Spec, refine: a.Refine}
	case tui.WorkFeature:
		return work{kind: workFeature, feature: a.Description}
	}
	return work{kind: workTickets}
}

// settleWork resolves the settings of the run c that depend on what it works on: workers start in
// the permission mode resolved for a planned run, or for one that isn't (see resolveSettings).
func (c *options) settleWork() {
	c.WorkerPermissionMode = c.permissionModes.unplanned
	if c.work().planned() {
		c.WorkerPermissionMode = c.permissionModes.planned
	}
}

// givenMode is a way a run's flags and variables say what it works on, for checkWorkFlags.
type givenMode struct {
	kind   workKind
	name   string // as the run's problems name it: --spec, or ORCHESTRA_SPEC, as given
	runs   string // what it runs, after "one runs"
	run    string // a planned run, as the run's problems call it; "" for the others
	scope  string // what a planned run is scoped to
	before string // what a planned run does before it runs any ticket
}

// modesGiven lists what the flags and variables of f say the run works on, a spec first, then a
// feature, a ticket and the current tickets. --feature counts as given even empty; ORCHESTRA_TICKETS=1
// doesn't, as a standing choice that whatever else is given overrides.
func (f *runFlags) modesGiven() []givenMode {
	var given []givenMode
	if f.specFile != "" {
		given = append(given, givenMode{kind: workSpec, name: f.specFrom(), runs: "a spec", run: "a spec run",
			scope: "the epic its breakdown files", before: "break the spec down"})
	}
	if f.set["feature"] {
		given = append(given, givenMode{kind: workFeature, name: "--feature", runs: "a feature request",
			run: "a feature run", scope: "the epic it files", before: "file a plan"})
	}
	if f.Ticket != "" {
		given = append(given, givenMode{kind: workTicket, name: "--ticket (or ORCHESTRA_TICKET)"})
	}
	if f.set["tickets"] && f.Tickets {
		given = append(given, givenMode{kind: workTickets, name: "--tickets", runs: "the current tickets"})
	}
	return given
}

// checkWorkFlags says how the flags and variables of f don't say what the run works on, or returns
// "": --spec and --feature without what they need, a planned run with anything else, which --ticket
// with --tickets is not (it is the ticket, both saying the run doesn't ask), --refine with neither
// (ORCHESTRA_REFINE alone, like ORCHESTRA_TICKETS, is a standing choice that a run without a spec
// leaves unused; --refine with --feature changes nothing, as a feature is always talked through), and
// a planned run whose --limit is reached.
func checkWorkFlags(f *runFlags) string {
	if f.set["spec"] && f.specFile == "" {
		return "--spec needs the spec's file: orchestra --spec <file>"
	}
	given := f.modesGiven()
	if len(given) > 1 && given[0].run != "" {
		first, then := given[0], given[1]
		why := "one runs " + first.runs + ", the other " + then.runs + "."
		if then.kind == workTicket {
			why = first.run + " is scoped to " + first.scope + "."
		}
		return first.name + " can't be combined with " + then.name + ": " + why
	}
	switch {
	case f.set["feature"] && f.Feature == "":
		return "--feature needs the request: orchestra --feature \"<what to build>\""
	case (len(given) == 0 || given[0].run == "") && f.set["refine"] && f.Refine:
		return "--refine only applies with --spec or --feature."
	case len(given) == 1 && given[0].run != "" && f.Limit >= 0 && f.DoneSoFar >= f.Limit:
		// Only with --limit or LIMIT: the loop would end at once, the plan filed and none of it started.
		return fmt.Sprintf("%s would %s and run none of it: --done-so-far (%d) has reached --limit (%d). "+
			"Raise --limit [LIMIT], lower --done-so-far [DONE_SO_FAR], or leave both out.",
			given[0].name, given[0].before, f.DoneSoFar, f.Limit)
	}
	return ""
}

// asksWork reports whether the run c asks what to work on. terminal says whether stdin and stdout
// are both a terminal, as the form is drawn on stdout. --feature, --spec (or ORCHESTRA_SPEC), --ticket
// (or ORCHESTRA_TICKET), --tickets (or ORCHESTRA_TICKETS=1) and --plain say already. So does a run
// whose --limit is reached: it starts nothing, and a feature would be filed with none of it run.
func asksWork(c options, terminal bool) bool {
	return terminal && c.work().kind == workUnsaid && !c.Plain && c.DoneSoFar < c.Limit
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
	termName string // the terminal out is, as TERM names it
}

// askWork asks what the run c works on (see asksWork), reading the answers from stdin and drawing
// the form on stdout, the terminal TERM names termName. It returns the answer and ExitOK; or the exit
// code when the run is not to go on, having said why. Whether the current tickets have anything to
// run is for the run to check once they are picked.
func askWork(
	ctx context.Context, stops *stopWatch, c options, stdin io.Reader, stdout, stderr io.Writer, termName string,
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
		repo:     c.Repo,
		in:       stdin,
		out:      stdout,
		err:      stderr,
		termName: termName,
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
	work, err := tui.AskWork(ctx, q.in, q.out, q.termName, q.repo, ready, nothing)
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

// featureChosen has the run s run the feature typed at the question, answer, as if --feature had
// given it (see setWork).
func (s *session) featureChosen(answer tui.Work) {
	// A feature has no path to check, so setWork runs no command and can't fail.
	_ = s.setWork(context.Background(), answered(answer))
}

// specChosen has the run s run the spec chosen at the question, answer, as if --spec (and --refine)
// had given it (see setWork). It returns the exit code.
func (s *session) specChosen(ctx context.Context, answer tui.Work) int {
	return s.setWork(ctx, answered(answer))
}

// setWork has the run s work on w, chosen at the question, as if its flags had given it: with the
// settings that depend on it (settleWork), and a spec's or a feature's in the run lock from now on,
// as --spec's and --feature's are, the spec by its path in the repository (Config.Spec). A spec git
// can't commit where it would be kept (see checkSpecPath) ends the run before it starts, having
// said why. It returns the exit code.
func (s *session) setWork(ctx context.Context, w work) int {
	c := &s.cfg
	c.putWork(w)
	if problems := checkSpecPath(ctx, c, specChosenFrom); len(problems) > 0 {
		fmt.Fprintln(s.e.stderr, "orchestra cannot run that spec:")
		for _, p := range problems {
			fmt.Fprintln(s.e.stderr, "  - "+p)
		}
		return dispatch.ExitSetup
	}
	if !w.planned() { // the current tickets: the settings resolved and the run lock are theirs already
		return dispatch.ExitOK
	}
	c.settleWork()
	s.holder.Ticket, s.holder.Feature, s.holder.Spec = w.ticket, w.feature, c.Spec
	if err := s.lock.Rewrite(s.holder); err != nil {
		s.log.Raw("", fmt.Errorf("cannot record what the run works on in the run lock: %w", err))
	}
	return dispatch.ExitOK
}
