package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentkind"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/requires"
	"github.com/noesis-sol/orchestra/internal/tui"
	"golang.org/x/term"
)

// runInit sets up .orchestra/ in the repository around e's dir, asking what it needs when run in a
// terminal, and returns the exit code. It reads the form's answers from e's stdin and prints to its
// stdout and stderr. It plans what to set up from the project and the flags, asks the rest, sets up
// Beads, then applies the choice.
func runInit(ctx context.Context, args []string, e cmdEnv, stop stopCatch) int {
	dir, getenv, stdin, stdout, stderr := e.dir, e.getenv, e.stdin, e.stdout, e.stderr
	f, code, ok := parseInitFlags(args, getenv, stderr)
	if !ok {
		return code
	}
	if f.update {
		return runInitUpdate(ctx, dir, f, getenv, stdout, stderr)
	}
	fail := func(why any) int {
		fmt.Fprintln(stderr, "orchestra init:", why)
		return dispatch.ExitSetup
	}
	repo, existing, err := openInitRepo(ctx, dir)
	if err != nil {
		return fail(err)
	}
	interactive := isTerminal(stdin) && isTerminal(stdout)
	choice, ask, err := planInit(ctx, repo, f, existing, getenv, interactive)
	if err != nil {
		return fail(err)
	}

	ui := tui.NewInitScreen(stdout, getenv("TERM"))
	ui.Header(repo)
	scout := organ.Client{Bin: "claude", Model: getenv("ORGAN_MODEL"), Effort: scoutEffort(getenv, existing.OrganEffort)}
	// A stop signal, caught as init's stopPolicy says, cancels stop.ctx. While the form asks, it cancels
	// the form as Esc does: Bubble Tea's own handler would take SIGTERM for a submitted form. After it,
	// init stops at the end of the step under way, saying what it did; without the catch, Go would end
	// init with no word between two of the files it writes.
	stopCtx := stop.ctx
	spent, err := askOrDefault(stopCtx, stdin, stdout, getenv("TERM"), interactive, repo, &choice, ask, scout)
	signalled := stop.stopped()
	steps := scoutSteps(spent)
	if err != nil || signalled {
		ui.Steps(steps)
		ui.Cancelled()
		if signalled {
			return dispatch.ExitInterrupted
		}
		return dispatch.ExitSetup
	}
	// Checked again once the form is answered, which can take a while: a run may have started since.
	// From here init holds the lock, so that no run starts while it changes the project.
	lock, refusal := lockInit(ctx, repo)
	if refusal != "" {
		ui.Steps(steps)
		return fail(refusal)
	}
	defer func() { _ = lock.Close() }() // only releases it; init exiting would too
	did, halt := runSteps(stopCtx, initSteps(repo, choice, f.force, getenv, ui.Working))
	if steps = append(steps, did...); halt != nil {
		return initHalted(ui, stderr, steps, halt)
	}
	needs := initRequires(stopCtx, repo, ui.Working)
	if stop.stopped() {
		ui.Steps(steps)
		fmt.Fprintln(stderr, "orchestra init: stopped while checking the project's requirements")
		return dispatch.ExitInterrupted
	}
	signOff(ctx, ui, repo, steps, needs, choice, existing.WorkerEffort, getenv)
	return dispatch.ExitOK
}

