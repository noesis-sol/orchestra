package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/herdr"
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
	Notify       bool
	Plain        bool
	Triage       bool   // triage organ on each deferred ticket
	Review       bool   // reviewer organ when the loop stops
	OrganModel   string // model for the organs; "" uses the claude CLI's default
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
		"macOS notifications for finished tickets and stops [NOTIFY=0 turns off]")
	fs.StringVar(&c.WTRoot, "worktrees", getenv("WT_ROOT"),
		"folder for the per-ticket worktrees, outside the repository "+
			"(default: <repo>-worktrees next to it) [WT_ROOT]")
	fs.BoolVar(&c.Triage, "triage", getenv("TRIAGE") != "0",
		"triage each deferred ticket with claude and note a recommendation on it [TRIAGE=0 turns off]")
	fs.BoolVar(&c.Review, "review", getenv("REVIEW") != "0",
		"write a run report with claude when the loop stops [REVIEW=0 turns off]")
	fs.StringVar(&c.OrganModel, "organ-model", getenv("ORGAN_MODEL"),
		"model for triage and the report (default: the claude CLI's default) [ORGAN_MODEL]")
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
	fs.BoolVar(&c.ResolveConflicts, "resolve-conflicts", true,
		"when a finished ticket's rebase onto work merged while it ran stops on conflicts, "+
			"ask its worker to resolve them before setting it aside; needs a check command "+
			"(default: .orchestra/settings.json, else on)")
	fs.BoolVar(&c.LaunchPrompt, "prompt-at-launch", getenv("PROMPT_AT_LAUNCH") != "0",
		"start Claude workers with their prompt instead of pasting it in [PROMPT_AT_LAUNCH=0 turns off]")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.BoolVar(&c.Plain, "plain", false,
		"print plain log lines instead of the interactive view (automatic when not on a terminal)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra [flags]\n"+
			"       orchestra init [--check \"<command>\"] [--check-timeout D] [--concurrent N] [--force]\n"+
			"       orchestra plan [--apply]\n\n"+
			"Work through 'bd ready' (or, with -ticket, one ticket and its subtickets) one ticket at a time, "+
			"one agent per Herdr tab and git worktree.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 done, 2 setup problem, 3 worker blocked, paused or over its time, "+
			"4 Herdr/Beads/git failure,\n5 main checkout dirty or off its branch, 6 merge failed, 130 Ctrl+C.\n")
	}
	if err := fs.Parse(args); err != nil {
		return c, nil, err
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
		return c, nil, errUnexpectedArgs
	}
	c.showVersion = *showVersion
	if c.showVersion {
		return c, nil, nil
	}

	// A variable's problem counts only when no flag overrides it; a flag is held to the same rule.
	// ResolveConcurrency checks the range of -concurrent (and -c) below.
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
	} {
		if !v.isSet && v.problem != "" {
			problems = append(problems, v.problem)
		} else if v.isSet && v.value < 0 {
			problems = append(problems, fmt.Sprintf("-%s must be a whole number (got %d).", v.flag, v.value))
		}
	}
	if !set["ticket-limit"] && ticketLimitProblem != "" {
		problems = append(problems, ticketLimitProblem)
	}
	if !set["check-timeout"] && checkTimeoutProblem != "" {
		problems = append(problems, checkTimeoutProblem)
	}

	if out, err := command.Output(ctx, command.ReadLimit, "", "git", "rev-parse", "--show-toplevel"); err == nil {
		c.Repo = strings.TrimSpace(out)
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

	if c.Repo != "" {
		lay := project.Locate(c.Repo)
		if c.WorkerPrompt == "" {
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
		if n, err := project.ResolveConcurrency(c.Concurrency, settings); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.Concurrency = n
		}
		ticketLimitSet := set["ticket-limit"] || ticketLimitGiven
		if d, err := project.ResolveTicketLimit(c.TicketLimit, ticketLimitSet, settings); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.TicketLimit = d
		}
		checkTimeoutSet := set["check-timeout"] || checkTimeoutGiven
		if d, err := project.ResolveCheckTimeout(c.CheckTimeout, checkTimeoutSet, settings); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.CheckTimeout = d
		}
		on, d, err := project.ResolveConflictResolution(c.ResolveConflicts, set["resolve-conflicts"], settings)
		if err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.ResolveConflicts, c.ResolveTimeout = on, d
		}
		if n, d, err := project.ResolveEnvironmentHold(settings); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.EnvHoldCount, c.EnvHoldWindow = n, d
		}
		if d, err := project.ResolveEnvironmentProbe(settings); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.EnvProbe = d
		}
		if types, err := project.ResolveExcludeTypes(settings); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.ExcludeTypes = types
		}
		if b, err := os.ReadFile(c.WorkerPrompt); err != nil {
			problems = append(problems, "Worker prompt not found: "+c.WorkerPrompt+". Set the project up with: orchestra init")
		} else if !strings.Contains(string(b), "TICKET_ID") {
			problems = append(problems, "Worker prompt has no TICKET_ID placeholder: "+c.WorkerPrompt)
		}
		if st, err := os.Stat(filepath.Join(c.Repo, ".beads")); err != nil || !st.IsDir() {
			problems = append(problems, "No Beads database in "+c.Repo+". Run: bd init")
		} else if _, err := exec.LookPath("bd"); err == nil && c.Ticket != "" {
			if p := scopeProblem(ctx, beads.Tracker{Repo: c.Repo}, c.Ticket); p != "" {
				problems = append(problems, p)
			}
		}

		// Finished tickets are merged into the main checkout's branch, so run from there, on a branch.
		if linked, err := linkedWorktree(ctx, c.Repo); err != nil {
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
	}
	return c, problems, nil
}

