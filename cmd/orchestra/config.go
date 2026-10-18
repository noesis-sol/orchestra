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
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// readEnv reads the variable called name ("" for none) as parse does: def where it is unset, and
// def and the problem where it holds a value parse doesn't take, which only matters when no flag
// overrides the variable. given says it held one parse takes.
func readEnv[T any](
	getenv func(string) string, name string, def T, parse func(string) (T, error),
) (v T, given bool, problem string) {
	if name == "" || getenv(name) == "" {
		return def, false, ""
	}
	v, err := parse(getenv(name))
	if err != nil {
		return def, false, name + " " + err.Error() + "."
	}
	return v, true, ""
}

// envBool reads an on-or-off variable (see parseOnOff), or def when it is unset. Like readEnv,
// anything else is returned as a problem.
func envBool(getenv func(string) string, name string, def bool) (bool, string) {
	v, _, problem := readEnv(getenv, name, def, parseOnOff)
	return v, problem
}

// parseOnOff reads 1, true, yes or on as on, and 0, false, no or off as off, in any case.
func parseOnOff(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	}
	return false, fmt.Errorf("must be 1 or 0, true or false, yes or no, or on or off (got '%s')", v)
}

// parseWhole reads a whole number: 0 or more.
func parseWhole(v string) (int, error) {
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("must be a whole number (got '%s')", v)
	}
	return n, nil
}

// parseLimit reads a duration such as 2h, or 0 for none.
func parseLimit(v string) (time.Duration, error) { return project.ParseDuration(v, "2h", true) }

// parseText takes any text.
func parseText(v string) (string, error) { return v, nil }

// envOr reads a variable that takes any text, or def when it is unset.
func envOr(getenv func(string) string, name, def string) string {
	v, _, _ := readEnv(getenv, name, def, parseText)
	return v
}

// envFlags are a FlagSet's flags that default to environment variables, bound by bindEnv and
// settled with them once the flags are parsed.
type envFlags struct {
	fs       *flag.FlagSet
	getenv   func(string) string
	bindings []*envBinding
}

// envBinding is a flag bound to the variable that gives its default.
type envBinding struct {
	fs       *flag.FlagSet
	names    []string // the flag's, then its shorthands' and aliases'
	envGiven bool     // the variable gave the flag's default
	problem  string   // the variable's value the flag doesn't take: a problem unless the flag is set
	given    *bool    // set to whether the flag or the variable gave the value; nil where nothing asks
	// the problem with the value the flag is set to, as given under name, the first of names the command
	// line sets; nil for none
	check func(name string) string
}

// bindEnv defines the flag name for dst with define, the FlagSet's method for T such as IntVar. Its
// default is the value of the variable env ("" for none), as parse reads it, else def.
func bindEnv[T any](
	e *envFlags, define func(*T, string, T, string), dst *T, name, env string, def T,
	parse func(string) (T, error), usage string,
) *envBinding {
	v, given, problem := readEnv(e.getenv, env, def, parse)
	define(dst, name, v, usage)
	b := &envBinding{fs: e.fs, names: []string{name}, envGiven: given, problem: problem}
	e.bindings = append(e.bindings, b)
	return b
}

// bindGiven is bindEnv for a setting the project's settings.json also has: the flag gives g.Value,
// and g.Given says whether it or its variable did.
func bindGiven[T any](
	e *envFlags, define func(*T, string, T, string), g *project.Given[T], name, env string, def T,
	parse func(string) (T, error), usage string,
) *envBinding {
	b := bindEnv(e, define, &g.Value, name, env, def, parse, usage)
	b.given = &g.Given
	return b
}

// bindWhole is bindEnv for a whole number, which the flag's value must be as the variable's must.
func bindWhole(e *envFlags, dst *int, name, env string, def int, usage string) *envBinding {
	b := bindEnv(e, e.fs.IntVar, dst, name, env, def, parseWhole, usage)
	b.check = func(string) string {
		if *dst < 0 {
			return fmt.Sprintf("--%s must be a whole number (got %d).", name, *dst)
		}
		return ""
	}
	return b
}

// short defines short as the flag's shorthand.
func (b *envBinding) short(short string) {
	b.fs.Var(b.fs.Lookup(b.names[0]).Value, short, "shorthand for --"+b.names[0])
	b.names = append(b.names, short)
}

