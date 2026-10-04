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
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// version can be set at build time with -ldflags "-X main.version=…". Otherwise Go's build info
// supplies it: the tag for a build from a tagged commit or 'go install …@vX.Y.Z', a pseudo-version
// for anything after it.
var version = ""

func buildVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

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
}

// errUnexpectedArgs is loadConfig's error for positional arguments: a run takes none.
var errUnexpectedArgs = errors.New("unexpected arguments")

// loadConfig reads the flags in args (defaulting to the environment variables orchestrate.sh used)
// and collects every setup problem, so they can be reported together. The error is the flag
// package's (flag.ErrHelp after -h, or a malformed flag), or errUnexpectedArgs for a positional
// argument such as 'init' after a flag; both have been reported to output.
func loadConfig(
	ctx context.Context, args []string, getenv func(string) string, output io.Writer,
) (options, []string, error) {
	c, given, problems, err := readFlags(args, getenv, output)
	if err != nil || c.showVersion {
		return c, nil, err
	}
	problems = append(problems, checkHost(ctx, &c, getenv)...)
	if c.Repo != "" {
		problems = append(problems, resolveProject(ctx, &c, given, getenv)...)
		problems = append(problems, checkCheckout(ctx, &c)...)
	}
	return c, problems, nil
}

// overrides says which of the project's settings a flag or variable overrides where its value
// can't tell: a duration of 0, -resolve-conflicts=true.
type overrides struct {
	ticketLimit, checkTimeout, resolveConflicts bool
}

