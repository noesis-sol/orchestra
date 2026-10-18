package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
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
// epic, and starts without asking. It has no ticket limit unless --limit or LIMIT gives one. Run again,
// it carries on with the root epic an earlier run filed rather than break the spec down again, and
// with the spec changed since, the breakdown only updates that tree for the change (spec_carry.go).

// breakdownRequest is what a spec's breakdown is given: the committed spec, by its path in the
// repository, its SHA-256 and its commit, whether to refine it with the user first (--refine), and,
// when the spec changed since an earlier run filed its tree, what changed (update mode).
type breakdownRequest struct {
	project.Spec
	Refine bool
	Update *specUpdate // nil for a spec broken down for the first time
}

// breakdownResult is what a breakdown filed: the root epic, under which it filed the rest, and the
// assumptions it made where the spec leaves a choice open, for the log.
type breakdownResult struct {
	Root        string // "" when it filed none
	Assumptions []string
	Problem     string // with no root epic, why it filed none, as it says; "" when it doesn't
}

// specBreakdown breaks a committed spec down into Beads tickets, which it files itself. Stopped (its
// context ended) or failing, it still names the root epic when it filed one. One with the user that
// the user left without handing a root epic back is errNotHandedBack.
type specBreakdown interface {
	BreakDown(ctx context.Context, r breakdownRequest) (breakdownResult, error)
}

// breakdownOf is the breakdown the spec run s has its spec broken down by: with the user, in a
// Claude Code session beside orchestra's (spec_refine.go), when the run has --refine, else the
// headless one (spec_breakdown.go); tests give their own.
var breakdownOf = func(s *session) specBreakdown {
	if s.cfg.Refine {
		return newRefineBreakdown(s)
	}
	return newHeadlessBreakdown(s)
}