// initRequires checks the requirements of the project at repo, as settings.json has them once init has
// written it, as a run does as it starts (installing what it can), and returns a step for each, for
// init's list of what the machine needs: one missing says its manual step, or why its install failed
// and where its output is. A list a run would refuse is a missing step saying why. working says what
// runs.
func initRequires(ctx context.Context, repo string, working func(string)) []project.Step {
	settings, _, err := project.LoadSettings(repo)
	var list []project.Requirement
	if err == nil {
		list, err = project.ResolveRequires(settings)
	}
	var limit time.Duration
	if err == nil {
		limit, err = project.ResolveCheckTimeout(project.Given[time.Duration]{}, settings)
	}
	if err != nil {
		return []project.Step{{Kind: project.StepMissing, Label: "requires", Detail: err.Error()}}
	}
	report := requires.Check(ctx, list, requires.Options{Dir: repo, InstallLimit: limit, Working: working})
	steps := make([]project.Step, 0, len(report))
	for _, res := range report {
		s := project.Step{Kind: project.StepDone, Label: res.Name}
		switch res.State {
		case requires.Installed:
			s.Detail = res.Says()
		case requires.Missing:
			s.Kind, s.Detail = project.StepMissing, res.Why()
		case requires.OtherOS:
			s.Kind, s.Detail = project.StepKept, res.Says()
		case requires.Unchecked:
			s.Kind, s.Detail = project.StepCaution, res.Says()
		}
		steps = append(steps, s)
	}
	return steps
}

// initSteps are the steps init takes to set up the repository at repo as the choice says, once it is
// answered: Beads, then .orchestra/ and the rest Apply writes (anew where force is), then the test
// work. working says what a step under way is doing.
func initSteps(
	repo string, c project.Choice, force bool, getenv func(string) string, working func(string),
) []setupStep {
	return []setupStep{
		{name: "Beads", run: func(ctx context.Context) ([]project.Step, error) {
			return setUpBeads(ctx, repo, c, getenv, working), nil
		}},
		// Apply writes the whole setup once it starts, or nothing where the stop came first.
		{name: "the setup", run: func(ctx context.Context) ([]project.Step, error) {
			return project.Apply(ctx, repo, c, force)
		}, stopped: func(took []project.Step) string {
			switch {
			case took == nil:
				return " before writing .orchestra/"
			case c.FilesTestWork():
				return " once it had written the files above, before filing the test work: orchestra init again files it"
			}
			return " once it had written the files above"
		}},
		{name: "the test work", run: func(ctx context.Context) ([]project.Step, error) {
			return fileTestWork(ctx, repo, c), nil
		}, stopped: func([]project.Step) string {
			return " while filing the test work: orchestra init again files what is missing"
		}},
	}
}

// initHalted reports where halt ended init's setup, after the steps so far, and returns the exit code:
// a stop signal stopped init, or a step failed.
func initHalted(ui tui.InitScreen, stderr io.Writer, steps []project.Step, halt *setupHalt) int {
	ui.Steps(steps)
	if halt.stop {
		fmt.Fprintln(stderr, "orchestra init: stopped"+halt.words)
		return dispatch.ExitInterrupted
	}
	fmt.Fprintln(stderr, "orchestra init:", halt.err)
	return dispatch.ExitSetup
}

// openInitRepo finds the repository around dir that init sets up, and reads its settings. The error
// is why init doesn't set it up.
func openInitRepo(ctx context.Context, dir string) (repo string, existing project.Settings, err error) {
	if repo, err = (git.Git{}).TopLevel(ctx, dir); err != nil {
		return "", existing, errors.New("not inside a git repository")
	}
	if refusal := initRefusal(ctx, repo); refusal != "" {
		return "", existing, errors.New(refusal)
	}
	existing, _, err = project.LoadSettings(repo)
	return repo, existing, err
}

// signOff prints init's summary of the repository at repo: its steps, what orchestra needs and the
// project's requirements (needs, see initRequires), and what is left for the user, workerEffort being
// the project's worker_effort.
func signOff(
	ctx context.Context, ui tui.InitScreen, repo string, steps, needs []project.Step, c project.Choice,
	workerEffort string, getenv func(string) string,
) {
	pre := append(project.Prerequisites(ctx, repo, getenv, onHost(ctx)), needs...)
	ui.Steps(steps)
	ui.Prerequisites(pre)
	ui.Next(project.NextSteps(ctx, repo, steps, pre, c, workerEffort, onHost(ctx)))
	ui.SignOff(!slices.ContainsFunc(pre, func(p project.Step) bool { return p.Kind == project.StepMissing }))
}

