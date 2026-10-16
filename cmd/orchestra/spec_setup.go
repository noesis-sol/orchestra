package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A spec run starts a whole app from an empty repository, unattended:
//
//	mkdir app && cd app && git init && orchestra --spec ~/app-spec.md
//
// In a repository orchestra init hasn't set up, the run sets it up itself, with init's defaults as
// init takes them without a terminal (specSetupChoice): bd init where Beads isn't set up, the
// worker prompt, runners that check nothing yet and the settings. It files no test work and installs
// no skill: the breakdown's first ticket scaffolds the project and its checks. It then commits what
// it wrote, with the spec, as a ticket's worktree has only what is committed; in a repository with
// no commit yet, that is the first. Nothing is installed unattended: bd missing is a setup problem,
// as is a folder that isn't a git repository.

// noRepoForSpec is a spec run's setup problem outside a git repository.
const noRepoForSpec = "Not inside a git repository: cd into the project first, or for a new one, " +
	"start it with: git init"

// specSetupChoice is what a spec run sets up the repository at repo with, where orchestra init
// hasn't: init's defaults, as init takes them without a terminal, declining what it offers; the
// concurrency -c (or ORCHESTRA_CONCURRENT) gives, else 1; and no MCP servers for workers. agent is
// the workers' agent kind.
func specSetupChoice(repo string, concurrency project.Given[int], agent string) project.Choice {
	f := initFlags{concurrent: concurrency, mcp: project.Given[string]{Given: true}, agent: agent}
	if !concurrency.Given {
		f.concurrent = project.Given[int]{Value: 1, Given: true}
	}
	return initChoice(repo, f, project.Settings{})
}

// planSpecSetup has the spec run c set up its repository where orchestra init hasn't (c.specSetup),
// and returns the settings the run reads: those it will have once set up, so that the run's
// problems are all found before anything is written. Elsewhere it returns settings, the project's.
func planSpecSetup(c *options, concurrency project.Given[int], settings project.Settings) project.Settings {
	if c.specFile == "" || c.Repo == "" || project.IsSetUp(c.Repo) {
		return settings
	}
	choice := specSetupChoice(c.Repo, concurrency, c.AgentKind)
	c.specSetup = &choice
	return project.ChoiceSettings(project.Settings{}, choice)
}

// setUpForSpec sets up the repository of the spec run s where it needs it (see planSpecSetup), and
// then reads the worker prompt it wrote. It returns the exit code; what went wrong has been said.
func (s *session) setUpForSpec(ctx context.Context) int {
	c := s.cfg
	if c.specSetup == nil {
		return dispatch.ExitOK
	}
	code := specSetup{choice: *c.specSetup, repo: c.Repo, file: c.specFile, getenv: s.e.getenv,
		log: s.log, out: s.e.stdout, err: s.e.stderr}.run(ctx)
	if code != dispatch.ExitOK {
		return code
	}
	return s.readPrompt()
}

// specSetup is a spec run's setup of a repository orchestra init hasn't set up.
type specSetup struct {
	choice   project.Choice
	repo     string // the main checkout
	file     string // the spec, as --spec gives it, made absolute
	getenv   func(string) string
	log      *dispatch.Log // nil in tests
	out, err io.Writer
}

// run sets the repository up, once the main checkout has nothing uncommitted besides the spec, and
// commits what it wrote with the spec. It returns ExitOK, or the exit code having said why not.
func (u specSetup) run(ctx context.Context) int {
	rel, _ := project.SpecPath(u.repo, u.file)
	switch code := cleanCheckout(ctx, u.repo, u.err, rel); code {
	case dispatch.ExitOK:
	case dispatch.ExitInterrupted:
		return u.stopped()
	default:
		return code
	}
	before := git.Git{}.Changes(ctx, u.repo)
	fmt.Fprintln(u.out, "orchestra init hasn't set this repository up: setting it up as init does without a terminal")
	u.logLine("SPEC: setting up " + u.repo + ", which orchestra init hasn't, as init does without a terminal")
	steps := project.SetUpBeads(ctx, u.repo, u.choice, u.getenv, func(what string) { fmt.Fprintln(u.out, what) })
	if ctx.Err() != nil {
		return u.stopped()
	}
	for _, s := range steps {
		if s.Kind == project.StepMissing {
			return u.failed("couldn't set Beads up: "+s.Detail, dispatch.ExitTool)
		}
	}
	if st, err := os.Stat(filepath.Join(u.repo, ".beads")); err != nil || !st.IsDir() {
		return u.failed("couldn't set Beads up: bd init left no .beads folder", dispatch.ExitTool)
	}
	applied, err := project.Apply(ctx, u.repo, u.choice, false)
	if steps = append(steps, applied...); err != nil {
		return u.failed("couldn't set the repository up: "+dispatch.FirstLine(err.Error()), dispatch.ExitSetup)
	}
	for _, s := range steps {
		line := s.Label
		if s.Detail != "" {
			line += ": " + s.Detail
		}
		line = tui.Printable(line)
		fmt.Fprintln(u.out, "  - "+line)
		u.logLine("SPEC: setup: " + line)
	}
	commit, paths, err := project.CommitSetUp(ctx, u.repo, before, u.file)
	switch {
	case ctx.Err() != nil:
		return u.stopped()
	case err != nil:
		code := dispatch.ExitSetup
		if _, gitFailed := errors.AsType[*command.Error](err); gitFailed {
			code = dispatch.ExitTool
		}
		return u.failed("couldn't commit its setup: "+dispatch.FirstLine(err.Error()), code)
	}
	done := fmt.Sprintf("committed the setup with the spec %s as %s (%s)", rel, commit[:min(12, len(commit))],
		plural(len(paths), "file"))
	fmt.Fprintln(u.out, done)
	u.logLine("SPEC: " + done)
	return dispatch.ExitOK
}

// failed reports that the setup failed, why being what orchestra couldn't do, and returns code.
func (u specSetup) failed(why string, code int) int {
	fmt.Fprintln(u.err, "orchestra "+why)
	fmt.Fprintln(u.err, "Whatever it wrote is left uncommitted (see git status). orchestra init sets the repository "+
		"up with you.")
	u.logLine("SPEC: not set up: " + why)
	return code
}

// stopped reports that Ctrl+C (or another of stopSignals) stopped the setup, and returns the exit code.
func (u specSetup) stopped() int {
	fmt.Fprintln(u.err, "orchestra: stopped while setting the repository up for the spec; nothing was committed "+
		"(see git status).")
	u.logLine("SPEC: stopped while setting the repository up")
	return dispatch.ExitInterrupted
}

// logLine writes text to the run's log, if there is one.
func (u specSetup) logLine(text string) {
	if u.log != nil {
		u.log.Line(time.Now(), text)
	}
}
