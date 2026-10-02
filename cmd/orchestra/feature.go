package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// A feature run (--feature) takes a request from idea to a scoped run: the screen organ judges the
// request, the plan organ turns it into an epic and its tickets, the maintainer confirms the plan,
// and orchestra files it with bd and runs the epic as --ticket would. Nothing is filed until the
// plan is confirmed; the run's startup checks have passed by then, so a run that couldn't start
// files nothing either.

// featureOrgans is what a feature run asks of the organs.
type featureOrgans interface {
	Screen(ctx context.Context, r organ.Request) (organ.Screening, error)
	PlanFeature(ctx context.Context, ev organ.FeatureEvidence) (organ.FeaturePlan, error)
}

// featureTracker is what a feature run reads from and writes to Beads.
type featureTracker interface {
	Unclosed(ctx context.Context) ([]dispatch.Ticket, error)
	Create(ctx context.Context, t beads.NewTicket) (string, error)
	AddBlock(ctx context.Context, blocker, blocked string) error
}

// featureRun is one --feature request on its way to a filed epic.
type featureRun struct {
	request  string
	repo     string
	yes      bool // file without asking
	terminal bool // in and out are a terminal to ask on
	organs   featureOrgans
	tracker  featureTracker
	log      *dispatch.Log // nil in tests
	in       io.Reader
	out, err io.Writer
}

// runFeature screens, plans, shows, confirms and files the feature request in c.Feature, and
// returns the epic's ID, which the run is then scoped to. When there is nothing to run it returns
// "" and the exit code; what happened has been reported.
func runFeature(
	ctx context.Context, stops *stopWatch, c options, log *dispatch.Log, stdin io.Reader, stdout, stderr io.Writer,
) (string, int) {
	// Ctrl+C stops the feature run as it does the loop; until the plan is filed, nothing is left.
	ctx, stop := stops.context(ctx)
	defer stop()
	return featureRun{
		request: c.Feature,
		repo:    c.Repo,
		yes:     c.Yes,
		// The plan and the question go to stdout: asked with stdout sent to a file, the question
		// would wait for an answer to something nobody sees.
		terminal: isTerminal(stdin) && isTerminal(stdout),
		organs:   organ.Client{Bin: "claude", Model: c.OrganModel, Effort: c.OrganEffort},
		tracker:  beads.Tracker{Repo: c.Repo},
		log:      log,
		in:       stdin,
		out:      stdout,
		err:      stderr,
	}.run(ctx)
}