// initFlags are orchestra init's flags, each with whether the command line gives it.
type initFlags struct {
	checkFast, checkFull, setup, mcp, prefix project.Given[string]
	fastTimeout, fullTimeout                 project.Given[time.Duration]
	concurrent                               project.Given[int]
	union, install, verifier                 project.Given[bool]
	force                                    bool
	update                                   bool   // --update: bring the worker prompt and verifier up to date alone
	agent                                    string // the workers' agent kind, AGENT_KIND's by default
}

// initSynopsis is orchestra init's flags, as its usage line gives them (see subcommand).
const initSynopsis = "[--check-fast \"<command>\"] [--check-full \"<command>\"] " +
	"[--check-fast-timeout D] [--check-full-timeout D]\n" +
	"[--setup \"<command>\"] [--concurrent N] [--mcp names] [--changelog-union]\n" +
	"[--install-beads] [--verifier] [--prefix name] [--agent kind] [--force]\n" +
	"[--update]"

// initAbout is what orchestra init -h says between its usage line and its flags.
const initAbout = "Set up Beads and .orchestra/ in this repository. Where bd is missing, it offers to install it\n" +
	"(with Homebrew, or the Beads install script); where Beads isn't set up, it runs bd init. Then\n" +
	"the worker prompt (from the built-in template, or moved from .claude/worker-prompt.md), the\n" +
	"checks' runners (scripts/check-fast.sh, the merge check, and scripts/check-full.sh, run once\n" +
	"at the end of a run; one that is there and differs is kept unless its flag is given),\n" +
	"settings.json (the checks, their time limits, how many tickets run at the same time and\n" +
	"the MCP servers workers get, by name), a .gitignore for the log, reports and per-ticket\n" +
	"files, and a check of what orchestra needs and of the project's requirements (its 'requires'\n" +
	"list, installing what it can, as each run does), written into README.md, where there is one,\n" +
	"as its section on what to install. Where the project keeps a CHANGELOG.md, it\n" +
	"offers to merge it by union in .gitattributes; where it has a lockfile and no setup command,\n" +
	"it offers one (npm ci for package-lock.json, …). It offers a verifier, a subagent that checks\n" +
	"each ticket's change in a fresh context, which the worker prompt then has workers run, and the\n" +
	"ticket prefix, renaming every ticket when it changes. In a\n" +
	"terminal it asks for anything the flags don't give. Run it from the main checkout, between\n" +
	"runs: it changes nothing in a linked worktree, or while a run holds the repository's lock.\n" +
	"With --update, it does nothing else but bring the worker prompt and the verifier up to date\n" +
	"with orchestra's templates: it rewrites the sections the templates changed since they were\n" +
	"written, keeping the rest, and shows the diff to commit.\n\n"