// alias defines alias as another name for the flag, and returns b.
func (b *envBinding) alias(alias string) *envBinding {
	b.fs.Var(b.fs.Lookup(b.names[0]).Value, alias, "alias of --"+b.names[0])
	b.names = append(b.names, alias)
	return b
}

// settle reconciles each flag with its variable once the flags are parsed: a variable's problem
// counts only when no flag overrides it, and a flag set is held to its check. set is the flags the
// command line sets.
func (e *envFlags) settle() (set map[string]bool, problems []string) {
	set = map[string]bool{}
	e.fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	for _, b := range e.bindings {
		i := slices.IndexFunc(b.names, func(n string) bool { return set[n] })
		flagSet := i >= 0
		switch {
		case !flagSet && b.problem != "":
			problems = append(problems, b.problem)
		case flagSet && b.check != nil:
			if p := b.check(b.names[i]); p != "" {
				problems = append(problems, p)
			}
		}
		if b.given != nil {
			*b.given = flagSet || b.envGiven
		}
	}
	return set, problems
}

// options is a run's configuration: the loop's, and what only the command uses.
type options struct {
	dispatch.Config
	WorkerPrompt string
	ownPrompt    bool // WorkerPrompt came from --prompt or WORKER_PROMPT, not the project
	notSetUp     bool // the project was never set up (see project.IsSetUp): run says to run orchestra init first
	Notify       bool
	Plain        bool
	Triage       bool   // triage organ on each deferred ticket
	Review       bool   // reviewer organ when the loop stops
	OrganModel   string // model for the organs; "" uses the claude CLI's default
	OrganEffort  string // effort for every organ; "" gives each its own
	WorkerEffort string // effort for Claude workers and the refine session, resolved as project.ResolveWorkerEffort
	yes          bool   // --yes, which is gone: a run given it ends saying so
	specFile     string // the spec to run (--spec, ORCHESTRA_SPEC), made absolute; Config.Spec is where it is kept
	Refine       bool   // talk the spec through with the user before it is broken down (--refine)
	limitGiven   bool   // --limit or LIMIT gave Config.Limit, which a spec or feature run otherwise hasn't
	Tickets      bool   // run the current tickets without asking what to work on (see asksWork)
	showVersion  bool
	// what the spec run sets up its repository with, which orchestra init hasn't (spec_setup.go); nil elsewhere
	specSetup *project.Choice
	// how long the spec's breakdown may take, and the effort it runs at (--spec-timeout, --spec-effort)
	SpecTimeout time.Duration
	SpecEffort  string
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
// Its phases, in the order their problems are reported: the flags, and the spec file --spec gives;
// the repository; the terminal, under the project's settings, read once here for it and the project
// (for a spec run that sets the repository up, those its setup writes: see planSpecSetup);
// the commands a run calls; then, in a repository, the project's settings and layout, its checkout,
// and where the spec is kept there.
func loadConfig(
	ctx context.Context, args []string, getenv func(string) string, output io.Writer,
) (options, []string, error) {
	f, problems, err := readFlags(args, getenv, output)
	c := f.options
	if err != nil || c.showVersion {
		return c, nil, err
	}
	specProblems := checkSpecFile(&c, f.specFrom())
	problems = append(problems, specProblems...)
	problems = append(problems, findRepo(ctx, &c)...)
	settings, unreadable := loadSettings(c.Repo)
	settings = planSpecSetup(&c, f.concurrency, settings)
	problems = append(problems, checkTerminal(ctx, &c, settings, getenv)...)
	problems = append(problems, checkTools()...)
	if c.Repo != "" {
		problems = append(problems, unreadable...)
		problems = append(problems, resolveProject(ctx, &c, f, settings, getenv)...)
		problems = append(problems, checkCheckout(ctx, &c)...)
		if len(specProblems) == 0 {
			problems = append(problems, checkSpecPath(ctx, &c, f.specFrom())...)
		}
	}
	return c, problems, nil
}

// runFlags are a run's flags, each defaulting to its environment variable: the options they give,
// and the settings the project's settings.json also has, which resolveSettings resolves into them.
type runFlags struct {
	options
	concurrency                                 project.Given[int]
	ticketLimit, checkTimeout, checkFullTimeout project.Given[time.Duration]
	specTimeout                                 project.Given[time.Duration]
	resolveConflicts                            project.Given[bool]
	set                                         map[string]bool // the flags the command line sets
	env                                         envFlags
}

// readFlags parses the flags in args, each defaulting to its environment variable, and reconciles
// the two: a variable's problem counts only when no flag overrides it. Its error is loadConfig's.
func readFlags(args []string, getenv func(string) string, output io.Writer) (*runFlags, []string, error) {
	fs := flag.NewFlagSet("orchestra", flag.ContinueOnError)
	fs.SetOutput(output)
	f := defineRunFlags(fs, getenv)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), "Usage: orchestra [flags]\n"+
			"       orchestra --feature \"<request>\" [flags]\n"+
			"       orchestra --spec <file> [--refine] [flags]\n")
		for _, c := range subcommands {
			fmt.Fprint(fs.Output(), usageLines("       ", c.name, c.synopsis))
		}
		fmt.Fprint(fs.Output(), "\n"+
			"Work through 'bd ready' (or, with --ticket, one ticket and its subtickets), as many tickets at once "+
			"as --concurrent allows, one worker (a coding agent) per tab and git worktree.\n"+
			"With --feature, talk the request through with you in a Claude Code session first, which files it as an "+
			"epic and its tickets, and run those.\n"+
			"With --spec, commit the spec and break it down into an epic and its tickets first, and run those.\n"+
			"In a terminal, without --feature, --spec, --ticket, --tickets or --plain, it first asks which: "+
			"the current tickets, a spec you pick (as --spec runs it), or a new feature you describe.\n\n")
		printFlags(fs, "yes")
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 done, 2 setup problem, 3 worker blocked, paused or over its time, "+
			"4 terminal (Herdr, tmux or cmux), Beads or git failure,\n5 main checkout dirty or off its branch, 6 merge failed, "+
			"7 environment failing workers, 8 usage limit held too long, 130 Ctrl+C.\n")
	}
	if err := fs.Parse(args); err != nil {
		return f, nil, err
	}
	if rest := fs.Args(); len(rest) > 0 {
		if c, ok := findSubcommand(rest[0]); ok {
			fmt.Fprintf(fs.Output(), "orchestra: %s comes before its flags:\n%s", c.name, usageLines("  ", c.name, c.synopsis))
		} else {
			fmt.Fprintf(fs.Output(), "orchestra: unexpected argument %q (see orchestra -h)\n", rest[0])
		}
		return f, nil, errUnexpectedArgs
	}
	if f.showVersion {
		return f, nil, nil
	}
	var problems []string
	f.set, problems = f.env.settle()
	if _, problem := envBool(getenv, noUpdateCheck, false); problem != "" {
		problems = append(problems, problem) // no flag: the variable alone turns the check off
	}
	f.Feature = strings.TrimSpace(f.Feature)
	if (f.specFile != "" || f.Feature != "") && !f.limitGiven {
		f.Limit = dispatch.NoLimit // a whole app, or a feature, can take far more tickets than a run's default
	}
	return f, append(problems, checkFlagCombos(f)...), nil
}