// checkSpecFile checks the spec --spec (or ORCHESTRA_SPEC, as from says) gives, c.specFile: a file
// of text with something in it (see project.ReadSpec). It makes c.specFile absolute, as the working
// directory dir has it.
func checkSpecFile(c *options, dir, from string) []string {
	if c.specFile == "" {
		return nil
	}
	given := c.specFile
	if !filepath.IsAbs(given) {
		c.specFile = filepath.Join(dir, given)
	}
	if abs, err := filepath.Abs(c.specFile); err == nil {
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

// specTracker is Beads as a spec run reads it: the root epic its breakdown filed and what is under it,
// to check and count it; what carries the spec's metadata; the blocks cycles. It sets the root
// epic's spec metadata where the breakdown left it out.
type specTracker interface {
	Show(ctx context.Context, id string) (dispatch.Ticket, error)
	Descendants(ctx context.Context, id string) ([]dispatch.Ticket, error)
	WithMetadata(ctx context.Context, key, value string) ([]dispatch.Ticket, error)
	Cycles(ctx context.Context) ([][]string, error)
	SetMetadata(ctx context.Context, id, key, value string) error
}

// specRun is a spec run's step before its loop: the spec committed, then broken down, unless an
// earlier run broke it down already.
type specRun struct {
	file      string // the spec, as --spec gives it, made absolute
	repo      string // the main checkout
	refine    bool   // --refine
	breakdown specBreakdown
	tracker   specTracker
	log       *dispatch.Log // nil in tests
	out, err  io.Writer
	update    *specUpdate // the breakdown's update, once run finds the spec changed since its tree was filed
}

// runSpec sets up the repository of the run s where orchestra init hasn't (spec_setup.go), commits
// the spec and has it broken down, or carries on with the root epic an earlier run filed for it, and
// returns the root epic's ID, which the run is then scoped to. When there is nothing to run it
// returns "" and the exit code; what happened has been reported.
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
	c, code := r.carryOn(ctx, spec)
	switch {
	case code != dispatch.ExitOK:
		return "", code
	case c.root != "": // the spec unchanged since its tree was filed: no breakdown
		return c.root, dispatch.ExitOK
	}
	r.update = c.update
	res, err := r.breakdown.BreakDown(ctx, breakdownRequest{Spec: spec, Refine: r.refine, Update: c.update})
	switch {
	case ctx.Err() != nil:
		return "", r.stopped(true, res.Root)
	case errors.Is(err, errNotHandedBack):
		return "", r.notHandedBack(ctx, spec)
	case err != nil:
		detail := tui.Printable(dispatch.FirstLine(err.Error()))
		fmt.Fprintln(r.err, "orchestra couldn't break the spec down:", detail)
		r.logLine("SPEC: not broken down: " + detail)
		return "", r.unfinished(ctx, spec, res.Root)
	case res.Root == "":
		fmt.Fprintln(r.err, "orchestra: the spec's breakdown named no root epic, so there is nothing to run.")
		line := "SPEC: broken down into no root epic"
		if why := planLine(res.Problem); why != "" {
			fmt.Fprintln(r.err, "It says: "+why)
			line += ": " + why
		}
		r.logLine(line)
		return "", r.unfinished(ctx, spec, "")
	}
	if code := r.check(ctx, spec, res.Root); code != dispatch.ExitOK {
		return "", code
	}
	r.sayFiled(ctx, res)
	return res.Root, dispatch.ExitOK
}

// unfinished says what a breakdown that didn't finish filed for the spec, as bd has it by the spec's
// metadata, under the root epic it filed (root, else the one bd has), and how to carry on. It
// returns the exit code.
func (r specRun) unfinished(ctx context.Context, spec project.Spec, root string) int {
	issues, err := r.tracker.WithMetadata(ctx, dispatch.SpecKey, spec.Path)
	switch {
	case err != nil:
		fmt.Fprintln(r.err, "orchestra can't list what it filed:", dispatch.FirstLine(err.Error()))
	case len(issues) == 0:
		fmt.Fprintf(r.err, "It filed nothing for %s.\n", spec.Path)
		r.logLine("SPEC: nothing filed for " + spec.Path)
	default:
		if root == "" {
			root = rootAmong(issues, spec)
		}
		epics := 0
		for _, t := range issues {
			if t.IssueType == "epic" {
				epics++
			}
		}
		filed := plural(epics, "epic") + " and " + plural(len(issues)-epics, "ticket") + " for " + spec.Path
		if root != "" {
			filed += ", under " + root
		}
		if r.update != nil { // filed by this run's update and earlier runs
			fmt.Fprintf(r.err, "The spec's tree has %s:\n", filed)
			r.logLine("SPEC: the tree has " + filed)
		} else {
			fmt.Fprintf(r.err, "It filed %s:\n", filed)
			r.logLine("SPEC: filed " + filed)
		}
		for i, t := range issues {
			if i == unfinishedShown {
				fmt.Fprintf(r.err, "  … and %d more: bd list --metadata-field %s --all\n", len(issues)-i,
					command.ShellWord(dispatch.SpecKey+"="+spec.Path))
				break
			}
			fmt.Fprintf(r.err, "  %s  %s  %s\n", t.ID, t.IssueType, planLine(t.Title))
		}
	}
	fmt.Fprintln(r.err, r.again())
	return dispatch.ExitSetup
}

// unfinishedShown is how many of the issues an unfinished breakdown filed it lists.
const unfinishedShown = 20

// check checks the tree a breakdown filed under root before the run starts on it (checkBreakdown),
// whichever breakdown it was: a root epic that isn't the spec's, or in update mode another than the
// one whose tree it updated, ends the run, exit 2, saying what was filed; anything else amiss is a
// warning, said and logged. It returns the exit code.
func (r specRun) check(ctx context.Context, spec project.Spec, root string) int {
	var warnings []string
	var err error
	if r.update != nil && root != r.update.Root {
		err = fmt.Errorf("it named %s as the root epic, not %s, whose tree it was to update", root, r.update.Root)
	} else {
		warnings, err = checkBreakdown(ctx, r.tracker, spec, root)
	}
	for _, w := range warnings {
		w = planLine(w)
		fmt.Fprintln(r.err, "warning: "+w)
		r.logLine("SPEC: warning: " + w)
	}
	switch {
	case ctx.Err() != nil:
		return r.stopped(true, root)
	case err != nil:
		detail := tui.Printable(dispatch.FirstLine(err.Error()))
		fmt.Fprintln(r.err, "orchestra won't run what the spec's breakdown filed:", detail)
		r.logLine("SPEC: not run: " + detail)
		return r.unfinished(ctx, spec, "")
	}
	return dispatch.ExitOK
}

// commit commits the spec, once the main checkout has nothing uncommitted besides it, and says so;
// one committed already is left as it is. It returns ExitOK, or the exit code having said why not.
func (r specRun) commit(ctx context.Context) (project.Spec, int) {
	// Checked before anything is committed or filed, as a feature's checkout is before its session.
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

// cleanCheckout checks that the main checkout repo has no uncommitted changes, besides those to the
// paths in besides (a spec about to be committed). The loop checks it before each ticket; a spec or a
// feature is checked before anything is filed for it, so that a run that couldn't start files nothing.
// It returns ExitOK, or the exit code having said why not; ExitInterrupted, saying nothing, when ctx
// ended first.
func cleanCheckout(ctx context.Context, repo string, stderr io.Writer, besides ...string) int {
	dirty, err := (git.Git{}).DirtyTreeBesides(ctx, repo, besides...)
	switch {
	case err != nil && ctx.Err() != nil:
		return dispatch.ExitInterrupted
	case err != nil:
		fmt.Fprintf(stderr, "orchestra couldn't read the state of %s: %v\n", repo, err)
		return dispatch.ExitTool
	case dirty != "":
		fmt.Fprintf(stderr, "orchestra cannot start: uncommitted changes in %s. Commit or stash them first; "+
			"inspect with: git status\n", repo)
		return dispatch.ExitDirty
	}
	return dispatch.ExitOK
}

// planLine is s, text a breakdown or bd gave, printable and on one line.
func planLine(s string) string { return strings.Join(strings.Fields(tui.Printable(s)), " ") }

// sayFiled says what the breakdown filed: the root epic, the epics and tickets under it (by bd's
// count, the root among the epics), and the assumptions it made. After an update, the counts are
// the tree's, earlier runs' issues with the update's.
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
	if r.update != nil {
		fmt.Fprintf(r.out, "updated %s for the spec's change%s\n", res.Root, counts)
		r.logLine("SPEC: updated " + res.Root + " for the change" + counts)
	} else {
		fmt.Fprintf(r.out, "broke the spec down into %s%s\n", res.Root, counts)
		r.logLine("SPEC: broken down into " + res.Root + counts)
	}
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
	case r.update != nil:
		fmt.Fprintf(r.err, "orchestra: stopped while %s's tree was updated for the spec's change.\n", r.update.Root)
		r.logLine("SPEC: stopped during the update of " + r.update.Root)
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
	again := "Run it again to carry on: orchestra --spec " + command.ShellWord(r.file)
	if r.refine {
		again += " --refine"
	}
	return again
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