// parseInitFlags reads init's flags in args, --agent defaulting to AGENT_KIND. ok is false where
// init stops there with code: after -h, or once it has told stderr what is wrong with them.
func parseInitFlags(args []string, getenv func(string) string, stderr io.Writer) (f initFlags, code int, ok bool) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	e := &envFlags{fs: fs, getenv: getenv}
	bindGiven(e, fs.StringVar, &f.checkFast, "check-fast", "", "", parseText, "the command "+project.FastRunner+
		" runs, orchestra's merge check (lint, build, the fast tests); \"\" for a check that passes at once "+
		"(asked when omitted)").alias("check")
	bindGiven(e, fs.StringVar, &f.checkFull, "check-full", "", "", parseText, "the slower suites' command, which "+
		project.FullRunner+" runs after check-fast.sh, once at the end of a run; \"\" for none")
	bindGiven(e, fs.DurationVar, &f.fastTimeout, "check-fast-timeout", "", 0, project.ParseCheckTimeout,
		"how long check-fast may run on a ticket before it merges, e.g. 5m (default "+
			project.DefaultCheckTimeoutText+")").alias("check-timeout").check = positive(&f.fastTimeout.Value)
	bindGiven(e, fs.DurationVar, &f.fullTimeout, "check-full-timeout", "", 0, project.ParseCheckTimeout,
		"how long check-full may run at the end of a run, e.g. 90m (default "+
			project.DefaultCheckFullTimeoutText+")").check = positive(&f.fullTimeout.Value)
	bindGiven(e, fs.StringVar, &f.setup, "setup", "", "", parseText, "the command that installs the dependencies "+
		"in a worktree, run in each new one before its worker starts and before check-fast when a rebase changes a "+
		"lockfile, e.g. \"npm ci\"; \"\" for none (offered for a lockfile when omitted; an existing setting is kept)")
	fs.BoolVar(&f.force, "force", false, "replace an existing .orchestra/worker-prompt.md with the template")
	fs.BoolVar(&f.update, "update", false, "only rewrite the sections of the worker prompt and "+project.VerifierPath+
		" that orchestra's templates changed since they were written, keeping the rest, and show the diff; "+
		"no other flag goes with it")
	bindGiven(e, fs.BoolVar, &f.union, "changelog-union", "", false, parseOnOff, "add 'CHANGELOG.md merge=union' "+
		"to .gitattributes, so two tickets' changelog entries don't conflict (asked when omitted; =false declines)")
	bindGiven(e, fs.StringVar, &f.mcp, "mcp", "", "", parseText, "the MCP servers workers get, by name, "+
		"comma-separated, e.g. postgres,firecrawl; \"\" for none (asked when omitted)")
	bindGiven(e, fs.BoolVar, &f.install, "install-beads", "", false, parseOnOff, "install Beads (bd) where it is "+
		"missing, with Homebrew or the Beads install script (asked when omitted; =false declines)")
	bindGiven(e, fs.BoolVar, &f.verifier, "verifier", "", false, parseOnOff, "write "+project.VerifierPath+
		", a subagent that checks each ticket's change against the ticket in a fresh context, and have the worker "+
		"prompt run it before a worker closes its ticket (asked when omitted; =false declines)")
	bindGiven(e, fs.StringVar, &f.prefix, "prefix", "", "", parseText, "the Beads ticket prefix, IDs reading "+
		"<prefix>-<hash>: lowercase letters, digits and hyphens, starting with a letter; for a new Beads bd init "+
		"takes it, and an existing one's tickets are all renamed to it with bd rename-prefix (asked when omitted; "+
		"kept when not given)")
	bindEnv(e, fs.StringVar, &f.agent, "agent", "AGENT_KIND", agentkind.Default.Name, parseText,
		"the workers' agent kind, whose skill folder gets the skills test work needs: .claude/skills for claude, "+
			".agents/skills for codex [AGENT_KIND]")
	concurrent := bindGiven(e, fs.IntVar, &f.concurrent, "concurrent", "", 0, parseWhole,
		"tickets to run at the same time by default (asked when omitted)")
	concurrent.short("c")
	concurrent.check = func(name string) string {
		if err := project.ConcurrencyRange(f.concurrent.Value); err != nil {
			return name + " " + err.Error()
		}
		return ""
	}
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), usageLines("Usage: ", "init", initSynopsis)+"\n"+initAbout)
		printFlags(fs)
	}
	if code, ok := parseArgs(fs, args, "init"); !ok {
		return f, code, false
	}
	if _, problems := e.settle(); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(stderr, "orchestra init:", p)
		}
		return f, dispatch.ExitSetup, false
	}
	return f, dispatch.ExitOK, true
}

// positive is the check of a time limit's flag, given as name: init takes no limit of 0, as a run doesn't.
func positive(d *time.Duration) func(name string) string {
	return func(name string) string {
		if err := project.PositiveCheckTimeout(*d); err != nil {
			return name + " " + err.Error()
		}
		return ""
	}
}

