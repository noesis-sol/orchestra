package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// envInt reads a whole-number variable, or def when it is unset. For anything else it returns def
// and the problem, which only matters when no flag overrides the variable.
func envInt(getenv func(string) string, name string, def int) (int, string) {
	v := getenv(name)
	if v == "" {
		return def, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def, fmt.Sprintf("%s must be a whole number (got '%s').", name, v)
	}
	return n, ""
}

// envBool reads an on-or-off variable: 1, true, yes or on for on, 0, false, no or off for off, in any
// case, or def when it is unset. Like envInt, anything else is returned as a problem.
func envBool(getenv func(string) string, name string, def bool) (bool, string) {
	v := getenv(name)
	switch strings.ToLower(v) {
	case "":
		return def, ""
	case "1", "true", "yes", "on":
		return true, ""
	case "0", "false", "no", "off":
		return false, ""
	}
	return def, fmt.Sprintf("%s must be 1 or 0, true or false, yes or no, or on or off (got '%s').", name, v)
}

// envDuration reads a duration variable such as 2h, which may be 0 unless positive is set; given is
// false when it is unset. Like envInt, anything else is returned as a problem.
func envDuration(getenv func(string) string, name string, positive bool) (d time.Duration, given bool, problem string) {
	v := getenv(name)
	if v == "" {
		return 0, false, ""
	}
	d, err := time.ParseDuration(v)
	switch {
	case positive && (err != nil || d <= 0):
		return 0, false, fmt.Sprintf("%s must be a positive duration such as 5m (got '%s').", name, v)
	case err != nil || d < 0:
		return 0, false, fmt.Sprintf("%s must be a duration such as 2h, or 0 for none (got '%s').", name, v)
	}
	return d, true, ""
}

func envOr(getenv func(string) string, name, def string) string {
	if v := getenv(name); v != "" {
		return v
	}
	return def
}

// options is a run's configuration: the loop's, and what only the command uses.
type options struct {
	dispatch.Config
	WorkerPrompt string
	ownPrompt    bool // WorkerPrompt came from -prompt or WORKER_PROMPT, not the project
	notSetUp     bool // the project was never set up (see project.IsSetUp): run says to run orchestra init first
	Notify       bool
	Plain        bool
	Triage       bool   // triage organ on each deferred ticket
	Review       bool   // reviewer organ when the loop stops
	OrganModel   string // model for the organs; "" uses the claude CLI's default
	OrganEffort  string // effort for every organ; "" gives each its own
	WorkerEffort string // effort for Claude workers; "" is Claude Code's default
	Yes          bool   // file a --feature plan without asking
	Tickets      bool   // run the current tickets without asking what to work on (see asksWork)
	showVersion  bool
	// the terminal asked for (--terminal, ORCHESTRA_TERMINAL), which Terminal resolves; "" leaves it to the project
	terminalAsked string
	where         runsIn // where orchestra itself runs, as checkTerminal finds it
}

// runsIn is where orchestra itself runs in the run's terminal: only the terminal checks set it.
type runsIn struct {
	pane string // orchestra's own Herdr or tmux pane, or cmux surface; "" when it runs in none
	// orchestra's own cmux workspace (CMUX_WORKSPACE_ID), which the run never closes; "" outside cmux
	cmuxWorkspace string
	inSight       bool // the workers' tabs are where orchestra runs, for the dashboard's number keys to go to
}

// errUnexpectedArgs is loadConfig's error for positional arguments: a run takes none.
var errUnexpectedArgs = errors.New("unexpected arguments")

