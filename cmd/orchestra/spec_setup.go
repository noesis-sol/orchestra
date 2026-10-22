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
// no skill: the breakdown's first ticket, the environment setup, scaffolds the project and its
// checks. It then commits what it wrote, with the spec, as a ticket's worktree has only what is
// committed; in a repository with no commit yet, that is the first. Nothing is installed unattended:
// bd missing is a setup problem, as is a folder that isn't a git repository. Where the run's fetch
// brings in a setup first, an upstream's pushed after the clone was made, the run keeps that one.

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

// specPlan is a spec run's plan, made at configuration, to set up its repository where orchestra init
// hasn't (planSpecSetup): the setup, and the run's flags, which resolve the settings of a setup its
// fetch brings in instead (keepFetchedSetup).
type specPlan struct {
	choice project.Choice
	flags  *runFlags
}

// planSpecSetup has the spec run c, of flags f, plan to set up its repository where orchestra init
// hasn't (c.specSetup), and returns the settings the run reads: those it will have once set up, so
// that the run's problems are all found before anything is written. Elsewhere it returns settings,
// the project's.
func planSpecSetup(c *options, f *runFlags, settings project.Settings) project.Settings {
	if c.work().kind != workSpec || c.Repo == "" || project.IsSetUp(c.Repo) {
		return settings
	}
	choice := specSetupChoice(c.Repo, f.concurrency, c.AgentKind)
	c.specSetup = &specPlan{choice: choice, flags: f}
	return project.ChoiceSettings(project.Settings{}, choice)
}

// setUpForSpec sets up the repository of the spec run s where it planned to (see planSpecSetup), and
// then reads the worker prompt it wrote. Where the run's fetch (spec_fetch.go) has brought in a setup
// meanwhile, an upstream's set up after this clone was made, it sets nothing up and takes that one
// (keepFetchedSetup). It returns the exit code; what went wrong has been said.
func (s *session) setUpForSpec(ctx context.Context) int {
	c := s.cfg
	switch {
	case c.specSetup == nil:
		return dispatch.ExitOK
	case project.IsSetUp(c.Repo):
		return s.keepFetchedSetup(ctx)
	}
	code := specSetup{choice: c.specSetup.choice, repo: c.Repo, file: c.work().spec, getenv: s.e.getenv,
		log: s.log, out: s.e.stdout, err: s.e.stderr}.run(ctx)
	if code != dispatch.ExitOK {
		return code
	}
	return s.readPrompt()
}