// readFlags parses the flags in args, each defaulting to its environment variable, and reconciles
// the two: a variable's problem counts only when no flag overrides it. Its error is loadConfig's.
func readFlags(args []string, getenv func(string) string, output io.Writer) (options, overrides, []string, error) {
	var c options
	var problems []string
	fs := flag.NewFlagSet("orchestra", flag.ContinueOnError)
	fs.SetOutput(output)

	fs.StringVar(&c.Workspace, "workspace", "",
		"Herdr workspace for the worker tabs (default: the one orchestra runs in; "+
			"list IDs with: herdr workspace list)")
	limit, limitProblem := envInt(getenv, "LIMIT", 40)
	fs.IntVar(&c.Limit, "limit", limit, "stop after this many tickets in total [LIMIT]")
	doneSoFar, doneSoFarProblem := envInt(getenv, "DONE_SO_FAR", 0)
	fs.IntVar(&c.DoneSoFar, "done-so-far", doneSoFar,
		"tickets dispatched in earlier runs, counted toward -limit [DONE_SO_FAR]")
	fs.StringVar(&c.AgentKind, "agent", envOr(getenv, "AGENT_KIND", "claude"),
		"Herdr agent kind for the workers [AGENT_KIND]")
	fs.StringVar(&c.WorkerPrompt, "prompt", getenv("WORKER_PROMPT"),
		"worker instructions with TICKET_ID as placeholder (default: .orchestra/worker-prompt.md, "+
			"or .claude/worker-prompt.md in a project set up before 'orchestra init') [WORKER_PROMPT]")
	fs.BoolVar(&c.Notify, "notify", getenv("NOTIFY") != "0",
		"macOS notifications for tickets closed, set aside or waiting on you, and the run's end [NOTIFY=0 turns off]")
	fs.StringVar(&c.WTRoot, "worktrees", getenv("WT_ROOT"),
		"folder for the per-ticket worktrees, outside the repository "+
			"(default: <repo>-worktrees next to it) [WT_ROOT]")
	fs.BoolVar(&c.Triage, "triage", getenv("TRIAGE") != "0",
		"have the triage organ note a recommendation on each deferred ticket [TRIAGE=0 turns off]")
	fs.BoolVar(&c.Review, "review", getenv("REVIEW") != "0",
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
	fs.IntVar(&c.Concurrency, "concurrent", concurrent,
		"tickets to work on at the same time (default: .orchestra/settings.json, else 1) [ORCHESTRA_CONCURRENT]")
	fs.IntVar(&c.Concurrency, "c", concurrent, "shorthand for --concurrent")
	ticketLimit, ticketLimitGiven, ticketLimitProblem := envDuration(getenv, "TICKET_LIMIT", false)
	fs.DurationVar(&c.TicketLimit, "ticket-limit", ticketLimit,
		"stop the run when a ticket's worker is still going this long after dispatch, e.g. 2h; 0 for none "+
			"(default: .orchestra/settings.json, else none) [TICKET_LIMIT]")
	checkTimeout, checkTimeoutGiven, checkTimeoutProblem := envDuration(getenv, "ORCHESTRA_CHECK_TIMEOUT", true)
	fs.DurationVar(&c.CheckTimeout, "check-timeout", checkTimeout,
		"stop the check command on a rebased ticket after this long and set the ticket aside, e.g. 5m "+
			"(default: .orchestra/settings.json, else 30m) [ORCHESTRA_CHECK_TIMEOUT]")
	fs.StringVar(&c.Ticket, "ticket", getenv("ORCHESTRA_TICKET"),
		"work on this ticket and its subtickets only, each parent after its children; "+
			"nothing else is started [ORCHESTRA_TICKET]")
	fs.StringVar(&c.Feature, "feature", "",
		"screen and plan this feature request as an epic and its tickets, file them in Beads once you "+
			"confirm, and run the epic as --ticket would")
	fs.BoolVar(&c.Yes, "yes", false, "with --feature, file the plan without asking")
	fs.BoolVar(&c.Tickets, "tickets", getenv("ORCHESTRA_TICKETS") == "1",
		"run the current tickets without first asking, in a terminal, whether to plan a new feature instead "+
			"[ORCHESTRA_TICKETS=1]")
	fs.BoolVar(&c.ResolveConflicts, "resolve-conflicts", true,
		"when a finished ticket's rebase onto work merged while it ran stops on conflicts, "+
			"ask its worker to resolve them before setting it aside; needs a check command "+
			"(default: .orchestra/settings.json, else on)")
	fs.BoolVar(&c.LaunchPrompt, "prompt-at-launch", getenv("PROMPT_AT_LAUNCH") != "0",
		"start Claude workers with their prompt instead of pasting it in [PROMPT_AT_LAUNCH=0 turns off]")
	fs.BoolVar(&c.showVersion, "version", false, "print the version and exit")
	fs.BoolVar(&c.showVersion, "v", false, "shorthand for --version")
	fs.BoolVar(&c.Plain, "plain", false,
		"print plain log lines instead of the interactive view (automatic when not on a terminal)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra [flags]\n"+
			"       orchestra init [--check \"<command>\"] [--check-timeout D] [--concurrent N] [--mcp names] [--force]\n"+
			"       orchestra --feature \"<request>\" [--yes] [flags]\n"+
			"       orchestra plan [--apply]\n\n"+
			"Work through 'bd ready' (or, with -ticket, one ticket and its subtickets), as many tickets at once "+
			"as --concurrent allows, one worker (a coding agent) per Herdr tab and git worktree.\n"+
			"With --feature, plan the request as an epic and its tickets first, and run those.\n"+
			"In a terminal, without --feature, --ticket, --tickets or -plain, it first asks which: "+
			"the current tickets, or a new feature you describe.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 done, 2 setup problem, 3 worker blocked, paused or over its time, "+
			"4 Herdr/Beads/git failure,\n5 main checkout dirty or off its branch, 6 merge failed, "+
			"7 environment failing workers, 130 Ctrl+C.\n")
	}
	if err := fs.Parse(args); err != nil {
		return c, overrides{}, nil, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		switch rest[0] {
		case "init":
			fmt.Fprintln(fs.Output(), "orchestra: init comes before its flags: "+
				"orchestra init [--check \"<command>\"] [--check-timeout D] [--concurrent N] [--force]")
		case "plan":
			fmt.Fprintln(fs.Output(), "orchestra: plan comes before its flags: orchestra plan [--apply]")
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
	// -concurrent and -c, ResolveTicketLimit and ResolveCheckTimeout their flags).
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
		ticketLimit:      set["ticket-limit"] || ticketLimitGiven,
		checkTimeout:     set["check-timeout"] || checkTimeoutGiven,
		resolveConflicts: set["resolve-conflicts"],
	}, problems, nil
}

// checkHost finds what a run needs around the project: its repository (c.Repo), the Herdr pane it
// runs in and the workspace for the workers' tabs (c.Workspace), and the commands it calls.
func checkHost(ctx context.Context, c *options, getenv func(string) string) []string {
	var problems []string
	if repo, err := (git.Git{}).TopLevel(ctx, ""); err == nil {
		c.Repo = repo
	} else {
		problems = append(problems, "Not inside a git repository: cd into the project first.")
	}
	if c.Workspace == "" {
		c.Workspace = herdr.CurrentWorkspace(ctx, getenv)
	}
	if c.Workspace == "" && getenv("HERDR_ENV") == "1" {
		problems = append(problems, "Could not tell which Herdr workspace this pane is in. "+
			"Find the ID with 'herdr workspace list', then run: orchestra --workspace <id>")
	}
	if getenv("HERDR_ENV") != "1" {
		problems = append(problems,
			"Not running inside a Herdr pane (HERDR_ENV is not 1). Start 'herdr' and run this from a pane.")
	}
	for _, cmd := range []string{"bd", "herdr", "git"} {
		if _, err := exec.LookPath(cmd); err != nil {
			problems = append(problems, "Required command not found: "+cmd)
		}
	}
	return problems
}

// resolveProject reads what the project in c.Repo decides, under the flags and variables in c
// that override it: its layout under .orchestra/ and its settings.
func resolveProject(ctx context.Context, c *options, given overrides, getenv func(string) string) []string {
	var problems []string
	lay := project.Locate(c.Repo)
	if c.ownPrompt = c.WorkerPrompt != ""; !c.ownPrompt {
		c.WorkerPrompt = lay.Prompt
	} else if !filepath.IsAbs(c.WorkerPrompt) {
		c.WorkerPrompt = filepath.Join(c.Repo, c.WorkerPrompt)
	}
	c.LogPath, c.ReportsDir = lay.Log, lay.Reports
	settings, _, err := project.LoadSettings(c.Repo)
	if err != nil {
		problems = append(problems, "Unreadable settings: "+err.Error())
	}
	c.Check = settings.Check
	c.NoFootprint = settings.Footprint != nil && !*settings.Footprint
	c.WorkerArgs = mcp.ChromeArgs(settings.MCPServers)
	ok := keep(&problems, &c.WorkerEffort)(project.ResolveEffort(c.WorkerEffort, "worker_effort", settings.WorkerEffort))
	if ok && c.WorkerEffort != "" {
		c.WorkerArgs = append(c.WorkerArgs, "--effort", c.WorkerEffort)
	}
	keep(&problems, &c.OrganEffort)(project.ResolveEffort(c.OrganEffort, "organ_effort", settings.OrganEffort))
	keep(&problems, &c.Concurrency)(project.ResolveConcurrency(c.Concurrency, settings))
	keep(&problems, &c.TicketLimit)(project.ResolveTicketLimit(c.TicketLimit, given.ticketLimit, settings))
	keep(&problems, &c.CheckTimeout)(project.ResolveCheckTimeout(c.CheckTimeout, given.checkTimeout, settings))
	keep2(&problems, &c.ResolveConflicts, &c.ResolveTimeout)(
		project.ResolveConflictResolution(c.ResolveConflicts, given.resolveConflicts, settings))
	keep2(&problems, &c.EnvHoldCount, &c.EnvHoldWindow)(project.ResolveEnvironmentHold(settings))
	keep(&problems, &c.EnvProbe)(project.ResolveEnvironmentProbe(settings))
	keep(&problems, &c.ExcludeTypes)(project.ResolveExcludeTypes(settings))
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
	if linked, err := (git.Git{}).LinkedWorktree(ctx, c.Repo); err != nil {
		problems = append(problems, "Could not tell whether "+c.Repo+" is the main checkout: "+err.Error()+".")
	} else if linked {
		problems = append(problems, c.Repo+" is a linked worktree. Run this from the main checkout.")
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

// exitStatus is the status orchestra ends with when it isn't 0; what went wrong has been
// reported already.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// status turns an exit code into run's result: nil for 0.
func status(code int) error {
	if code == dispatch.ExitOK {
		return nil
	}
	return exitStatus(code)
}

// exitCode is the code orchestra exits with when run returns err: an exitStatus's, 1 for another
// error, 0 for none.
func exitCode(err error) int {
	var s exitStatus
	switch {
	case errors.As(err, &s):
		return int(s)
	case err != nil:
		return 1
	}
	return dispatch.ExitOK
}

// notSetUpMessage is what a run says first in a project never set up, before its other problems.
const notSetUpMessage = "orchestra isn't set up in this repository yet. Run this first:\n" +
	"  orchestra init\n" +
	"It writes " + project.Dir + "/ (the worker prompt and settings) and sets up Beads if needed."

// run is orchestra: 'orchestra init …', 'orchestra plan …' or a run. It returns nil or an exitStatus.
// A run's setup, held until it ends (the lock, the stop signals' watch, the log), the question of
// what it works on, and a feature typed there or given with --feature are here; runPlain or
// runDashboard runs the loop and the organ phase after it.
func run(
	ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer,
) (err error) {
	if len(args) > 1 && args[1] == "init" {
		return status(runInit(ctx, ".", args[2:], getenv, stdin, stdout, stderr))
	}
	if len(args) > 1 && args[1] == "plan" {
		return status(runPlan(ctx, ".", args[2:], stdout, stderr))
	}
	cfg, problems, err := loadConfig(ctx, args[1:], getenv, stderr)
	switch {
	case err == flag.ErrHelp:
		return nil
	case err != nil:
		return exitStatus(dispatch.ExitSetup)
	case cfg.showVersion:
		fmt.Fprintln(stdout, "orchestra", buildVersion())
		return nil
	}
	if cfg.notSetUp || len(problems) > 0 {
		heading := "orchestra cannot start:"
		if cfg.notSetUp {
			fmt.Fprintln(stderr, notSetUpMessage)
			heading = "\nAlso fix before a run:"
		}
		if len(problems) > 0 {
			fmt.Fprintln(stderr, heading)
		}
		for _, p := range problems {
			fmt.Fprintln(stderr, "  - "+p)
		}
		return exitStatus(dispatch.ExitSetup)
	}
	// One run at a time in a repository: a second would race this one for the same tickets, worktrees
	// and branch. Taken before anything changes, a --feature request's screening included, and held
	// until orchestra exits, after the organ phase.
	started := time.Now() // the run's start, as its lock and each record of its event stream give it
	holder := project.Holder{
		PID: os.Getpid(), Started: started, Version: buildVersion(), Branch: cfg.Base,
		Ticket: cfg.Ticket, Feature: cfg.Feature, Pane: getenv("HERDR_PANE_ID"),
	}
	lock, lockErr := project.LockRun(ctx, cfg.Repo, holder)
	var held *project.HeldError
	switch {
	case errors.As(lockErr, &held):
		fmt.Fprintf(stderr, "orchestra cannot start:\n  - %v. One run at a time works on a repository: "+
			"follow that one, or stop it first.\n", held)
		return exitStatus(dispatch.ExitSetup)
	case errors.Is(lockErr, errors.ErrUnsupported): // logged below
	case lockErr != nil:
		fmt.Fprintln(stderr, "orchestra cannot take its run lock:", lockErr)
		return exitStatus(dispatch.ExitSetup)
	}
	defer func() { _ = lock.Close() }() // only releases it; orchestra exiting would too

	prompt, err := os.ReadFile(cfg.WorkerPrompt)
	if err != nil {
		fmt.Fprintln(stderr, "orchestra cannot read the worker prompt:", err)
		return exitStatus(dispatch.ExitSetup)
	}
	for _, dir := range []string{cfg.WTRoot, filepath.Dir(cfg.LogPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(stderr, "orchestra cannot create its folder:", err)
			return exitStatus(dispatch.ExitSetup)
		}
	}
	// From here to the end, closing the log included, a stop signal doesn't end orchestra outright.
	stops := catchStops()
	defer stops.release()
	log, err := dispatch.OpenLog(cfg.LogPath, cfg.Notify, filepath.Base(cfg.Repo))
	if err != nil {
		fmt.Fprintln(stderr, "orchestra cannot open its log:", err)
		return exitStatus(dispatch.ExitSetup)
	}
	defer func() { // records how the run ended, and shows its last notifications before orchestra exits
		log.End(exitCode(err))
		if err := log.Close(); err != nil {
			fmt.Fprintln(stderr, "orchestra cannot close its log:", err)
		}
	}()
	if errors.Is(lockErr, errors.ErrUnsupported) {
		log.Line(time.Now(), "no run lock on "+runtime.GOOS+": nothing stops a second run in this repository")
	}
	if err := project.EnsureRunExcluded(ctx, cfg.Repo); err != nil {
		log.Raw("", fmt.Errorf("cannot keep %s/%s/ out of git: %w", project.Dir, project.RunName, err))
	}
	if err := os.Chdir(cfg.Repo); err != nil {
		fmt.Fprintln(stderr, err)
		return exitStatus(dispatch.ExitSetup)
	}
	typed := false // the feature was typed at the question rather than given with --feature
	if asksWork(cfg, isTerminal(stdin) && isTerminal(stdout)) {
		feature, code := askWork(ctx, stops, cfg, stdin, stdout, stderr)
		if code != dispatch.ExitOK { // before the run's start: nothing to record
			return exitStatus(code)
		}
		cfg.Feature, typed = feature, feature != "" // "" for the current tickets
		// The lock, taken before the question, says from now on what --feature would have.
		if typed {
			holder.Feature = feature
			if err := lock.Rewrite(holder); err != nil {
				log.Raw("", fmt.Errorf("cannot record the feature in the run lock: %w", err))
			}
		}
	}
	featureCode := dispatch.ExitOK
	switch off := interviewOff(cfg); { // the run is scoped to the epic filed
	case cfg.Feature == "":
	case typed && off == "": // talked through with the user, and filed, in a Claude Code session
		cfg.Ticket, featureCode = runInterview(ctx, stops, cfg, holder.Pane, log, stdin, stdout, stderr)
	default: // planned by the organs, and filed by orchestra
		if typed {
			fmt.Fprintf(stdout, "orchestra can't talk the feature through with claude (%s): its organs plan it.\n", off)
		}
		cfg.Ticket, featureCode = runFeature(ctx, stops, cfg, log, stdin, stdout, stderr)
	}
	log.Begin(cfg.Repo, dispatch.RunStart{Started: started, Version: buildVersion(), Repo: cfg.Repo, Branch: cfg.Base,
		Scope: cfg.Ticket, Feature: cfg.Feature, Concurrency: cfg.Concurrency})
	if cfg.Feature != "" && cfg.Ticket == "" { // nothing filed to run
		return status(featureCode)
	}
	out, isFile := stdout.(*os.File)
	plain := cfg.Plain || !isFile || !term.IsTerminal(int(out.Fd()))
	if nothingToRun(ctx, cfg, log, stdout, plain) {
		return nil
	}

	organCtx, cancelOrgans := context.WithCancel(context.Background())
	defer cancelOrgans()
	orch := newLoop(organCtx, &cfg, log, string(prompt))
	if cfg.Triage {
		orch.StartTriage()
	}
	r := loopRun{orch: orch, cfg: cfg, stops: stops, log: log, cancelOrgans: cancelOrgans}
	if plain {
		return status(runPlain(ctx, r, stdout))
	}
	return status(runDashboard(ctx, r, stdin, out, stderr))
}

// nothingToRun ends the run c before its loop when it has nothing to run (see
// dispatch.CheckNothingToRun), and reports whether it did: it says why on stdout, plain or in a box,
// and gives the run's done line to the log and the event stream, as the loop's end would, without
// a notification; run's deferred End records the end, with code 0. Nothing is cleared, opened or
// asked of claude. A run whose limit is reached is left to the loop, which ends it LIMIT_REACHED.
func nothingToRun(ctx context.Context, c options, log *dispatch.Log, stdout io.Writer, plain bool) bool {
	if c.DoneSoFar >= c.Limit {
		return false
	}
	tracker, repo := beads.Tracker{Repo: c.Repo, ExcludeTypes: c.ExcludeTypes}, git.Git{}
	n, err := dispatch.CheckNothingToRun(ctx, c.Config, tracker, repo, repo)
	if err != nil || n == nil { // what the check couldn't read, the loop reads again and reports
		return false
	}
	ev := dispatch.Event{Kind: dispatch.EvDone, N: c.DoneSoFar, Limit: c.Limit, Text: n.Done, Time: time.Now()}
	log.Line(ev.Time, ev.Text)
	log.Record(ev)
	sink := tui.Printer{Out: stdout}
	if out, ok := stdout.(*os.File); ok && !plain {
		sink.Styled, sink.Width = true, termWidth(out)
	}
	sink.Nothing(*n)
	return true
}

// termWidth is the width of the terminal out, or 80 when it can't be read.
func termWidth(out *os.File) int {
	if w, _, err := term.GetSize(int(out.Fd())); err == nil {
		return w
	}
	return 80
}

// newLoop makes the run's loop on Beads, Herdr, git and the workers' reports, its organs advising
// under organCtx. Organs that can't run are turned off in cfg, and the log says so.
func newLoop(organCtx context.Context, cfg *options, log *dispatch.Log, prompt string) *dispatch.Loop {
	cfg.Version = buildVersion()
	organsOff := organ.Unavailable("claude")
	cfg.Predict = organsOff == "" // the predictor goes with footprints, which the loop checks
	tracker, terminal, repo := beads.Tracker{Repo: cfg.Repo, ExcludeTypes: cfg.ExcludeTypes}, herdr.Terminal{}, git.Git{}
	orch := dispatch.New(cfg.Config, log, prompt, dispatch.Deps{
		Tickets:   tracker,
		Notes:     tracker,
		Tabs:      terminal,
		Starter:   terminal,
		Namer:     terminal,
		Agents:    terminal,
		Reporter:  claude.Reporter{},
		Checkout:  repo,
		Worktrees: repo,
		Merger:    repo,
		History:   repo,
		Advisor:   organ.Client{Bin: "claude", Model: cfg.OrganModel, Effort: cfg.OrganEffort},
		AdviceCtx: organCtx,
	})
	if organsOff != "" && (cfg.Triage || cfg.Review || cfg.Concurrency > 1 && !cfg.NoFootprint) {
		log.Line(time.Now(), "organs off: "+organsOff)
		cfg.Triage, cfg.Review = false, false
	}
	return orch
}

// loopRun is what the two ways of running the loop, plain and under the dashboard, share: the loop
// and its configuration, the stop signals' watch, the log, and how to cancel the organs' context.
type loopRun struct {
	orch         *dispatch.Loop
	cfg          options
	stops        *stopWatch
	log          *dispatch.Log
	cancelOrgans func()
}

// organs runs the organ phase after the loop ended with code and its final line.
func (r loopRun) organs(code int, final string, out tui.Printer) {
	organPhase(r.orch, r.cfg, r.stops, r.log, code, final, out, r.cancelOrgans)
}

// runPlain runs the loop printing plain log lines (with --plain, or when stdout isn't a terminal),
// then the organ phase. It returns the loop's exit code.
func runPlain(ctx context.Context, r loopRun, stdout io.Writer) int {
	ctx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	sink := tui.Printer{Out: stdout}
	// The signal that stops the loop is the first; one while it winds down ends orchestra.
	r.stops.quitWith(quitter{loop: r.orch, log: r.log, out: sink, exit: os.Exit})
	r.stops.on(func(s os.Signal) { cancelRun(dispatch.InterruptedError(stoppedHow(s))) })
	stopDrain := watchDrain(func() { r.orch.Drain("by " + signalName(drainSignals[0])) })
	r.orch.SetSink(sink)
	r.orch.ReportInterrupt = true
	code := r.orch.Run(ctx)
	r.stops.on(nil)
	stopDrain()
	r.organs(code, r.orch.Final(), sink)
	return code
}

// runDashboard runs the loop under the interactive dashboard, then, with the terminal restored,
// prints the run's summary and the loop's events from where the dashboard left off, and runs the
// organ phase. It returns the loop's exit code, or ExitInterrupted when the dashboard closed before
// the loop ended.
func runDashboard(ctx context.Context, r loopRun, stdin io.Reader, stdout *os.File, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// orchestra handles the signals itself: Bubble Tea's handler knows nothing of SIGHUP.
	drain := func(on bool) {
		if on {
			r.orch.Drain("from the dashboard")
		} else {
			r.orch.Resume("from the dashboard")
		}
	}
	// The dashboard draws on the alternate screen, which leaves the earlier output as it was and has
	// no scrollback: a terminal that reflows a frame as the pane resizes can't push its rows out of
	// Bubble Tea's reach, as it can on the normal screen.
	p := tea.NewProgram(tui.NewDashboard(r.cfg.Config, cancel, drain, focusTab(ctx, r.log)),
		tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen(), tea.WithoutSignalHandler())
	quitBy := make(chan os.Signal, 1)
	r.stops.on(func(s os.Signal) {
		quitBy <- s // before the quit, so stoppedBy finds it
		go p.Quit() // Quit waits for the dashboard to have started; a stop mustn't wait
	})
	// the dashboard hears it from the loop
	stopDrain := watchDrain(func() { r.orch.Drain("by " + signalName(drainSignals[0])) })
	progSink := tui.NewProgramSink(p)
	r.orch.SetSink(progSink)
	codes := make(chan int, 1)
	ran := make(chan struct{}) // closed once the dashboard has exited and the terminal is restored
	go func() {
		// The loop recovers its workers' panics; one in the loop itself is a bug that ends
		// orchestra, but not with the terminal left in raw mode. Panicking again from here keeps
		// the original stack in the crash.
		defer func() {
			if v := recover(); v != nil {
				r.log.Line(time.Now(), fmt.Sprintf("PANIC: %v\n\n%s", v, debug.Stack()))
				p.Kill()
				<-ran
				panic(v)
			}
		}()
		codes <- r.orch.Run(ctx)
		p.Send(tui.Finished{})
	}()
	final, err := p.Run()
	close(ran)
	// A stop signal from here on goes to the organ phase, which it skips, until quitWith below: one
	// that came while the dashboard closed was a further one, if a signal closed it.
	r.stops.on(nil)
	var sig os.Signal // the one that closed the dashboard, if any
	select {
	case sig = <-quitBy:
	default:
	}
	stopDrain()
	if err != nil {
		fmt.Fprintln(stderr, "orchestra:", err)
	}
	// As wide as the pane is now: it may have narrowed under the dashboard.
	sink := tui.Printer{Out: stdout, Styled: true, Width: termWidth(stdout)}
	m, _ := final.(tui.Dashboard)
	sink.Summary(m) // the dashboard went with the alternate screen
	sink.End(m)
	// From here the loop's events are printed, starting with any the dashboard never received.
	progSink.Handoff(sink, m.Received())
	// With the terminal restored, a stop signal while the stopped loop winds down can end orchestra.
	r.stops.quitWith(quitter{loop: r.orch, log: r.log, out: sink, exit: os.Exit})
	if why := stoppedBy(m, err, len(codes) > 0, sig); why != "" {
		// The loop may be in the middle of a command; log the stop and leave the workers to the user.
		cancel()
		msg := dispatch.InterruptLine(why, r.orch.Running())
		ev := dispatch.Event{Kind: dispatch.EvStop, Detail: dispatch.Interrupted, Text: msg, Time: time.Now()}
		r.log.Line(ev.Time, msg)
		r.log.Notify(dispatch.Notice(ev))
		r.log.Record(ev)
		sink.Event(ev)
		r.stops.windDown()
		// Let the loop and its workers stop before triage closes and the reviewer reads its state.
		// Run waits for its workers, whose commands stop with it or at their time limits, and names
		// any not back within a second below the INTERRUPTED line.
		<-codes
		r.organs(dispatch.ExitInterrupted, msg, sink)
		return dispatch.ExitInterrupted
	}
	code := <-codes
	r.organs(code, r.orch.Final(), sink)
	return code
}

// focusTab is the dashboard's callback for a worker's number: it switches Herdr to the worker's tab
// in the background, as the dashboard mustn't wait on Herdr. A failure goes to the log and changes
// nothing on screen.
func focusTab(ctx context.Context, log *dispatch.Log) func(tab string) {
	return func(tab string) {
		go func() {
			if err := (herdr.Terminal{}).FocusTab(ctx, tab); err != nil {
				log.Raw("", fmt.Errorf("cannot switch to tab %s from the dashboard: %w", tab, err))
			}
		}()
	}
}

// stoppedBy says what stopped the run when the dashboard m has closed before the loop ended by
// itself: Ctrl+C in the dashboard, a signal (sig, nil if none came) that closed it, or the
// dashboard failing (err is what its program returned) while the loop was still running. It is ""
// when the loop ended the run.
func stoppedBy(m tui.Dashboard, err error, loopDone bool, sig os.Signal) string {
	switch {
	case m.Interrupted():
		return "with Ctrl+C"
	case m.Final() != nil || loopDone:
		return ""
	case sig != nil:
		return "by " + signalName(sig)
	case err != nil:
		return "because the dashboard failed"
	default:
		// The dashboard quits by itself only on the loop's last event, so this shouldn't happen.
		return "because the dashboard closed"
	}
}

// organs is what organPhase needs from the loop.
type organs interface {
	FinishTriage(ctx context.Context)
	Review(ctx context.Context, code int, final string) (string, error)
	SaveReport(report string) (string, error)
}

// organPhase runs after the loop stops: it waits for pending triage, then has the reviewer write
// the run report. Ctrl+C, or another of stopSignals, skips whatever is left, and one that came
// while the loop wound down skips it all; so does SIGTERM or SIGHUP at any time in the run. The
// one after the signal that skipped it ends orchestra (see stopWatch.further).
func organPhase(orch organs, c options, stops *stopWatch, log *dispatch.Log, code int, final string, out tui.Printer,
	cancelOrgans func(),
) {
	if !c.Triage && !c.Review || stops.leaving() {
		return
	}
	ctx, stop := stops.context(context.Background())
	defer stop()
	if ctx.Err() != nil {
		return
	}
	go func() {
		<-ctx.Done()
		cancelOrgans()
	}()
	if c.Triage {
		out.Say("finishing triage…", "Finishing triage…")
		orch.FinishTriage(ctx)
	}
	if !c.Review || ctx.Err() != nil {
		return
	}
	out.Say("writing the run report with claude… (ctrl+c skips)", "Writing the run report with Claude… (Ctrl+C skips)")
	report, err := orch.Review(ctx, code, final)
	if err != nil {
		if ctx.Err() == nil {
			msg := "REVIEW_FAILED: " + dispatch.FirstLine(err.Error())
			log.Line(time.Now(), msg)
			out.Warn(msg)
		}
		return
	}
	out.Report(report)
	path, err := orch.SaveReport(report)
	if err != nil {
		why := dispatch.FirstLine(err.Error())
		log.Line(time.Now(), "report not saved: "+why)
		out.Say("report not saved: "+why, "Report not saved: "+why)
		return
	}
	log.Line(time.Now(), "REPORT written to "+path)
	out.Say("report saved to "+tui.Tildify(path), "Report saved to "+tui.Tildify(path))
}