// planInit is what init would set up in repo, whose settings are existing: the choice, from the
// project as it is and the flags, and the questions left to ask, those the flags don't answer, with
// what the form needs for them in a terminal. It reads the ticket prefix only where it would ask
// for it or --prefix gives one: that takes a few bd calls. The error is why init can't go on.
func planInit(
	ctx context.Context, repo string, f initFlags, existing project.Settings, getenv func(string) string,
	interactive bool,
) (project.Choice, tui.Ask, error) {
	c := initChoice(repo, f, existing)
	c.Servers, c.ServersErr = mcp.Discover(mcp.UserConfig(getenv), project.ConfigRoots(ctx, repo)...)
	c.SetupOffer, c.SetupFrom = project.FindSetup(repo)
	c.Install = project.FindBeadsInstall(ctx, runtime.GOOS)
	bd, onPath := project.LocateBd(ctx, getenv)
	q := project.Questions{
		// Stage 2's choice of checks, unless a flag gives one of them.
		Check:      !f.checkFast.Given && !f.checkFull.Given,
		Concurrent: !f.concurrent.Given,
		MCP:        !f.mcp.Given,
		Setup:      !f.setup.Given && c.Setup == "" && c.SetupOffer != "",
		Union:      !f.union.Given && project.OffersUnion(ctx, repo),
		Install:    !f.install.Given && bd == "" && c.Install.Command != "",
		Verifier:   !f.verifier.Given && project.OffersVerifier(repo, c.Agent),
	}
	// Offered as yes.
	c.Union, c.InstallBeads, c.Verifier = f.union.Value || q.Union, f.install.Value || q.Install,
		f.verifier.Value || q.Verifier
	// The ticket prefix, asked where bd is there or init offers to install it.
	q.Prefix = interactive && !f.prefix.Given && (onPath || q.Install || f.install.Value)
	if err := planPrefix(ctx, repo, f.prefix, &c, &q.Prefix); err != nil {
		return c, tui.Ask{}, err
	}
	ask := tui.Ask{Questions: q}
	if interactive && q.Check {
		ask.CheckScript = regularFile(filepath.Join(repo, project.CheckScript))
		var err error
		if ask.Runners, err = project.PlanRunners(repo, project.Choice{}); err != nil { // as they are
			return c, ask, err
		}
		if ask.Skill, err = project.PlanSkill(repo, c.Agent); err != nil {
			return c, ask, err
		}
	}
	return c, ask, nil
}

// initChoice is the choice init starts from in repo: the project's settings, existing, and its worker
// prompt, with the flags given over them.
func initChoice(repo string, f initFlags, existing project.Settings) project.Choice {
	prompt, _ := os.ReadFile(project.Locate(repo).Prompt) // no prompt yet: no check command to find in it
	c := project.DefaultChoice(existing, string(prompt))
	switch {
	case f.checkFast.Given:
		c.Fast = project.RunnerSuites(f.checkFast.Value, "--check-fast", project.FastRunner)
		c.ReplaceFast, c.CheckFrom = true, "--check-fast"
	case c.Fast != nil && len(*c.Fast) == 0 && regularFile(filepath.Join(repo, project.CheckScript)):
		// The project's own check, called as it is.
		c.Fast = &[]project.Suite{{Name: "the project's own check", Command: project.CheckScript,
			FoundIn: project.CheckScript}}
		c.CheckFrom = project.CheckScript
	}
	if f.checkFull.Given {
		c.Full = project.RunnerSuites(f.checkFull.Value, "--check-full", project.FullRunner)
		c.ReplaceFull = true
	}
	if f.concurrent.Given {
		c.Concurrent, c.Unasked.Concurrent = f.concurrent.Value, false
	}
	if f.fastTimeout.Given {
		c.CheckFastTimeout, c.ReplacedTimeout = command.ShortDuration(f.fastTimeout.Value), ""
	}
	if f.fullTimeout.Given {
		c.CheckFullTimeout, c.ReplacedFullTimeout = command.ShortDuration(f.fullTimeout.Value), ""
	}
	if f.mcp.Given {
		names := mcp.ParseNames(f.mcp.Value)
		c.MCP = &names
	}
	if f.setup.Given {
		c.Setup = strings.TrimSpace(f.setup.Value)
	}
	c.Agent = f.agent
	return c
}