func (f featureRun) run(ctx context.Context) (string, int) {
	// The loop checks the main checkout before each ticket; a feature run checks it before filing,
	// so that a run that couldn't start files nothing.
	if dirty, err := (git.Git{}).DirtyTree(ctx, f.repo); err != nil {
		if code, stopped := f.interrupted(ctx); stopped {
			return "", code
		}
		fmt.Fprintf(f.err, "orchestra couldn't read the state of %s: %v\n", f.repo, err)
		return "", dispatch.ExitTool
	} else if dirty != "" {
		fmt.Fprintf(f.err, "orchestra cannot start: uncommitted changes in %s. Commit or stash them first; "+
			"inspect with: git status\n", f.repo)
		return "", dispatch.ExitDirty
	}

	fmt.Fprintln(f.out, "screening the request with claude…")
	s, err := f.organs.Screen(ctx, organ.Request{Text: f.request, Repo: filepath.Base(f.repo), README: f.readme()})
	if code, stopped := f.interrupted(ctx); stopped {
		return "", code
	}
	switch {
	case err != nil:
		fmt.Fprintln(f.err, "orchestra couldn't screen the request:", dispatch.FirstLine(err.Error()))
		f.logLine("FEATURE not screened: " + dispatch.FirstLine(err.Error()))
		return "", dispatch.ExitSetup
	case s.Verdict == organ.ScreenReject:
		fmt.Fprintln(f.err, "orchestra won't plan this request:", s.Reason)
		f.logLine("FEATURE rejected: " + s.Reason)
		return "", dispatch.ExitSetup
	case s.Verdict == organ.ScreenUnclear:
		fmt.Fprintln(f.err, "orchestra can't plan this request yet:", s.Reason)
		f.logLine("FEATURE unclear: " + s.Reason)
		return "", dispatch.ExitSetup
	}

	// Every ticket not closed, epics too, so the plan doesn't repeat one: one left in progress by a
	// stopped run, or set aside with bd defer, is still to be done.
	unclosed, err := f.tracker.Unclosed(ctx)
	if err != nil {
		if code, stopped := f.interrupted(ctx); stopped {
			return "", code
		}
		fmt.Fprintln(f.err, "orchestra couldn't plan the request: cannot read the tickets:", err)
		return "", dispatch.ExitTool
	}
	tickets := make([]organ.UnclosedTicket, len(unclosed))
	for i, t := range unclosed {
		tickets[i] = organ.UnclosedTicket{ID: t.ID, Status: t.Status, Title: t.Title}
	}
	ev, err := organ.GatherFeature(ctx, f.repo, f.request, git.Git{}.TrackedFiles(ctx, f.repo), tickets)
	if err != nil {
		if code, stopped := f.interrupted(ctx); stopped {
			return "", code
		}
		fmt.Fprintln(f.err, "orchestra couldn't plan the request:", err)
		return "", dispatch.ExitSetup
	}
	fmt.Fprintln(f.out, "planning the feature with claude… (this can take a few minutes; ctrl+c stops it)")
	p, err := f.organs.PlanFeature(ctx, ev)
	if code, stopped := f.interrupted(ctx); stopped {
		return "", code
	}
	switch {
	case err != nil:
		fmt.Fprintln(f.err, "orchestra couldn't plan the request:", dispatch.FirstLine(err.Error()))
		f.logLine("FEATURE not planned: " + dispatch.FirstLine(err.Error()))
		return "", dispatch.ExitSetup
	case p.NeedsAnswers():
		fmt.Fprintln(f.err, "orchestra needs answers before it can plan this request:")
		for _, q := range p.Questions {
			fmt.Fprintln(f.err, "  - "+q)
		}
		fmt.Fprintln(f.err, "Nothing was filed. Run it again with the answers in the request: "+
			"orchestra --feature \"<the request, and the answers>\"")
		f.logLine(fmt.Sprintf("FEATURE needs answers: %d questions", len(p.Questions)))
		return "", dispatch.ExitSetup
	}

	showPlan(f.out, p)
	question := fmt.Sprintf("File these %d tickets and start the run?", len(p.Tickets))
	if len(p.Tickets) == 1 {
		question = "File this ticket and start the run?"
	}
	switch {
	case f.yes:
	case !f.terminal:
		fmt.Fprintln(f.err, "orchestra has no terminal to ask on: nothing was filed. "+
			"Pass --yes to file the plan without asking.")
		return "", dispatch.ExitSetup
	default:
		ok, err := confirm(ctx, f.in, f.out, question)
		if err != nil {
			fmt.Fprintln(f.err, "orchestra: stopped before filing; nothing was filed.")
			return "", dispatch.ExitInterrupted
		}
		if !ok {
			fmt.Fprintln(f.out, "Nothing was filed.")
			return "", dispatch.ExitOK
		}
	}
	return f.file(ctx, p)
}

// interrupted reports whether Ctrl+C (or another of stopSignals) stopped the feature run, saying
// that nothing was filed, and returns its exit code.
func (f featureRun) interrupted(ctx context.Context) (int, bool) {
	if ctx.Err() == nil {
		return 0, false
	}
	fmt.Fprintln(f.err, "orchestra: stopped before filing; nothing was filed.")
	return dispatch.ExitInterrupted, true
}

// readme is the repository's README.md, or "" when it has none or it can't be read. It is read
// through an os.Root, so a symbolic link can't lead outside the repository.
func (f featureRun) readme() string {
	root, err := os.OpenRoot(f.repo)
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }() // read-only: nothing to flush
	b, err := root.ReadFile("README.md")
	if err != nil {
		return ""
	}
	return string(b)
}

func (f featureRun) logLine(text string) {
	if f.log != nil {
		f.log.Line(time.Now(), text)
	}
}