// linkedWorktree reports whether repo is a linked worktree rather than the main checkout: its git
// directory is not the common one.
func linkedWorktree(ctx context.Context, repo string) (bool, error) {
	gitDir, err := command.Output(ctx, command.ReadLimit, repo, "git", "rev-parse", "--absolute-git-dir")
	if err != nil {
		return false, err
	}
	commonDir, err := command.Output(ctx, command.ReadLimit, repo,
		"git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(gitDir) != strings.TrimSpace(commonDir), nil
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
	case t.Status == "closed":
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

// run is orchestra: 'orchestra init …', 'orchestra plan …' or a run. It returns nil or an exitStatus.
func run(
	ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer,
) error {
	if len(args) > 1 && args[1] == "init" {
		return status(runInit(ctx, ".", args[2:], stdin, stdout, stderr))
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
	if len(problems) > 0 {
		fmt.Fprintln(stderr, "orchestra cannot start:")
		for _, p := range problems {
			fmt.Fprintln(stderr, "  - "+p)
		}
		return exitStatus(dispatch.ExitSetup)
	}

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
	log, err := dispatch.OpenLog(cfg.LogPath, cfg.Notify, filepath.Base(cfg.Repo))
	if err != nil {
		fmt.Fprintln(stderr, "orchestra cannot open its log:", err)
		return exitStatus(dispatch.ExitSetup)
	}
	defer func() { // shows the run's last notifications before orchestra exits
		if err := log.Close(); err != nil {
			fmt.Fprintln(stderr, "orchestra cannot close its log:", err)
		}
	}()
	if err := project.EnsureRunExcluded(ctx, cfg.Repo); err != nil {
		log.Raw("", fmt.Errorf("cannot keep %s/%s/ out of git: %w", project.Dir, project.RunName, err))
	}
	if err := os.Chdir(cfg.Repo); err != nil {
		fmt.Fprintln(stderr, err)
		return exitStatus(dispatch.ExitSetup)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	organCtx, cancelOrgans := context.WithCancel(context.Background())
	defer cancelOrgans()
	cfg.Version = buildVersion()
	organsOff := organ.Unavailable("claude")
	cfg.Predict = organsOff == "" // the predictor goes with footprints, which the loop checks
	tracker, terminal, repo := beads.Tracker{Repo: cfg.Repo, ExcludeTypes: cfg.ExcludeTypes}, herdr.Terminal{}, git.Git{}
	orch := dispatch.New(cfg.Config, log, string(prompt), dispatch.Deps{
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
		Advisor:   organ.Client{Bin: "claude", Model: cfg.OrganModel},
		AdviceCtx: organCtx,
	})
	if organsOff != "" && (cfg.Triage || cfg.Review || cfg.Concurrency > 1 && !cfg.NoFootprint) {
		log.Line(time.Now(), "organs off: "+organsOff)
		cfg.Triage, cfg.Review = false, false
	}
	if cfg.Triage {
		orch.StartTriage()
	}

	width := 80
	out, isFile := stdout.(*os.File)
	if isFile {
		if w, _, err := term.GetSize(int(out.Fd())); err == nil {
			width = w
		}
	}
	if cfg.Plain || !isFile || !term.IsTerminal(int(out.Fd())) {
		ctx, cancelRun := context.WithCancelCause(ctx)
		stopWatching := watchSignals(func(s os.Signal) {
			why := "by " + signalName(s)
			if s == os.Interrupt {
				why = "with Ctrl+C"
			}
			cancelRun(dispatch.InterruptedError(why))
		})
		stopDrain := watchDrain(func() { orch.Drain("by " + signalName(drainSignals[0])) })
		sink := tui.Printer{Out: stdout}
		orch.SetSink(sink)
		orch.ReportInterrupt = true
		code := orch.Run(ctx)
		stopDrain()
		if !leaving(stopWatching()) {
			organPhase(orch, cfg, log, code, orch.Final(), sink, cancelOrgans)
		}
		return status(code)
	}

	// Clear the screen so the dashboard starts at the top; earlier output stays in the scrollback.
	// Done here rather than as a Bubble Tea command, which a run that ends at once can outpace.
	fmt.Fprint(stdout, "\x1b[H\x1b[2J")
	// orchestra handles the signals itself: Bubble Tea's handler knows nothing of SIGHUP.
	drain := func(on bool) {
		if on {
			orch.Drain("from the dashboard")
		} else {
			orch.Resume("from the dashboard")
		}
	}
	p := tea.NewProgram(tui.NewDashboard(cfg.Config, cancel, drain), tea.WithInput(stdin), tea.WithOutput(stdout),
		tea.WithoutSignalHandler())
	stopWatching := watchSignals(func(os.Signal) { p.Quit() })
	// the dashboard hears it from the loop
	stopDrain := watchDrain(func() { orch.Drain("by " + signalName(drainSignals[0])) })
	progSink := tui.NewProgramSink(p)
	orch.SetSink(progSink)
	codes := make(chan int, 1)
	ran := make(chan struct{}) // closed once the dashboard has exited and the terminal is restored
	go func() {
		// The loop recovers its workers' panics; one in the loop itself is a bug that ends
		// orchestra, but not with the terminal left in raw mode. Panicking again from here keeps
		// the original stack in the crash.
		defer func() {
			if v := recover(); v != nil {
				log.Line(time.Now(), fmt.Sprintf("PANIC: %v\n\n%s", v, debug.Stack()))
				p.Kill()
				<-ran
				panic(v)
			}
		}()
		codes <- orch.Run(ctx)
		p.Send(tui.Finished{})
	}()
	final, err := p.Run()
	close(ran)
	sig := stopWatching()
	stopDrain()
	if err != nil {
		fmt.Fprintln(stderr, "orchestra:", err)
	}
	sink := tui.Printer{Out: stdout, Styled: true, Width: width}
	m, _ := final.(tui.Dashboard)
	if m.Final() != nil {
		sink.Event(*m.Final())
	}
	// From here the loop's events are printed, starting with any the dashboard never received.
	progSink.Handoff(sink, m.Received())
	if why := stoppedBy(m, err, len(codes) > 0, sig); why != "" {
		// The loop may be in the middle of a command; log the stop and leave the workers to the user.
		cancel()
		msg := dispatch.InterruptLine(why, orch.Running())
		ev := dispatch.Event{Kind: dispatch.EvStop, Text: msg, Time: time.Now()}
		log.Alert(ev.Time, msg)
		sink.Event(ev)
		// Let the loop and its workers stop before triage closes and the reviewer reads its state.
		// Run waits for its workers, whose commands stop with it or at their time limits, and names
		// any not back within a second below the INTERRUPTED line.
		<-codes
		if !leaving(sig) {
			organPhase(orch, cfg, log, dispatch.ExitInterrupted, msg, sink, cancelOrgans)
		}
		return exitStatus(dispatch.ExitInterrupted)
	}
	code := <-codes
	organPhase(orch, cfg, log, code, orch.Final(), sink, cancelOrgans)
	return status(code)
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

// stopSignals stop a run the way Ctrl+C does: SIGINT, kill's SIGTERM, and SIGHUP from closing the
// terminal or Herdr pane.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// watchSignals calls onStop, once and from another goroutine, when orchestra receives one of
// stopSignals. The function it returns stops watching and returns the signal that came, or nil;
// from then on the signals have their default effect again.
func watchSignals(onStop func(os.Signal)) (stop func() os.Signal) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, stopSignals...)
	got := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-sigs:
			got <- s // before onStop, so whatever onStop makes happen finds it
			onStop(s)
		case <-done:
		}
	}()
	return func() os.Signal {
		signal.Stop(sigs)
		close(done)
		select {
		case s := <-got:
			return s
		default:
			return nil
		}
	}
}

// watchDrain calls onDrain, from another goroutine, each time orchestra receives one of
// drainSignals. The function it returns stops watching.
func watchDrain(onDrain func()) (stop func()) {
	if len(drainSignals) == 0 {
		return func() {} // signal.Notify with no signals would catch them all
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, drainSignals...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigs:
				onDrain()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigs)
		close(done)
	}
}