// planPrefix reads the ticket prefix into the choice where init asks for it (*ask) or --prefix, flag,
// gives one, and takes --prefix's. Where it can't read the prefix, init doesn't ask for it: it can't
// tell what it would change. The error is why it can't take --prefix.
func planPrefix(ctx context.Context, repo string, flag project.Given[string], c *project.Choice, ask *bool) error {
	if !*ask && !flag.Given {
		return nil
	}
	if err := readPrefix(ctx, repo, c); err != nil {
		if flag.Given {
			return fmt.Errorf("--prefix: can't read the ticket prefix Beads has: %w", err)
		}
		*ask = false
		c.Prefix, c.PrefixWas = "", ""
		return nil
	}
	if problem := takePrefix(flag, c); problem != "" {
		return errors.New(problem)
	}
	return nil
}

// askOrDefault asks the questions ask names in a terminal (interactive), which TERM names termName,
// with the scout looking for the suites of the project in repo, and returns what the scout's calls
// cost; the error is the form's, cancelled by Esc or by the end of ctx. Outside a terminal it takes the
// questions as unasked (see Choice.DeclineUnasked).
func askOrDefault(
	ctx context.Context, in io.Reader, out io.Writer, termName string, interactive bool, repo string,
	c *project.Choice, ask tui.Ask, scout organ.Client,
) (spent []organ.Spend, err error) {
	if !interactive || !ask.Any() {
		c.DeclineUnasked(ask.Questions)
		return nil, nil
	}
	if ask.Check {
		// By the scout's goroutine, which AskInit waits for before it returns.
		scout.Spent = func(s organ.Spend) { spent = append(spent, s) }
		// The form runs the scout under a context made from ctx, with the environment ctx gives commands.
		ask.Scout = func(scoutCtx context.Context) (organ.Scouting, error) { return scout.Scout(scoutCtx, repo) }
	}
	err = tui.AskInit(ctx, in, out, termName, c, ask)
	return spent, err
}

// setUpBeads installs bd and sets Beads up as the choice says, then changes the ticket prefix where it
// does, and returns the steps; where ctx ends, which Ctrl+C does, it stops there. Beads goes first:
// bd init commits what was staged, and init stages a moved worker prompt; with the user's own changes
// staged, it doesn't run. Ctrl+C stops an install or bd init under way, which run in their own process
// groups, and init with it.
func setUpBeads(
	ctx context.Context, repo string, c project.Choice, getenv func(string) string, working func(string),
) []project.Step {
	steps := project.SetUpBeads(ctx, repo, c, getenv, working)
	if ctx.Err() != nil {
		return steps
	}
	refusal := ""
	if !c.PrefixNew && c.PrefixWas != "" && c.Prefix != c.PrefixWas {
		refusal = prefixRefusal(ctx, repo, c.PrefixWas) // read again: the form can take a while
	}
	if s, ok := project.ApplyPrefix(ctx, repo, c, refusal); ok {
		steps = append(steps, s)
	}
	return steps
}