// printFlags lists the flags of fs as fs.PrintDefaults does, but with two dashes before a long flag's name
// (--limit) and one before a shorthand's (-c), and without those named in hidden: flags that are gone, kept
// only to say so. A flag whose usage gives its default, "(default: …)", isn't followed by the flag package's.
func printFlags(fs *flag.FlagSet, hidden ...string) {
	fs.VisitAll(func(f *flag.Flag) {
		if slices.Contains(hidden, f.Name) {
			return
		}
		line := "  " + flagName(f.Name)
		kind, usage := flag.UnquoteUsage(f)
		if kind != "" {
			line += " " + kind
		}
		if len(line) <= len("  -v") { // a one-letter bool: its usage on the same line, as PrintDefaults has it
			line += "\t"
		} else {
			line += "\n    \t"
		}
		line += strings.ReplaceAll(usage, "\n", "\n    \t")
		if def := listedDefault(f); def != "" && !strings.Contains(usage, "(default:") {
			line += " (default " + def + ")"
		}
		fmt.Fprintln(fs.Output(), line)
	})
}

// flagName is the flag called name as orchestra writes it: --name, or -c for a one-letter shorthand.
func flagName(name string) string {
	if len(name) == 1 {
		return "-" + name
	}
	return "--" + name
}

// listedDefault is f's default as PrintDefaults gives it, quoted for a string: "" where it is the zero
// value of f's type.
func listedDefault(f *flag.Flag) string {
	t := reflect.TypeOf(f.Value)
	zero := reflect.Zero(t)
	if t.Kind() == reflect.Pointer {
		zero = reflect.New(t.Elem())
	}
	if f.DefValue == zero.Interface().(flag.Value).String() {
		return ""
	}
	if g, ok := f.Value.(flag.Getter); ok {
		if _, isText := g.Get().(string); isText {
			return strconv.Quote(f.DefValue)
		}
	}
	return f.DefValue
}