// loadConfig reads the flags in args (defaulting to the environment variables orchestrate.sh used)
// and collects every setup problem, so they can be reported together. The error is the flag
// package's (flag.ErrHelp after -h, or a malformed flag), or errUnexpectedArgs for a positional
// argument such as 'init' after a flag; both have been reported to output.
//
// Its phases, in the order their problems are reported: the flags; the repository; the terminal,
// under the project's settings, read once here for it and the project; the commands a run calls;
// then, in a repository, the project's settings and layout and its checkout.
func loadConfig(
	ctx context.Context, args []string, getenv func(string) string, output io.Writer,
) (options, []string, error) {
	c, given, problems, err := readFlags(args, getenv, output)
	if err != nil || c.showVersion {
		return c, nil, err
	}
	problems = append(problems, findRepo(ctx, &c)...)
	settings, unreadable := loadSettings(c.Repo)
	problems = append(problems, checkTerminal(ctx, &c, settings, getenv)...)
	problems = append(problems, checkTools()...)
	if c.Repo != "" {
		problems = append(problems, unreadable...)
		problems = append(problems, resolveProject(ctx, &c, given, settings, getenv)...)
		problems = append(problems, checkCheckout(ctx, &c)...)
	}
	return c, problems, nil
}

// overrides says which of the project's settings a flag or variable overrides where its value
// can't tell: a concurrency or duration of 0, -resolve-conflicts=true.
type overrides struct {
	concurrency, ticketLimit, checkTimeout, checkFullTimeout, resolveConflicts bool
}

