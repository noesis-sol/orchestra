package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A spec run (--spec <file>) builds what a spec describes, a Markdown or plain-text file. Once the
// run's startup checks have passed and its lock is held, orchestra commits the spec, where workers
// can read it (see project.CommitSpec), and has it broken down into Beads tickets: a root epic,
// child epics where the spec has separable parts, and tickets of one worker session each, which the
// breakdown files itself. The run is then scoped to the root epic, as a --feature run is to its
// epic, and starts without asking. It has no ticket limit unless -limit or LIMIT gives one.

// breakdownRequest is what a spec's breakdown is given: the committed spec, by its path in the
// repository, its SHA-256 and its commit, and whether to refine it with the user first (--refine).
type breakdownRequest struct {
	project.Spec
	Refine bool
}

// breakdownResult is what a breakdown filed: the root epic, under which it filed the rest, and the
// assumptions it made where the spec leaves a choice open, for the log.
type breakdownResult struct {
	Root        string // "" when it filed none
	Assumptions []string
}

// specBreakdown breaks a committed spec down into Beads tickets, which it files itself. Stopped (its
// context ended) or failing, it still names the root epic when it filed one.
type specBreakdown interface {
	BreakDown(ctx context.Context, r breakdownRequest) (breakdownResult, error)
}

// errNoBreakdown is the breakdown's error in a build that has none.
var errNoBreakdown = errors.New("the spec breakdown isn't available in this build")

// noBreakdown is the breakdown of a build that has none: it files nothing.
type noBreakdown struct{}

func (noBreakdown) BreakDown(context.Context, breakdownRequest) (breakdownResult, error) {
	return breakdownResult{}, errNoBreakdown
}

// breakdownOf is the breakdown the spec run s has its spec broken down by; tests give their own.
var breakdownOf = func(*session) specBreakdown { return noBreakdown{} }

// checkSpecFile checks the spec --spec (or ORCHESTRA_SPEC, as from says) gives, c.specFile: a file
// of text with something in it (see project.ReadSpec). It makes c.specFile absolute, as the working
// directory has it.
func checkSpecFile(c *options, from string) []string {
	if c.specFile == "" {
		return nil
	}
	given := c.specFile
	if abs, err := filepath.Abs(given); err == nil {
		c.specFile = abs
	}
	_, err := project.ReadSpec(c.specFile)
	var pathErr *fs.PathError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return []string{fmt.Sprintf("Spec not found: %s (%s).", given, from)}
	case errors.Is(err, project.ErrSpecEmpty):
		return []string{fmt.Sprintf("The spec %s (%s) is empty: write what to build in it first.", given, from)}
	case errors.Is(err, project.ErrSpecNotText):
		return []string{fmt.Sprintf("The spec %s (%s) isn't text: give a Markdown or plain-text file, in UTF-8.",
			given, from)}
	case errors.As(err, &pathErr): // its path is the one given, made absolute
		err = pathErr.Err
	}
	return []string{fmt.Sprintf("Cannot read the spec %s (%s): %v.", given, from, err)}
}

// checkSpecPath settles where the spec is kept in the repository, c.Spec (see project.SpecPath), and
// checks that git can commit it there: not in the .git folder, nor where git ignores it.
func checkSpecPath(ctx context.Context, c *options, from string) []string {
	if c.specFile == "" {
		return nil
	}
	c.Spec, _ = project.SpecPath(c.Repo, c.specFile)
	if project.InGitDir(c.Spec) {
		return []string{fmt.Sprintf("The spec %s (%s) is in the repository's .git folder, which git keeps "+
			"no file of: move it out.", c.Spec, from)}
	}
	ignored, err := git.Git{}.Ignored(ctx, c.Repo, c.Spec)
	switch {
	case err != nil:
		return []string{fmt.Sprintf("Cannot tell whether git ignores %s, where the spec (%s) is kept: %v.",
			c.Spec, from, err)}
	case ignored:
		return []string{fmt.Sprintf("git ignores %s, where the spec (%s) is kept, and workers only see "+
			"committed files: move the spec, or stop ignoring that path (git check-ignore -v %s says where).",
			c.Spec, from, command.ShellWord(c.Spec))}
	}
	return nil
}

// specTracker is what a spec run reads from Beads: what was filed under the root epic, to count it.
type specTracker interface {
	Descendants(ctx context.Context, id string) ([]dispatch.Ticket, error)
}

// specRun is a spec run's step before its loop: the spec committed, then broken down.
type specRun struct {
	file      string // the spec, as --spec gives it, made absolute
	repo      string // the main checkout
	refine    bool   // --refine
	breakdown specBreakdown
	tracker   specTracker
	log       *dispatch.Log // nil in tests
	out, err  io.Writer
}

// runSpec sets up the repository of the run s where orchestra init hasn't (spec_setup.go), commits
// the spec and has it broken down, and returns the root epic's ID, which the run is then scoped to.
// When there is nothing to run it returns "" and the exit code; what happened has been reported.
func (s *session) runSpec(ctx context.Context) (string, int) {
	// Ctrl+C stops the breakdown as it does the loop; what it filed so far stays filed.
	ctx, stop := s.stops.context(ctx)
	defer stop()
	if code := s.setUpForSpec(ctx); code != dispatch.ExitOK {
		return "", code
	}
	c := s.cfg
	return specRun{
		file: c.specFile, repo: c.Repo, refine: c.Refine,
		breakdown: breakdownOf(s), tracker: beads.Tracker{Repo: c.Repo},
		log: s.log, out: s.e.stdout, err: s.e.stderr,
	}.run(ctx)
}