// defineRunFlags defines a run's flags in fs, each bound to the environment variable it defaults
// to, if any. A variable's problems are reported in the order they are bound.
func defineRunFlags(fs *flag.FlagSet, getenv func(string) string) *runFlags {
	f := &runFlags{env: envFlags{fs: fs, getenv: getenv}}
	e, c := &f.env, &f.options
	fs.StringVar(&c.Workspace, "workspace", "",
		"Herdr workspace or tmux session for the worker tabs (default: the one orchestra runs in, "+
			"or under tmux outside it orchestra-<repository folder>; list Herdr's with: herdr workspace list); "+
			"none under cmux, where each worker has a workspace of its own")
	bindEnv(e, fs.StringVar, &c.terminalAsked, "terminal", "ORCHESTRA_TERMINAL", "", parseText,
		"what workers run in: herdr, tmux, cmux, or auto for the one orchestra runs in, else tmux "+
			"(default: .orchestra/settings.json, else auto) [ORCHESTRA_TERMINAL]")
	bindWhole(e, &c.Limit, "limit", "LIMIT", 40,
		"stop after this many tickets in total; none for a --spec or --feature run unless given [LIMIT]",
	).given = &f.limitGiven
	bindWhole(e, &c.DoneSoFar, "done-so-far", "DONE_SO_FAR", 0,
		"tickets dispatched in earlier runs, counted toward --limit [DONE_SO_FAR]")
	bindEnv(e, fs.StringVar, &c.AgentKind, "agent", "AGENT_KIND", "claude", parseText,
		"agent kind for the workers: claude, or another Herdr agent kind (Herdr only) [AGENT_KIND]")
	bindEnv(e, fs.StringVar, &c.WorkerPrompt, "prompt", "WORKER_PROMPT", "", parseText,
		"worker instructions with TICKET_ID as placeholder (default: .orchestra/worker-prompt.md, "+
			"or .claude/worker-prompt.md in a project set up before 'orchestra init') [WORKER_PROMPT]")
	bindEnv(e, fs.StringVar, &c.WTRoot, "worktrees", "WT_ROOT", "", parseText,
		"folder for the per-ticket worktrees, outside the repository "+
			"(default: <repo>-worktrees next to it) [WT_ROOT]")
	bindEnv(e, fs.StringVar, &c.OrganModel, "organ-model", "ORGAN_MODEL", "", parseText,
		"model for the organs: triage, the predictor and the run report (default: the claude CLI's default) [ORGAN_MODEL]")
	bindEnv(e, fs.StringVar, &c.OrganEffort, "organ-effort", "ORGAN_EFFORT", "", parseText,
		"effort for every organ: low, medium, high, xhigh or max (default: .orchestra/settings.json, "+
			"else low for triage and the predictor, medium for the reviewer and init's scout) [ORGAN_EFFORT]")
	bindEnv(e, fs.StringVar, &c.WorkerEffort, "worker-effort", "WORKER_EFFORT", "", parseText,
		"effort Claude workers, and a spec's or feature's talk-through, start at: low, medium, high, xhigh or max "+
			"(default: .orchestra/settings.json, else high) [WORKER_EFFORT]")
	bindEnv(e, fs.StringVar, &c.WorkerPermissionMode, "worker-permission-mode", "WORKER_PERMISSION_MODE", "",
		parseText, "permission mode Claude workers start in: default (or manual, its name in newer Claude Code), "+
			"acceptEdits or auto, where Claude Code's classifier decides and asks only when it can't (default: "+
			".orchestra/settings.json, else auto for a --spec or --feature run and Claude Code's default "+
			"otherwise) [WORKER_PERMISSION_MODE]")
	bindEnv(e, fs.StringVar, &c.Ticket, "ticket", "ORCHESTRA_TICKET", "", parseText,
		"work on this ticket and its subtickets only, each parent after its children; "+
			"nothing else is started [ORCHESTRA_TICKET]")
	bindGiven(e, fs.IntVar, &f.concurrency, "concurrent", "ORCHESTRA_CONCURRENT", 0, parseWhole,
		"tickets to work on at the same time (default: .orchestra/settings.json, else 1) [ORCHESTRA_CONCURRENT]").
		short("c")
	bindGiven(e, fs.DurationVar, &f.ticketLimit, "ticket-limit", "TICKET_LIMIT", 0, parseLimit,
		"stop the run when a ticket's worker is still going this long after dispatch, e.g. 2h; 0 for none "+
			"(default: .orchestra/settings.json, else none) [TICKET_LIMIT]")
	bindGiven(e, fs.DurationVar, &f.checkTimeout, "check-timeout", "ORCHESTRA_CHECK_TIMEOUT", 0,
		project.ParseCheckTimeout,
		"stop the merge check (check-fast) on a rebased ticket after this long and set the ticket aside, e.g. 5m "+
			"(default: .orchestra/settings.json, else 30m) [ORCHESTRA_CHECK_TIMEOUT]")
	bindGiven(e, fs.DurationVar, &f.checkFullTimeout, "check-full-timeout", "ORCHESTRA_CHECK_FULL_TIMEOUT", 0,
		project.ParseCheckTimeout,
		"stop the full check (check-full), run once at the end of a run, after this long, e.g. 90m "+
			"(default: .orchestra/settings.json, else 60m) [ORCHESTRA_CHECK_FULL_TIMEOUT]")
	bindGiven(e, fs.BoolVar, &f.resolveConflicts, "resolve-conflicts", "", true, parseOnOff,
		"when a finished ticket's rebase onto work merged while it ran stops on conflicts, "+
			"ask its worker to resolve them before setting it aside; needs a check command "+
			"(default: .orchestra/settings.json, else on)")
	bindEnv(e, fs.BoolVar, &c.Notify, "notify", "NOTIFY", true, parseOnOff,
		"macOS notifications for tickets closed, set aside or waiting on you, and the run's end [NOTIFY=0 turns off]")
	bindEnv(e, fs.BoolVar, &c.Triage, "triage", "TRIAGE", true, parseOnOff,
		"have the triage organ note a recommendation on each deferred ticket [TRIAGE=0 turns off]")
	bindEnv(e, fs.BoolVar, &c.Review, "review", "REVIEW", true, parseOnOff,
		"have the reviewer organ write a run report when the loop stops [REVIEW=0 turns off]")
	bindEnv(e, fs.BoolVar, &c.Tickets, "tickets", "ORCHESTRA_TICKETS", false, parseOnOff,
		"run the current tickets without first asking, in a terminal, whether to run a spec or talk a new feature "+
			"through instead [ORCHESTRA_TICKETS=1]")
	bindEnv(e, fs.BoolVar, &c.LaunchPrompt, "prompt-at-launch", "PROMPT_AT_LAUNCH", true, parseOnOff,
		"start Claude workers with their prompt instead of pasting it in [PROMPT_AT_LAUNCH=0 turns off]")
	fs.StringVar(&c.Feature, "feature", "",
		"talk this feature request through with you in a Claude Code session, which files it as an epic and its "+
			"tickets in Beads, and run the epic as --ticket would")
	fs.BoolVar(&c.yes, "yes", false, "gone; left out of the help, it ends the run saying what to use instead")
	bindEnv(e, fs.StringVar, &c.specFile, "spec", "ORCHESTRA_SPEC", "", parseText,
		"commit this spec, a Markdown or plain-text file (one outside the repository as a copy in "+
			project.Dir+"/"+project.SpecsDir+"/), break it down into an epic and its tickets, "+
			"and run the epic as --ticket would; a repository orchestra init hasn't set up, even one with no "+
			"commit, is set up first, with init's defaults [ORCHESTRA_SPEC]")
	bindEnv(e, fs.BoolVar, &c.Refine, "refine", "ORCHESTRA_REFINE", false, parseOnOff,
		"with --spec, talk the spec through with you in a Claude Code session before it is broken down; "+
			"--feature always talks the feature through, with or without it [ORCHESTRA_REFINE=1]")
	bindGiven(e, fs.DurationVar, &f.specTimeout, "spec-timeout", "ORCHESTRA_SPEC_TIMEOUT", 0,
		project.ParseCheckTimeout,
		"with --spec, stop the spec's breakdown after this long, e.g. 90m "+
			"(default: .orchestra/settings.json, else 60m) [ORCHESTRA_SPEC_TIMEOUT]")
	bindEnv(e, fs.StringVar, &c.SpecEffort, "spec-effort", "ORCHESTRA_SPEC_EFFORT", "", parseText,
		"with --spec, the effort the spec's breakdown runs at: low, medium, high, xhigh or max "+
			"(default: .orchestra/settings.json, else high) [ORCHESTRA_SPEC_EFFORT]")
	fs.BoolVar(&c.showVersion, "version", false, "print the version and exit")
	fs.BoolVar(&c.showVersion, "v", false, "shorthand for --version")
	fs.BoolVar(&c.Plain, "plain", false,
		"print plain log lines instead of the interactive view (automatic when not on a terminal)")
	return f
}