// keepFetchedSetup has the spec run s, which planned to set its repository up, keep the setup its
// fetch brought in: it drops its plan, resolves the settings it read with the plan's from that setup's
// settings.json, as configuration does for a repository set up (the terminal it runs in aside, chosen
// already), checks for the Beads database, and reads the worker prompt. It returns the exit code,
// having said what stops the run.
func (s *session) keepFetchedSetup(ctx context.Context) int {
	c := &s.cfg
	plan := c.specSetup
	c.specSetup = nil
	const why = "SPEC: the fetch brought in orchestra's setup: the repository is set up already"
	fmt.Fprintln(s.e.stdout, why)
	s.log.Line(time.Now(), why)
	settings, problems := loadSettings(c.Repo)
	if len(problems) == 0 {
		problems = append(resolveSettings(c, plan.flags, settings), resolveMCP(ctx, c, settings, s.e.getenv)...)
	}
	if st, err := os.Stat(filepath.Join(c.Repo, ".beads")); err != nil || !st.IsDir() {
		problems = append(problems, "No Beads database in "+c.Repo+". Run: bd init")
	}
	if len(problems) > 0 {
		fmt.Fprintln(s.e.stderr, "orchestra cannot start:")
		for _, p := range problems {
			fmt.Fprintln(s.e.stderr, "  - "+p)
			s.log.Line(time.Now(), "SPEC: cannot start: "+p)
		}
		return dispatch.ExitSetup
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
	before, err := git.Git{}.Changes(ctx, u.repo)
	if err != nil { // what the setup writes couldn't be told from what was there
		return u.failed("couldn't read the checkout's state before setting it up: "+err.Error(), dispatch.ExitTool)
	}
	fmt.Fprintln(u.out, "orchestra init hasn't set this repository up: setting it up as init does without a terminal")
	u.logLine("SPEC: setting up " + u.repo + ", which orchestra init hasn't, as init does without a terminal")
	var committed specCommit
	did, halt := runSteps(ctx, u.steps(before, &committed))
	return u.halted(did, halt, rel, committed)
}

// The names of a spec run's setup steps (see specSetup.steps).
const (
	specStepBeads  = "Beads"
	specStepApply  = "the setup"
	specStepCommit = "the commit"
)

// specCommit is the commit of a spec run's setup: its hash, and the files it took.
type specCommit struct {
	hash  string
	paths []string
}

// specFailure is a spec run's setup step's error: why, what orchestra couldn't do, and the exit code.
type specFailure struct {
	why  string
	code int
}

func (f *specFailure) Error() string { return f.why }

// steps are the spec run's setup steps: Beads, then .orchestra/ and the rest Apply writes, then the
// commit of what changed since before, with the spec, which they put in committed.
func (u specSetup) steps(before map[string]string, committed *specCommit) []setupStep {
	return []setupStep{
		{name: specStepBeads, run: func(ctx context.Context) ([]project.Step, error) {
			steps := project.SetUpBeads(ctx, u.repo, u.choice, u.getenv, func(what string) { fmt.Fprintln(u.out, what) })
			for _, s := range steps {
				if s.Kind == project.StepMissing {
					return steps, &specFailure{"couldn't set Beads up: " + s.Detail, dispatch.ExitTool}
				}
			}
			if st, err := os.Stat(filepath.Join(u.repo, ".beads")); err != nil || !st.IsDir() {
				return steps, &specFailure{"couldn't set Beads up: bd init left no .beads folder", dispatch.ExitTool}
			}
			return steps, nil
		}},
		// Apply writes the whole setup once it starts, or nothing where the stop came first.
		{name: specStepApply, run: func(ctx context.Context) ([]project.Step, error) {
			applied, err := project.Apply(ctx, u.repo, u.choice, false)
			if err != nil {
				return applied, &specFailure{"couldn't set the repository up: " + dispatch.FirstLine(err.Error()),
					dispatch.ExitSetup}
			}
			return applied, nil
		}},
		{name: specStepCommit, run: func(ctx context.Context) ([]project.Step, error) {
			var err error
			committed.hash, committed.paths, err = project.CommitSetUp(ctx, u.repo, before, u.file)
			if err != nil {
				code := dispatch.ExitSetup
				if _, gitFailed := errors.AsType[*command.Error](err); gitFailed {
					code = dispatch.ExitTool
				}
				return nil, &specFailure{"couldn't commit its setup: " + dispatch.FirstLine(err.Error()), code}
			}
			return nil, nil
		}},
	}
}

// halted reports how the setup's steps ended, having taken did, where halt halted them, and returns
// the exit code. Once it has written the setup, it lists what it did, then says what the commit did;
// rel is the spec's path in the repository.
func (u specSetup) halted(did []project.Step, halt *setupHalt, rel string, committed specCommit) int {
	if halt == nil || halt.step == specStepCommit {
		for _, s := range did {
			line := s.Label
			if s.Detail != "" {
				line += ": " + s.Detail
			}
			line = tui.Printable(line)
			fmt.Fprintln(u.out, "  - "+line)
			u.logLine("SPEC: setup: " + line)
		}
	}
	switch {
	case halt == nil:
	case halt.stop && committed.hash != "": // the stop came as the commit was made: it says so, then stops
		u.committed(rel, committed)
		return u.stoppedCommitted()
	case halt.stop:
		return u.stopped()
	default:
		if f, ok := errors.AsType[*specFailure](halt.err); ok {
			return u.failed(f.why, f.code)
		}
		return u.failed(halt.step+": "+dispatch.FirstLine(halt.err.Error()), dispatch.ExitSetup)
	}
	u.committed(rel, committed)
	return dispatch.ExitOK
}

// committed reports the setup's commit, with the spec at rel.
func (u specSetup) committed(rel string, committed specCommit) {
	done := fmt.Sprintf("committed the setup with the spec %s as %s (%s)", rel,
		committed.hash[:min(12, len(committed.hash))], plural(len(committed.paths), "file"))
	fmt.Fprintln(u.out, done)
	u.logLine("SPEC: " + done)
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

// stoppedCommitted reports that a stop came once the setup was committed, and returns the exit code.
func (u specSetup) stoppedCommitted() int {
	fmt.Fprintln(u.err, "orchestra: stopped once it had set the repository up for the spec and committed it "+
		"(see git log).")
	u.logLine("SPEC: stopped once the setup was committed")
	return dispatch.ExitInterrupted
}

// logLine writes text to the run's log, if there is one.
func (u specSetup) logLine(text string) {
	if u.log != nil {
		u.log.Line(time.Now(), text)
	}
}