// readFlags parses the flags in args, each defaulting to its environment variable, and reconciles
// the two: a variable's problem counts only when no flag overrides it. Its error is loadConfig's.
func readFlags(args []string, getenv func(string) string, output io.Writer) (options, overrides, []string, error) {
	var c options
	var problems []string
	fs := flag.NewFlagSet("orchestra", flag.ContinueOnError)
	fs.SetOutput(output)

	fs.StringVar(&c.Workspace, "workspace", "",
		"Herdr workspace or tmux session for the worker tabs (default: the one orchestra runs in, "+
			"or under tmux outside it orchestra-<repository folder>; list Herdr's with: herdr workspace list); "+
			"none under cmux, where each worker has a workspace of its own")
	fs.StringVar(&c.terminalAsked, "terminal", getenv("ORCHESTRA_TERMINAL"),
		"what workers run in: herdr, tmux, cmux, or auto for the one orchestra runs in, else tmux "+
			"(default: .orchestra/settings.json, else auto) [ORCHESTRA_TERMINAL]")
	limit, limitProblem := envInt(getenv, "LIMIT", 40)
	fs.IntVar(&c.Limit, "limit", limit, "stop after this many tickets in total [LIMIT]")
	doneSoFar, doneSoFarProblem := envInt(getenv, "DONE_SO_FAR", 0)
	fs.IntVar(&c.DoneSoFar, "done-so-far", doneSoFar,
		"tickets dispatched in earlier runs, counted toward -limit [DONE_SO_FAR]")
	fs.StringVar(&c.AgentKind, "agent", envOr(getenv, "AGENT_KIND", "claude"),
		"agent kind for the workers: claude, or another Herdr agent kind (Herdr only) [AGENT_KIND]")
	fs.StringVar(&c.WorkerPrompt, "prompt", getenv("WORKER_PROMPT"),
		"worker instructions with TICKET_ID as placeholder (default: .orchestra/worker-prompt.md, "+
			"or .claude/worker-prompt.md in a project set up before 'orchestra init') [WORKER_PROMPT]")
	notify, notifyProblem := envBool(getenv, "NOTIFY", true)
	fs.BoolVar(&c.Notify, "notify", notify,
		"macOS notifications for tickets closed, set aside or waiting on you, and the run's end [NOTIFY=0 turns off]")
	fs.StringVar(&c.WTRoot, "worktrees", getenv("WT_ROOT"),
		"folder for the per-ticket worktrees, outside the repository "+
			"(default: <repo>-worktrees next to it) [WT_ROOT]")
	triage, triageProblem := envBool(getenv, "TRIAGE", true)
	fs.BoolVar(&c.Triage, "triage", triage,
		"have the triage organ note a recommendation on each deferred ticket [TRIAGE=0 turns off]")
	review, reviewProblem := envBool(getenv, "REVIEW", true)
	fs.BoolVar(&c.Review, "review", review,
		"have the reviewer organ write a run report when the loop stops [REVIEW=0 turns off]")
	fs.StringVar(&c.OrganModel, "organ-model", getenv("ORGAN_MODEL"),
		"model for the organs: triage, the predictor and the run report (default: the claude CLI's default) [ORGAN_MODEL]")
	fs.StringVar(&c.OrganEffort, "organ-effort", getenv("ORGAN_EFFORT"),
		"effort for every organ: low, medium, high, xhigh or max (default: .orchestra/settings.json, "+
			"else low for triage, the predictor and screening, medium for the run report, high for planning) [ORGAN_EFFORT]")
	fs.StringVar(&c.WorkerEffort, "worker-effort", getenv("WORKER_EFFORT"),
		"effort Claude workers start at: low, medium, high, xhigh or max "+
			"(default: .orchestra/settings.json, else Claude Code's default) [WORKER_EFFORT]")
	concurrent, concurrentProblem := envInt(getenv, "ORCHESTRA_CONCURRENT", 0)
	concurrentGiven := getenv("ORCHESTRA_CONCURRENT") != "" && concurrentProblem == ""
	fs.IntVar(&c.Concurrency, "concurrent", concurrent,
		"tickets to work on at the same time (default: .orchestra/settings.json, else 1) [ORCHESTRA_CONCURRENT]")
	fs.IntVar(&c.Concurrency, "c", concurrent, "shorthand for --concurrent")
	ticketLimit, ticketLimitGiven, ticketLimitProblem := envDuration(getenv, "TICKET_LIMIT", false)
	fs.DurationVar(&c.TicketLimit, "ticket-limit", ticketLimit,
		"stop the run when a ticket's worker is still going this long after dispatch, e.g. 2h; 0 for none "+
			"(default: .orchestra/settings.json, else none) [TICKET_LIMIT]")
	checkTimeout, checkTimeoutGiven, checkTimeoutProblem := envDuration(getenv, "ORCHESTRA_CHECK_TIMEOUT", true)
	fs.DurationVar(&c.CheckTimeout, "check-timeout", checkTimeout,
		"stop the merge check (check-fast) on a rebased ticket after this long and set the ticket aside, e.g. 5m "+
			"(default: .orchestra/settings.json, else 30m) [ORCHESTRA_CHECK_TIMEOUT]")
	checkFullTimeout, checkFullTimeoutGiven, checkFullTimeoutProblem := envDuration(getenv,
		"ORCHESTRA_CHECK_FULL_TIMEOUT", true)
	fs.DurationVar(&c.CheckFullTimeout, "check-full-timeout", checkFullTimeout,
		"stop the full check (check-full), run once at the end of a run, after this long, e.g. 90m "+
			"(default: .orchestra/settings.json, else 60m) [ORCHESTRA_CHECK_FULL_TIMEOUT]")
	fs.StringVar(&c.Ticket, "ticket", getenv("ORCHESTRA_TICKET"),
		"work on this ticket and its subtickets only, each parent after its children; "+
			"nothing else is started [ORCHESTRA_TICKET]")
	fs.StringVar(&c.Feature, "feature", "",
		"screen and plan this feature request as an epic and its tickets, file them in Beads once you "+
			"confirm, and run the epic as --ticket would")
	fs.BoolVar(&c.Yes, "yes", false, "with --feature, file the plan without asking")
	tickets, ticketsProblem := envBool(getenv, "ORCHESTRA_TICKETS", false)
	fs.BoolVar(&c.Tickets, "tickets", tickets,
		"run the current tickets without first asking, in a terminal, whether to plan a new feature instead "+
			"[ORCHESTRA_TICKETS=1]")
	fs.BoolVar(&c.ResolveConflicts, "resolve-conflicts", true,
		"when a finished ticket's rebase onto work merged while it ran stops on conflicts, "+
			"ask its worker to resolve them before setting it aside; needs a check command "+
			"(default: .orchestra/settings.json, else on)")
	launchPrompt, launchPromptProblem := envBool(getenv, "PROMPT_AT_LAUNCH", true)
	fs.BoolVar(&c.LaunchPrompt, "prompt-at-launch", launchPrompt,
		"start Claude workers with their prompt instead of pasting it in [PROMPT_AT_LAUNCH=0 turns off]")
	fs.BoolVar(&c.showVersion, "version", false, "print the version and exit")
	fs.BoolVar(&c.showVersion, "v", false, "shorthand for --version")
	fs.BoolVar(&c.Plain, "plain", false,
		"print plain log lines instead of the interactive view (automatic when not on a terminal)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra [flags]\n"+
			"       orchestra init [--check-fast \"<command>\"] [--check-full \"<command>\"] [--concurrent N] "+
			"[--mcp names] [--force]\n"+
			"       orchestra --feature \"<request>\" [--yes] [flags]\n"+
			"       orchestra plan [--apply]\n"+
			"       orchestra update [--check] [--force]\n\n"+
			"Work through 'bd ready' (or, with -ticket, one ticket and its subtickets), as many tickets at once "+
			"as --concurrent allows, one worker (a coding agent) per tab and git worktree.\n"+
			"With --feature, plan the request as an epic and its tickets first, and run those.\n"+
			"In a terminal, without --feature, --ticket, --tickets or -plain, it first asks which: "+
			"the current tickets, or a new feature you describe.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 done, 2 setup problem, 3 worker blocked, paused or over its time, "+
			"4 terminal (Herdr, tmux or cmux), Beads or git failure,\n5 main checkout dirty or off its branch, 6 merge failed, "+
			"7 environment failing workers, 130 Ctrl+C.\n")
	}
	if err := fs.Parse(args); err != nil {
		return c, overrides{}, nil, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		switch rest[0] {
		case "init":
			fmt.Fprintln(fs.Output(), "orchestra: init comes before its flags: "+
				"orchestra init [--check-fast \"<command>\"] [--check-full \"<command>\"] [--concurrent N] [--force]")
		case "plan":
			fmt.Fprintln(fs.Output(), "orchestra: plan comes before its flags: orchestra plan [--apply]")
		case "update":
			fmt.Fprintln(fs.Output(), "orchestra: update comes before its flags: orchestra update [--check] [--force]")
		default:
			fmt.Fprintf(fs.Output(), "orchestra: unexpected argument %q (see orchestra -h)\n", rest[0])
		}
		return c, overrides{}, nil, errUnexpectedArgs
	}
	if c.showVersion {
		return c, overrides{}, nil, nil
	}

	// A variable's problem counts only when no flag overrides it; a flag is held to the same rule,
	// its value checked here when nothing else does (0 for the others: ResolveConcurrency checks
	// -concurrent and -c, ResolveTicketLimit, ResolveCheckTimeout and ResolveCheckFullTimeout their flags,
	// and a boolean flag has no value to check).
	_, updateCheckProblem := envBool(getenv, noUpdateCheck, false)
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, v := range []struct {
		flag    string
		isSet   bool
		value   int
		problem string
	}{
		{"limit", set["limit"], c.Limit, limitProblem},
		{"done-so-far", set["done-so-far"], c.DoneSoFar, doneSoFarProblem},
		{"concurrent", set["concurrent"] || set["c"], 0, concurrentProblem},
		{"ticket-limit", set["ticket-limit"], 0, ticketLimitProblem},
		{"check-timeout", set["check-timeout"], 0, checkTimeoutProblem},
		{"check-full-timeout", set["check-full-timeout"], 0, checkFullTimeoutProblem},
		{"notify", set["notify"], 0, notifyProblem},
		{"triage", set["triage"], 0, triageProblem},
		{"review", set["review"], 0, reviewProblem},
		{"tickets", set["tickets"], 0, ticketsProblem},
		{"prompt-at-launch", set["prompt-at-launch"], 0, launchPromptProblem},
		{"", false, 0, updateCheckProblem}, // no flag: the variable alone turns the check off
	} {
		if !v.isSet && v.problem != "" {
			problems = append(problems, v.problem)
		} else if v.isSet && v.value < 0 {
			problems = append(problems, fmt.Sprintf("-%s must be a whole number (got %d).", v.flag, v.value))
		}
	}
	c.Feature = strings.TrimSpace(c.Feature)
	switch {
	case set["feature"] && c.Feature == "":
		problems = append(problems, "--feature needs the request: orchestra --feature \"<what to build>\"")
	case set["feature"] && c.Ticket != "":
		problems = append(problems, "--feature can't be combined with --ticket (or ORCHESTRA_TICKET): "+
			"a feature run is scoped to the epic it files.")
	case set["feature"] && set["tickets"] && c.Tickets:
		problems = append(problems, "--feature can't be combined with --tickets: "+
			"one runs a new feature, the other the current tickets.")
	case !set["feature"] && c.Yes:
		problems = append(problems, "--yes only applies with --feature.")
	case set["feature"] && c.Limit >= 0 && c.DoneSoFar >= c.Limit:
		// The loop would end at once, the plan filed and none of it started.
		problems = append(problems, fmt.Sprintf("--feature would file a plan and run none of it: "+
			"-done-so-far (%d) has reached -limit (%d). Raise -limit [LIMIT] or lower -done-so-far [DONE_SO_FAR].",
			c.DoneSoFar, c.Limit))
	}
	return c, overrides{
		concurrency:      set["concurrent"] || set["c"] || concurrentGiven,
		ticketLimit:      set["ticket-limit"] || ticketLimitGiven,
		checkTimeout:     set["check-timeout"] || checkTimeoutGiven,
		checkFullTimeout: set["check-full-timeout"] || checkFullTimeoutGiven,
		resolveConflicts: set["resolve-conflicts"],
	}, problems, nil
}