// checkFlagCombos checks how a run's flags go together: --feature and --spec with what they need
// and against what they can't be combined with.
func checkFlagCombos(f *runFlags) []string {
	if f.set["yes"] {
		return []string{"--yes is gone: --feature always talks the feature through with you; " +
			"orchestra --spec <file> runs a request written to a file unattended."}
	}
	if p := checkSpecCombos(f); p != "" {
		return []string{p}
	}
	feature := f.set["feature"]
	switch {
	case feature && f.Feature == "":
		return []string{"--feature needs the request: orchestra --feature \"<what to build>\""}
	case feature && f.Ticket != "":
		return []string{"--feature can't be combined with --ticket (or ORCHESTRA_TICKET): " +
			"a feature run is scoped to the epic it files."}
	case feature && f.set["tickets"] && f.Tickets:
		return []string{"--feature can't be combined with --tickets: " +
			"one runs a new feature, the other the current tickets."}
	case feature && f.Limit >= 0 && f.DoneSoFar >= f.Limit:
		// Only with --limit or LIMIT, as for a spec: the loop would end at once, the plan filed and none
		// of it started.
		return []string{fmt.Sprintf("--feature would file a plan and run none of it: "+
			"--done-so-far (%d) has reached --limit (%d). Raise --limit [LIMIT], lower --done-so-far [DONE_SO_FAR], "+
			"or leave both out.",
			f.DoneSoFar, f.Limit)}
	}
	return nil
}