func signalName(s os.Signal) string {
	switch s {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	}
	if len(drainSignals) > 0 && s == drainSignals[0] {
		return "SIGUSR1"
	}
	return s.String()
}

// leaving reports whether sig asks orchestra to go away rather than stop the run: SIGTERM, or
// SIGHUP when nobody is left to read the report. The organ phase is skipped then.
func leaving(sig os.Signal) bool { return sig == syscall.SIGTERM || sig == syscall.SIGHUP }

// organs is what organPhase needs from the loop.
type organs interface {
	FinishTriage(ctx context.Context)
	Review(ctx context.Context, code int, final string) (string, error)
	SaveReport(report string) (string, error)
}

// organPhase runs after the loop stops: it waits for pending triage, then has the reviewer write
// the run report. Ctrl+C, or another of stopSignals, skips whatever is left.
func organPhase(
	orch organs, c options, log *dispatch.Log, code int, final string, out tui.Printer, cancelOrgans func(),
) {
	if !c.Triage && !c.Review {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), stopSignals...)
	defer stop()
	go func() {
		<-ctx.Done()
		cancelOrgans()
	}()
	if c.Triage {
		out.Say("finishing triage…")
		orch.FinishTriage(ctx)
	}
	if !c.Review || ctx.Err() != nil {
		return
	}
	out.Say("writing the run report with claude… (ctrl+c skips)")
	report, err := orch.Review(ctx, code, final)
	if err != nil {
		if ctx.Err() == nil {
			msg := "REVIEW_FAILED: " + dispatch.FirstLine(err.Error())
			log.Alert(time.Now(), msg)
			out.Say(msg)
		}
		return
	}
	out.Report(report)
	path, err := orch.SaveReport(report)
	if err != nil {
		msg := "report not saved: " + dispatch.FirstLine(err.Error())
		log.Line(time.Now(), msg)
		out.Say(msg)
		return
	}
	log.Alert(time.Now(), "REPORT written to "+path)
	out.Say("report saved to " + tui.Tildify(path))
}