// findRepo finds the repository orchestra runs in (c.Repo), the one around the working directory.
func findRepo(ctx context.Context, c *options) []string {
	repo, err := (git.Git{}).TopLevel(ctx, "")
	if err != nil {
		return []string{"Not inside a git repository: cd into the project first."}
	}
	c.Repo = repo
	return nil
}

// loadSettings reads the settings of the project in repo, none outside a repository ("" for repo).
// Settings it can't read are a problem, and as none they leave every choice to the flags and defaults.
func loadSettings(repo string) (project.Settings, []string) {
	if repo == "" {
		return project.Settings{}, nil
	}
	settings, _, err := project.LoadSettings(repo)
	if err != nil {
		return project.Settings{}, []string{"Unreadable settings: " + err.Error()}
	}
	return settings, nil
}

// checkTools checks that the commands a run calls, whatever its terminal, are installed.
func checkTools() []string {
	var problems []string
	for _, cmd := range []string{"bd", "git"} {
		if !installed(cmd) {
			problems = append(problems, "Required command not found: "+cmd)
		}
	}
	return problems
}

// resolveProject reads what the project in c.Repo decides, under the flags and variables in c
// that override it: its layout under .orchestra/ and its settings.
func resolveProject(
	ctx context.Context, c *options, given overrides, settings project.Settings, getenv func(string) string,
) []string {
	var problems []string
	lay := project.Locate(c.Repo)
	if c.ownPrompt = c.WorkerPrompt != ""; !c.ownPrompt {
		c.WorkerPrompt = lay.Prompt
	} else if !filepath.IsAbs(c.WorkerPrompt) {
		c.WorkerPrompt = filepath.Join(c.Repo, c.WorkerPrompt)
	}
	c.LogPath, c.ReportsDir = lay.Log, lay.Reports
	c.Check, c.CheckFull = settings.CheckFast, settings.CheckFull
	c.Setup = settings.Setup
	c.NoFootprint = settings.Footprint != nil && !*settings.Footprint
	c.WorkerArgs = mcp.ChromeArgs(settings.MCPServers)
	ok := keep(&problems, &c.WorkerEffort)(project.ResolveEffort(c.WorkerEffort, "worker_effort", settings.WorkerEffort))
	if ok && c.WorkerEffort != "" {
		c.WorkerArgs = append(c.WorkerArgs, "--effort", c.WorkerEffort)
	}
	keep(&problems, &c.OrganEffort)(project.ResolveEffort(c.OrganEffort, "organ_effort", settings.OrganEffort))
	keep(&problems, &c.Concurrency)(project.ResolveConcurrency(c.Concurrency, given.concurrency, settings))
	keep(&problems, &c.TicketLimit)(project.ResolveTicketLimit(c.TicketLimit, given.ticketLimit, settings))
	keep(&problems, &c.CheckTimeout)(project.ResolveCheckTimeout(c.CheckTimeout, given.checkTimeout, settings))
	keep(&problems, &c.CheckFullTimeout)(
		project.ResolveCheckFullTimeout(c.CheckFullTimeout, given.checkFullTimeout, settings))
	keep2(&problems, &c.ResolveConflicts, &c.ResolveTimeout)(
		project.ResolveConflictResolution(c.ResolveConflicts, given.resolveConflicts, settings))
	keep(&problems, &c.CheckHandBacks)(project.ResolveCheckHandBacks(settings))
	keep2(&problems, &c.EnvHoldCount, &c.EnvHoldWindow)(project.ResolveEnvironmentHold(settings))
	keep(&problems, &c.EnvProbe)(project.ResolveEnvironmentProbe(settings))
	keep(&problems, &c.ExcludeTypes)(project.ResolveExcludeTypes(settings))
	keep(&problems, &c.SetupFiles)(project.ResolveSetupFiles(settings))
	// Claude workers get the MCP servers the project chose, defined in this machine's Claude Code
	// config; one this machine can't give them is for the maintainer to fix before they start.
	switch {
	case settings.MCPServers == nil:
	case !c.ClaudeWorkers(): // only named: the loop says they aren't passed to such workers
		named := []mcp.Server{}
		for _, name := range *settings.MCPServers {
			named = append(named, mcp.Server{Name: name})
		}
		c.MCP = &named
	default:
		if servers, err := mcp.Discover(mcp.UserConfig(getenv), project.ConfigRoots(ctx, c.Repo)...); err != nil {
			problems = append(problems, "Cannot read Claude Code's MCP config for the workers' MCP servers: "+err.Error()+".")
		} else if chosen, err := project.ResolveMCP(*settings.MCPServers, servers); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.MCP = &chosen
		}
	}
	return problems
}