// checkSpecCombos says how --spec (or ORCHESTRA_SPEC) and --refine don't go with the run's other
// flags, or returns "". ORCHESTRA_REFINE alone, like ORCHESTRA_TICKETS, is a standing choice that
// a run without a spec leaves unused; --refine with --feature changes nothing, as a feature is always
// talked through.
func checkSpecCombos(f *runFlags) string {
	spec, from := f.specFile != "", f.specFrom()
	switch {
	case f.set["spec"] && !spec:
		return "--spec needs the spec's file: orchestra --spec <file>"
	case spec && f.set["feature"]:
		return from + " can't be combined with --feature: one runs a spec, the other a feature request."
	case spec && f.Ticket != "":
		return from + " can't be combined with --ticket (or ORCHESTRA_TICKET): " +
			"a spec run is scoped to the epic its breakdown files."
	case spec && f.set["tickets"] && f.Tickets:
		return from + " can't be combined with --tickets: one runs a spec, the other the current tickets."
	case !spec && !f.set["feature"] && f.set["refine"] && f.Refine:
		return "--refine only applies with --spec or --feature."
	case spec && f.DoneSoFar >= f.Limit:
		// Only with --limit or LIMIT: a spec run has none otherwise.
		return fmt.Sprintf("%s would break the spec down and run none of it: "+
			"--done-so-far (%d) has reached --limit (%d). Raise --limit [LIMIT], lower --done-so-far [DONE_SO_FAR], "+
			"or leave both out.", from, f.DoneSoFar, f.Limit)
	}
	return ""
}

// specFrom is what gave the run its spec, for its problems: --spec, or ORCHESTRA_SPEC.
func (f *runFlags) specFrom() string {
	if f.set["spec"] {
		return "--spec"
	}
	return "ORCHESTRA_SPEC"
}