func (r specRun) run(ctx context.Context) (string, int) {
	spec, code := r.commit(ctx)
	if code != dispatch.ExitOK {
		return "", code
	}
	res, err := r.breakdown.BreakDown(ctx, breakdownRequest{Spec: spec, Refine: r.refine})
	switch {
	case ctx.Err() != nil:
		return "", r.stopped(true, res.Root)
	case err != nil:
		detail := tui.Printable(dispatch.FirstLine(err.Error()))
		fmt.Fprintln(r.err, "orchestra couldn't break the spec down:", detail)
		if res.Root != "" {
			fmt.Fprintf(r.err, "It filed %s before it stopped. %s\n", res.Root, r.again())
		}
		r.logLine("SPEC: not broken down: " + detail)
		return "", dispatch.ExitSetup
	case res.Root == "":
		fmt.Fprintln(r.err, "orchestra: the spec's breakdown named no root epic, so there is nothing to run.")
		r.logLine("SPEC: broken down into no root epic")
		return "", dispatch.ExitSetup
	}
	r.sayFiled(ctx, res)
	return res.Root, dispatch.ExitOK
}

// commit commits the spec, once the main checkout has nothing uncommitted besides it, and says so;
// one committed already is left as it is. It returns ExitOK, or the exit code having said why not.
func (r specRun) commit(ctx context.Context) (project.Spec, int) {
	// Checked before anything is committed or filed, as a feature's checkout is before it is filed.
	rel, _ := project.SpecPath(r.repo, r.file)
	switch code := cleanCheckout(ctx, r.repo, r.err, rel); code {
	case dispatch.ExitOK:
	case dispatch.ExitInterrupted:
		return project.Spec{}, r.stopped(false, "")
	default:
		return project.Spec{}, code
	}
	spec, committed, err := project.CommitSpec(ctx, r.repo, r.file)
	switch {
	case ctx.Err() != nil:
		return project.Spec{}, r.stopped(false, "")
	case err != nil:
		detail := dispatch.FirstLine(err.Error())
		fmt.Fprintln(r.err, "orchestra couldn't commit the spec:", detail)
		r.logLine("SPEC: not committed: " + detail)
		if _, gitFailed := errors.AsType[*command.Error](err); gitFailed {
			return project.Spec{}, dispatch.ExitTool
		}
		return project.Spec{}, dispatch.ExitSetup
	case committed:
		fmt.Fprintf(r.out, "committed the spec %s (sha256 %s)\n", spec.Path, spec.ShortSHA256())
		r.logLine(fmt.Sprintf("SPEC: committed %s (sha256 %s)", spec.Path, spec.ShortSHA256()))
	default:
		fmt.Fprintf(r.out, "the spec %s is committed already (sha256 %s)\n", spec.Path, spec.ShortSHA256())
		r.logLine(fmt.Sprintf("SPEC: %s is committed already (sha256 %s, commit %s)",
			spec.Path, spec.ShortSHA256(), spec.Commit[:min(12, len(spec.Commit))]))
	}
	return spec, dispatch.ExitOK
}

// sayFiled says what the breakdown filed: the root epic, the epics and tickets under it (by bd's
// count, the root among the epics), and the assumptions it made.
func (r specRun) sayFiled(ctx context.Context, res breakdownResult) {
	counts := ""
	if under, err := r.tracker.Descendants(ctx, res.Root); err != nil {
		r.logRaw(fmt.Errorf("cannot count what the breakdown filed under %s: %w", res.Root, err))
	} else {
		epics := 1
		for _, t := range under {
			if t.IssueType == "epic" {
				epics++
			}
		}
		counts = fmt.Sprintf(" (%s, %s)", plural(epics, "epic"), plural(len(under)+1-epics, "ticket"))
	}
	fmt.Fprintf(r.out, "broke the spec down into %s%s\n", res.Root, counts)
	r.logLine("SPEC: broken down into " + res.Root + counts)
	if len(res.Assumptions) == 0 {
		return
	}
	fmt.Fprintln(r.out, "Assumed where the spec leaves a choice open:")
	for _, a := range res.Assumptions {
		line := planLine(a) // the breakdown's words, printable and on one line
		fmt.Fprintln(r.out, "  - "+line)
		r.logLine("SPEC: assumed: " + line)
	}
}

// stopped reports that Ctrl+C (or another of stopSignals) stopped the spec run before its loop:
// during the breakdown (during), which had filed root ("" for none), or before it. It returns the
// exit code.
func (r specRun) stopped(during bool, root string) int {
	switch {
	case !during:
		fmt.Fprintln(r.err, "orchestra: stopped before the spec was broken down; nothing was filed.")
		r.logLine("SPEC: stopped before the breakdown")
	case root != "":
		fmt.Fprintf(r.err, "orchestra: stopped while the spec was broken down, with %s filed.\n", root)
		r.logLine("SPEC: stopped during the breakdown, with " + root + " filed")
	default:
		fmt.Fprintln(r.err, "orchestra: stopped while the spec was broken down, before a root epic was filed.")
		r.logLine("SPEC: stopped during the breakdown, before a root epic was filed")
	}
	fmt.Fprintln(r.err, r.again())
	return dispatch.ExitInterrupted
}

// again is how to carry on with the spec: the same command.
func (r specRun) again() string {
	return "Run it again to carry on: orchestra --spec " + command.ShellWord(r.file)
}

// logLine writes text to the run's log, if there is one.
func (r specRun) logLine(text string) {
	if r.log != nil {
		r.log.Line(time.Now(), text)
	}
}

// logRaw writes err to the run's log, if there is one.
func (r specRun) logRaw(err error) {
	if r.log != nil {
		r.log.Raw("", err)
	}
}