// showPlan prints the plan for the maintainer to confirm: the epic, then each ticket with its type,
// priority, files and the tickets it waits for, and what checking the plan changed.
func showPlan(w io.Writer, p organ.FeaturePlan) {
	fmt.Fprintf(w, "\nEpic: %s\n", p.Epic.Title)
	for line := range strings.Lines(p.Epic.Description) {
		fmt.Fprintln(w, "  "+strings.TrimRight(line, "\n"))
	}
	width := 0
	for _, t := range p.Tickets {
		width = max(width, len(t.Key))
	}
	fmt.Fprintf(w, "\n%s:\n", plural(len(p.Tickets), "ticket"))
	indent := strings.Repeat(" ", width+4)
	for _, t := range p.Tickets {
		fmt.Fprintf(w, "  %-*s  %-7s P%d  %s\n", width, t.Key, t.Type, t.Priority, t.Title)
		if len(t.Files) > 0 {
			fmt.Fprintf(w, "%sfiles: %s\n", indent, strings.Join(t.Files, ", "))
		}
		if len(t.BlockedBy) > 0 {
			fmt.Fprintf(w, "%safter: %s\n", indent, strings.Join(t.BlockedBy, ", "))
		}
	}
	if len(p.Notes) > 0 {
		fmt.Fprintln(w, "\nChecking the plan:")
		for _, n := range p.Notes {
			fmt.Fprintln(w, "  - "+n)
		}
	}
	fmt.Fprintln(w)
}

// confirm asks the question on out and reads the answer from in: yes for y or yes, no for
// anything else. It reads one byte at a time, so nothing after the answer's line is taken from
// the dashboard that reads in next. An error means ctx ended before an answer came.
func confirm(ctx context.Context, in io.Reader, out io.Writer, question string) (bool, error) {
	fmt.Fprint(out, question+" [y/N] ")
	answer := make(chan string, 1)
	go func() {
		var line []byte
		b := make([]byte, 1)
		for {
			n, err := in.Read(b)
			if n == 1 && b[0] == '\n' {
				break
			}
			line = append(line, b[:n]...)
			if err != nil {
				break
			}
		}
		answer <- string(line)
	}()
	select {
	case a := <-answer:
		a = strings.ToLower(strings.TrimSpace(a))
		return a == "y" || a == "yes", nil
	case <-ctx.Done():
		// The reader is left waiting on in; orchestra exits next.
		fmt.Fprintln(out)
		return false, ctx.Err()
	}
}

// planLink is a blocks link of a filed plan: ticket blocked waits for blocker. Both are bd IDs;
// the plan calls them blockedKey and blockerKey.
type planLink struct {
	blocked, blocker       string
	blockedKey, blockerKey string
}

// file files the plan with bd: the epic, its tickets as its children, then the blocks links. It
// returns the epic's ID, or, when a step fails, "" and the exit code, having listed what was
// filed and how to remove it or carry on with it.
func (f featureRun) file(ctx context.Context, p organ.FeaturePlan) (string, int) {
	prio := 4
	for _, t := range p.Tickets {
		prio = min(prio, t.Priority)
	}
	fmt.Fprintln(f.out, "filing the plan with bd…")
	epic, err := f.tracker.Create(ctx, beads.NewTicket{
		Title: p.Epic.Title, Description: p.Epic.Description, Type: "epic", Priority: prio,
	})
	if err != nil {
		return "", f.fileFailed(ctx, "the epic", err, "", nil, nil)
	}
	filed := []string{epic + " (epic) " + p.Epic.Title}
	ids := map[string]string{}
	for _, t := range p.Tickets {
		id, err := f.tracker.Create(ctx, beads.NewTicket{
			Title: t.Title, Description: t.Description, Acceptance: t.Acceptance,
			Type: t.Type, Priority: t.Priority, Parent: epic, Files: t.Files,
		})
		if err != nil {
			return "", f.fileFailed(ctx, "ticket "+t.Key, err, epic, filed, nil)
		}
		ids[t.Key] = id
		filed = append(filed, id+" ("+t.Key+") "+t.Title)
	}
	var links []planLink
	for _, t := range p.Tickets {
		for _, b := range t.BlockedBy {
			links = append(links, planLink{blocked: ids[t.Key], blocker: ids[b], blockedKey: t.Key, blockerKey: b})
		}
	}
	for i, l := range links {
		if err := f.tracker.AddBlock(ctx, l.blocker, l.blocked); err != nil {
			what := fmt.Sprintf("the link %s (%s) after %s (%s)", l.blocked, l.blockedKey, l.blocker, l.blockerKey)
			return "", f.fileFailed(ctx, what, err, epic, filed, links[i:])
		}
	}
	fmt.Fprintf(f.out, "filed epic %s with %s\n", epic, plural(len(p.Tickets), "ticket"))
	f.logLine(fmt.Sprintf("FEATURE filed epic %s with %s: %s",
		epic, plural(len(p.Tickets), "ticket"), dispatch.FeatureLine(f.request)))
	return epic, dispatch.ExitOK
}