// findRepo finds the repository orchestra runs in (c.Repo), the one around the working directory.
func findRepo(ctx context.Context, c *options) []string {
	repo, err := (git.Git{}).TopLevel(ctx, "")
	switch {
	case err != nil && c.specFile != "":
		return []string{noRepoForSpec}
	case err != nil:
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

// resolveProject reads what the project in c.Repo decides, under the flags and variables in f
// that override it: its layout under .orchestra/ and its settings.
func resolveProject(
	ctx context.Context, c *options, f *runFlags, settings project.Settings, getenv func(string) string,
) []string {
	lay := project.Locate(c.Repo)
	if c.ownPrompt = c.WorkerPrompt != ""; !c.ownPrompt {
		c.WorkerPrompt = lay.Prompt
	} else if !filepath.IsAbs(c.WorkerPrompt) {
		c.WorkerPrompt = filepath.Join(c.Repo, c.WorkerPrompt)
	}
	c.LogPath, c.ReportsDir = lay.Log, lay.Reports
	problems := resolveSettings(c, f, settings)
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

// resolveSettings resolves the project's settings into c, under the flags and variables in f that
// override them; it reads nothing else, so the precedence of each can be tested on its own.
func resolveSettings(c *options, f *runFlags, settings project.Settings) []string {
	var problems []string
	c.Check, c.CheckFull = settings.CheckFast, settings.CheckFull
	c.Setup = settings.Setup
	c.NoFootprint = settings.Footprint != nil && !*settings.Footprint
	c.WorkerArgs = mcp.ChromeArgs(settings.MCPServers)
	if keep(&problems, &c.WorkerEffort)(project.ResolveWorkerEffort(c.WorkerEffort, settings)) {
		c.WorkerArgs = append(c.WorkerArgs, "--effort", c.WorkerEffort)
	}
	keep(&problems, &c.WorkerPermissionMode)(
		project.ResolvePermissionMode(c.WorkerPermissionMode, settings, c.specFile != "" || c.Feature != ""))
	keep(&problems, &c.OrganEffort)(project.ResolveEffort(c.OrganEffort, "organ_effort", settings.OrganEffort))
	keep(&problems, &c.SpecEffort)(project.ResolveSpecEffort(c.SpecEffort, settings))
	keep(&problems, &c.SpecTimeout)(project.ResolveSpecTimeout(f.specTimeout, settings))
	keep(&problems, &c.Concurrency)(project.ResolveConcurrency(f.concurrency, settings))
	keep(&problems, &c.TicketLimit)(project.ResolveTicketLimit(f.ticketLimit, settings))
	keep(&problems, &c.CheckTimeout)(project.ResolveCheckTimeout(f.checkTimeout, settings))
	keep(&problems, &c.CheckFullTimeout)(project.ResolveCheckFullTimeout(f.checkFullTimeout, settings))
	keep2(&problems, &c.ResolveConflicts, &c.ResolveTimeout)(
		project.ResolveConflictResolution(f.resolveConflicts, settings))
	keep(&problems, &c.CheckHandBacks)(project.ResolveCheckHandBacks(settings))
	keep2(&problems, &c.EnvHoldCount, &c.EnvHoldWindow)(project.ResolveEnvironmentHold(settings))
	keep(&problems, &c.EnvProbe)(project.ResolveEnvironmentProbe(settings))
	keep(&problems, &c.UsageLimitWait)(project.ResolveUsageLimitWait(settings))
	keep(&problems, &c.ExcludeTypes)(project.ResolveExcludeTypes(settings))
	keep(&problems, &c.SetupFiles)(project.ResolveSetupFiles(settings))
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

// checkCheckout checks that the project in c.Repo can be run: that it was set up (c.notSetUp), unless
// the spec run sets it up (c.specSetup), its worker prompt, its Beads database and the ticket --ticket
// scopes the run to, a main checkout on a branch (c.Base), and a folder for the worktrees outside it
// (c.WTRoot).
func checkCheckout(ctx context.Context, c *options) []string {
	var problems []string
	// A project never set up is missing what orchestra init writes or sets up, the worker prompt and
	// Beads: run says to run it, first, in place of those two problems. A spec run sets it up itself.
	c.notSetUp = c.specSetup == nil && !c.ownPrompt && !project.IsSetUp(c.Repo)
	prompt := c.WorkerPrompt
	if rel, err := filepath.Rel(c.Repo, prompt); err == nil && filepath.IsLocal(rel) {
		prompt = rel
	}
	b, err := os.ReadFile(c.WorkerPrompt)
	switch {
	case c.notSetUp, c.specSetup != nil && !c.ownPrompt: // to be written
	case err != nil && c.ownPrompt:
		problems = append(problems, "Worker prompt not found: "+prompt+" (from --prompt or WORKER_PROMPT).")
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
		if !c.notSetUp && c.specSetup == nil {
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