// takePrefix takes --prefix's value, where flag gives one, into the choice, and returns "", or why it
// refuses it: not a prefix bd takes, or a rename of the tickets they can't have now. The flag consents
// to the rename.
func takePrefix(flag project.Given[string], c *project.Choice) string {
	p := project.NormalPrefix(flag.Value)
	if !flag.Given || p == c.PrefixWas {
		return ""
	}
	if problem := project.PrefixProblem(p); problem != "" {
		return "--prefix " + problem
	}
	if !c.PrefixNew && c.PrefixRefusal != "" {
		return fmt.Sprintf("--prefix: the tickets can't be renamed from %s to %s while %s. Nothing was changed.",
			c.PrefixWas, p, c.PrefixRefusal)
	}
	c.Prefix, c.RenamePrefix = p, true
	return ""
}

// initRefusal is why init doesn't set up the repository whose checkout is repo, or "" when it may. Not
// in a linked worktree: what it writes would land on that worktree's branch, a worker's ticket's, rather
// than on the main checkout's. And not while a run holds the lock: the runners and .gitattributes it
// rewrites leave the run's tree dirty, which stops the run at its next dispatch or merge, and the run
// would pick up the tickets it files.
func initRefusal(ctx context.Context, repo string) string {
	if p := mainCheckoutProblem(ctx, repo); p != "" {
		return p
	}
	holder, held, err := project.RunHolder(ctx, repo)
	switch {
	case errors.Is(err, errors.ErrUnsupported): // no run lock on this system: nothing to check
	case err != nil:
		return "cannot tell whether a run is going: " + err.Error()
	case held:
		return heldRefusal(&project.HeldError{Repo: repo, Holder: holder})
	}
	return ""
}

// lockInit takes the run lock of the repository whose main checkout is repo for init, and returns it,
// or why init doesn't set the repository up, as initRefusal says. Held until init ends, it keeps a run
// that starts meanwhile from working on tickets init renames, in a tree init writes into, and from
// taking up the tickets init files. A nil lock, where the system has none, holds nothing.
func lockInit(ctx context.Context, repo string) (*project.RunLock, string) {
	if p := mainCheckoutProblem(ctx, repo); p != "" {
		return nil, p
	}
	lock, err := project.LockRun(ctx, repo, project.Holder{Init: true, PID: os.Getpid()})
	var held *project.HeldError
	switch {
	case errors.As(err, &held):
		return nil, heldRefusal(held)
	case errors.Is(err, errors.ErrUnsupported): // no run lock on this system: nothing to hold
		return nil, ""
	case err != nil:
		return nil, "cannot take the run lock: " + err.Error()
	}
	return lock, ""
}

// heldRefusal is why init doesn't set up a repository whose lock held names the holder of.
func heldRefusal(held *project.HeldError) string {
	return fmt.Sprintf("%v. init changes files and tickets a run works from, which would stop it: "+
		"set up between runs.", held)
}

// scoutEffort is the effort the scout runs at: ORGAN_EFFORT's, else settings.json's organ_effort, checked
// as a run checks them. One that isn't an effort would reach claude --effort and fail the scout:
// the scout runs at its own instead, and a run says what is wrong with it.
func scoutEffort(getenv func(string) string, setting string) string {
	given := project.Given[string]{Value: getenv("ORGAN_EFFORT"), Name: "ORGAN_EFFORT"}
	effort, err := project.ResolveEffort(given, "organ_effort", setting)
	if err != nil {
		return ""
	}
	return effort
}

// scoutSteps say what the scout's call cost, as claude answered it: init has no log for an ORGAN
// line. One that ended in an error is to watch.
func scoutSteps(spent []organ.Spend) []project.Step {
	var steps []project.Step
	for _, s := range spent {
		kind := project.StepDone
		if s.Subtype != "" && s.Subtype != organ.SubtypeSuccess {
			kind = project.StepCaution
		}
		steps = append(steps, project.Step{Kind: kind, Label: "scout", Detail: "cost " + s.Cost()})
	}
	return steps
}

// regularFile reports whether p is a file.
func regularFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// isTerminal reports whether f is a terminal.
func isTerminal(f any) bool {
	fd, ok := f.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(fd.Fd()))
}