// keep returns what takes a Resolve function's results: it stores the setting in dst, or notes the
// error as a problem and leaves dst as it is. It reports whether there was no error.
func keep[T any](problems *[]string, dst *T) func(T, error) bool {
	return func(v T, err error) bool {
		if err != nil {
			*problems = append(*problems, err.Error()+".")
			return false
		}
		*dst = v
		return true
	}
}

// keep2 is keep for a Resolve function that returns two settings.
func keep2[A, B any](problems *[]string, dstA *A, dstB *B) func(A, B, error) {
	return func(a A, b B, err error) {
		if err != nil {
			*problems = append(*problems, err.Error()+".")
			return
		}
		*dstA, *dstB = a, b
	}
}

// checkCheckout checks that the project in c.Repo can be run: that it was set up (c.notSetUp), its
// worker prompt, its Beads database and the ticket --ticket scopes the run to, a main checkout on a
// branch (c.Base), and a folder for the worktrees outside it (c.WTRoot).
func checkCheckout(ctx context.Context, c *options) []string {
	var problems []string
	// A project never set up is missing what orchestra init writes or sets up, the worker prompt and
	// Beads: run says to run it, first, in place of those two problems.
	c.notSetUp = !c.ownPrompt && !project.IsSetUp(c.Repo)
	prompt := c.WorkerPrompt
	if rel, err := filepath.Rel(c.Repo, prompt); err == nil && filepath.IsLocal(rel) {
		prompt = rel
	}
	b, err := os.ReadFile(c.WorkerPrompt)
	switch {
	case c.notSetUp:
	case err != nil && c.ownPrompt:
		problems = append(problems, "Worker prompt not found: "+prompt+" (from -prompt or WORKER_PROMPT).")
	case err != nil:
		problems = append(problems, "Worker prompt not found: "+prompt+". Recreate it with: orchestra init")
	case !strings.Contains(string(b), "TICKET_ID"):
		problems = append(problems, "Worker prompt has no TICKET_ID placeholder: "+prompt)
	}
	idProblem := ""
	if c.Ticket != "" {
		if idProblem = dispatch.IDProblem(c.Ticket); idProblem != "" {
			problems = append(problems, fmt.Sprintf("Ticket %s (--ticket): %s: a ticket's ID names its worktree "+
				"folder and its branch wt/<id>. Give it a plain ID with: bd rename %s <new-id>",
				c.Ticket, idProblem, c.Ticket))
		}
	}
	if st, err := os.Stat(filepath.Join(c.Repo, ".beads")); err != nil || !st.IsDir() {
		if !c.notSetUp {
			problems = append(problems, "No Beads database in "+c.Repo+". Run: bd init")
		}
	} else if _, err := exec.LookPath("bd"); err == nil && c.Ticket != "" && idProblem == "" {
		if p := scopeProblem(ctx, beads.Tracker{Repo: c.Repo}, c.Ticket); p != "" {
			problems = append(problems, p)
		}
	}

	// Finished tickets are merged into the main checkout's branch, so run from there, on a branch.
	if p := mainCheckoutProblem(ctx, c.Repo); p != "" {
		problems = append(problems, strings.ToUpper(p[:1])+p[1:])
	}
	if base, err := (git.Git{}).CurrentBranch(ctx, c.Repo); err != nil {
		problems = append(problems, "Could not read the main checkout's branch: "+err.Error()+".")
	} else if c.Base = base; c.Base == "" {
		problems = append(problems,
			"The main checkout is on a detached HEAD. Check out the branch finished tickets should land on.")
	}

	if c.WTRoot == "" {
		c.WTRoot = filepath.Join(filepath.Dir(c.Repo), filepath.Base(c.Repo)+"-worktrees")
	}
	if !filepath.IsAbs(c.WTRoot) {
		c.WTRoot = filepath.Join(c.Repo, c.WTRoot)
	}
	if within(c.WTRoot, c.Repo) {
		problems = append(problems, fmt.Sprintf(
			"WT_ROOT (%s) must be outside the repository, or git sees the worktrees as untracked files.", c.WTRoot))
	}
	return problems
}