// fileFailed reports that filing what stopped with err, listing what was filed before it (the
// epic, then its tickets) with the commands to remove it or carry on with it, and returns the exit
// code. unlinked is the links not added when a link failed, that one first.
func (f featureRun) fileFailed(
	ctx context.Context, what string, err error, epic string, filed []string, unlinked []planLink,
) int {
	fmt.Fprintf(f.err, "orchestra couldn't file %s: %s\n", what, dispatch.FirstLine(err.Error()))
	code := dispatch.ExitTool
	if ctx.Err() != nil {
		code = dispatch.ExitInterrupted
	}
	var cmdErr *command.Error
	stopped := errors.As(err, &cmdErr) && cmdErr.Stopped
	if epic == "" {
		switch {
		case filedNothing(err):
			fmt.Fprintln(f.err, "Nothing was filed.")
		case stopped:
			fmt.Fprintln(f.err, "bd was stopped and may have filed the epic before it did: check with: bd list --type epic")
		default:
			fmt.Fprintln(f.err, "bd may have filed the epic all the same: check with: bd list --type epic")
		}
		f.logLine("FEATURE not filed: " + dispatch.FirstLine(err.Error()))
		return code
	}
	fmt.Fprintln(f.err, "Filed before it:")
	ids := make([]string, 0, len(filed))
	for _, line := range filed {
		fmt.Fprintln(f.err, "  "+line)
		id, _, _ := strings.Cut(line, " ")
		ids = append(ids, id)
	}
	switch {
	case unlinked != nil || filedNothing(err):
	case stopped:
		fmt.Fprintf(f.err, "bd was stopped and may have filed more before it did: check with: bd list --parent %s\n", epic)
	default:
		fmt.Fprintf(f.err, "bd may have filed %s all the same: check with: bd list --parent %s\n", what, epic)
	}
	slices.Reverse(ids) // children before their epic
	fmt.Fprintf(f.err, "Remove them with: bd delete %s --force\n", strings.Join(ids, " "))
	if unlinked == nil {
		fmt.Fprintf(f.err, "Or file the rest with bd and carry on with: orchestra --ticket %s\n", epic)
	} else {
		// Without its links the epic's tickets would run side by side, in no particular order.
		fmt.Fprintln(f.err, "Or add the links that are missing and carry on:")
		for _, l := range unlinked {
			fmt.Fprintf(f.err, "  bd dep add %s %s\n", l.blocked, l.blocker)
		}
		fmt.Fprintf(f.err, "  orchestra --ticket %s\n", epic)
	}
	f.logLine(fmt.Sprintf("FEATURE filing stopped at %s, after %s: %s", what, strings.Join(ids, " "),
		dispatch.FirstLine(err.Error())))
	return code
}

// filedNothing reports whether bd, failing with err, surely filed nothing: it exited with an error
// of its own. Stopped, or killed by a signal, it may have filed before it went; exiting 0 with
// output that can't be read, it most likely did.
func filedNothing(err error) bool {
	var cmdErr *command.Error
	var exit *exec.ExitError
	return errors.As(err, &cmdErr) && !cmdErr.Stopped && errors.As(cmdErr.Err, &exit) && exit.Exited()
}