// mainCheckoutProblem says why repo, a checkout's top level, can't be told to be the main checkout, or
// returns "": a linked worktree's branch is a worker's ticket's, not the one finished tickets land on.
// The problem is worded for init, after "orchestra init: "; a run's setup problems start with a capital.
func mainCheckoutProblem(ctx context.Context, repo string) string {
	linked, err := (git.Git{}).LinkedWorktree(ctx, repo)
	switch {
	case err != nil:
		return "could not tell whether " + repo + " is the main checkout: " + err.Error() + "."
	case linked:
		return repo + " is a linked worktree. Run this from the main checkout."
	}
	return ""
}

// scopeProblem says why a run can't be scoped to ticket id (--ticket), or returns "": the ticket
// must exist, be open and be work rather than a question for the maintainer.
func scopeProblem(ctx context.Context, tickets interface {
	Show(ctx context.Context, id string) (dispatch.Ticket, error)
}, id string) string {
	t, err := tickets.Show(ctx, id)
	switch {
	case err != nil:
		return fmt.Sprintf("Cannot read ticket %s (--ticket): %s", id, strings.Join(strings.Fields(err.Error()), " "))
	case t.Status == dispatch.StatusClosed:
		return fmt.Sprintf(
			"Ticket %s (--ticket) is closed: nothing to run. Reopen it with: bd update %s --status open", id, id)
	case dispatch.HasLabel(t, dispatch.HumanLabel):
		return fmt.Sprintf("Ticket %s (--ticket) is a question for you (label %s), not work. "+
			"Answer it with: bd human respond %s", id, dispatch.HumanLabel, id)
	}
	return ""
}

// within reports whether path is dir or inside it, following symlinks (path need not exist yet)
// and ignoring case on macOS, whose filesystems usually do.
func within(path, dir string) bool {
	path, dir = realPath(path), realPath(dir)
	if runtime.GOOS == "darwin" {
		path, dir = strings.ToLower(path), strings.ToLower(dir)
	}
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// realPath is p with symlinks resolved as far as it exists; the missing rest is kept as written.
func realPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}
