package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
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

func envInt(getenv func(string) string, name string, def int, problems *[]string) int {
	v := getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		*problems = append(*problems, fmt.Sprintf("%s must be a whole number (got '%s').", name, v))
	}
	return n
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

// loadConfig reads the flags in args (defaulting to the environment variables orchestrate.sh used)
// and collects every setup problem, so they can be reported together. The error is the flag
// package's: flag.ErrHelp after -h, or a malformed flag.
func loadConfig(args []string, getenv func(string) string, output io.Writer) (options, []string, error) {
	var c options
	var problems []string
	fs := flag.NewFlagSet("orchestra", flag.ContinueOnError)
	fs.SetOutput(output)

	fs.StringVar(&c.Workspace, "workspace", "", "Herdr workspace for the worker tabs (default: the one orchestra runs in; list IDs with: herdr workspace list)")
	fs.IntVar(&c.Limit, "limit", envInt(getenv, "LIMIT", 40, &problems), "stop after this many tickets in total [LIMIT]")
	fs.IntVar(&c.DoneSoFar, "done-so-far", envInt(getenv, "DONE_SO_FAR", 0, &problems), "tickets dispatched in earlier runs, counted toward -limit [DONE_SO_FAR]")
	fs.StringVar(&c.AgentKind, "agent", envOr(getenv, "AGENT_KIND", "claude"), "Herdr agent kind for the workers [AGENT_KIND]")
	fs.StringVar(&c.WorkerPrompt, "prompt", getenv("WORKER_PROMPT"), "worker instructions with TICKET_ID as placeholder (default: .orchestra/worker-prompt.md, or .claude/worker-prompt.md in a project set up before 'orchestra init') [WORKER_PROMPT]")
	fs.BoolVar(&c.Notify, "notify", getenv("NOTIFY") != "0", "macOS notifications for finished tickets and stops [NOTIFY=0 turns off]")
	fs.StringVar(&c.WTRoot, "worktrees", getenv("WT_ROOT"), "folder for the per-ticket worktrees, outside the repository (default: <repo>-worktrees next to it) [WT_ROOT]")
	fs.BoolVar(&c.Triage, "triage", getenv("TRIAGE") != "0", "triage each deferred ticket with claude and note a recommendation on it [TRIAGE=0 turns off]")
	fs.BoolVar(&c.Review, "review", getenv("REVIEW") != "0", "write a run report with claude when the loop stops [REVIEW=0 turns off]")
	fs.StringVar(&c.OrganModel, "organ-model", getenv("ORGAN_MODEL"), "model for triage and the report (default: the claude CLI's default) [ORGAN_MODEL]")
	concurrent := envInt(getenv, "ORCHESTRA_CONCURRENT", 0, &problems)
	fs.IntVar(&c.Concurrency, "concurrent", concurrent, "tickets to work on at the same time (default: .orchestra/settings.json, else 1) [ORCHESTRA_CONCURRENT]")
	fs.IntVar(&c.Concurrency, "c", concurrent, "shorthand for --concurrent")
	fs.BoolVar(&c.LaunchPrompt, "prompt-at-launch", getenv("PROMPT_AT_LAUNCH") != "0", "start Claude workers with their prompt instead of pasting it in [PROMPT_AT_LAUNCH=0 turns off]")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.BoolVar(&c.Plain, "plain", false, "print plain log lines instead of the interactive view (automatic when not on a terminal)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: orchestra [flags]\n       orchestra init [--check \"<command>\"] [--concurrent N] [--force]\n\nWork through 'bd ready' one ticket at a time, one agent per Herdr tab and git worktree.\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(fs.Output(), "\nExit codes: 0 done, 2 setup problem, 3 worker blocked or paused, 4 Herdr/Beads/git failure,\n5 main checkout dirty or off its branch, 6 merge failed, 130 Ctrl+C.\n")
	}
	if err := fs.Parse(args); err != nil {
		return c, nil, err
	}
	c.showVersion = *showVersion
	if c.showVersion {
		return c, nil, nil
	}

	if out, err := command.Output("", "git", "rev-parse", "--show-toplevel"); err == nil {
		c.Repo = strings.TrimSpace(out)
	} else {
		problems = append(problems, "Not inside a git repository: cd into the project first.")
	}
	if c.Workspace == "" {
		c.Workspace = herdr.CurrentWorkspace(getenv)
	}
	if c.Workspace == "" && getenv("HERDR_ENV") == "1" {
		problems = append(problems, "Could not tell which Herdr workspace this pane is in. Find the ID with 'herdr workspace list', then run: orchestra --workspace <id>")
	}
	if getenv("HERDR_ENV") != "1" {
		problems = append(problems, "Not running inside a Herdr pane (HERDR_ENV is not 1). Start 'herdr' and run this from a pane.")
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
		if n, err := project.ResolveConcurrency(c.Concurrency, settings); err != nil {
			problems = append(problems, err.Error()+".")
		} else {
			c.Concurrency = n
		}
		if b, err := os.ReadFile(c.WorkerPrompt); err != nil {
			problems = append(problems, "Worker prompt not found: "+c.WorkerPrompt+". Set the project up with: orchestra init")
		} else if !strings.Contains(string(b), "TICKET_ID") {
			problems = append(problems, "Worker prompt has no TICKET_ID placeholder: "+c.WorkerPrompt)
		}
		if st, err := os.Stat(filepath.Join(c.Repo, ".beads")); err != nil || !st.IsDir() {
			problems = append(problems, "No Beads database in "+c.Repo+". Run: bd init")
		}

		// Finished tickets are merged into the main checkout's branch, so run from there, on a branch.
		gitDir, _ := command.Output(c.Repo, "git", "rev-parse", "--absolute-git-dir")
		commonDir, _ := command.Output(c.Repo, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
		if strings.TrimSpace(gitDir) != strings.TrimSpace(commonDir) {
			problems = append(problems, c.Repo+" is a linked worktree. Run this from the main checkout.")
		}
		if c.Base = (git.Git{}).CurrentBranch(c.Repo); c.Base == "" {
			problems = append(problems, "The main checkout is on a detached HEAD. Check out the branch finished tickets should land on.")
		}

		if c.WTRoot == "" {
			c.WTRoot = filepath.Join(filepath.Dir(c.Repo), filepath.Base(c.Repo)+"-worktrees")
		}
		if abs, err := filepath.Abs(c.WTRoot); err == nil {
			c.WTRoot = abs
		}
		if c.WTRoot == c.Repo || strings.HasPrefix(c.WTRoot, c.Repo+"/") {
			problems = append(problems, fmt.Sprintf("WT_ROOT (%s) must be outside the repository, or git sees the worktrees as untracked files.", c.WTRoot))
		}
	}
	return c, problems, nil
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

// run is orchestra: 'orchestra init …' or a run. It returns nil or an exitStatus.
func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) > 1 && args[1] == "init" {
		return status(runInit(".", args[2:]))
	}
	cfg, problems, err := loadConfig(args[1:], getenv, stderr)
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

	prompt, _ := os.ReadFile(cfg.WorkerPrompt)
	os.MkdirAll(cfg.WTRoot, 0o755)
	os.MkdirAll(filepath.Dir(cfg.LogPath), 0o755)
	log, err := dispatch.OpenLog(cfg.LogPath, cfg.Notify, filepath.Base(cfg.Repo))
	if err != nil {
		fmt.Fprintln(stderr, "orchestra cannot open its log:", err)
		return exitStatus(dispatch.ExitSetup)
	}
	if err := project.EnsureRunExcluded(cfg.Repo); err != nil {
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
	tracker, terminal, repo := beads.Tracker{Repo: cfg.Repo}, herdr.Terminal{}, git.Git{}
	orch := dispatch.New(cfg.Config, log, string(prompt), dispatch.Deps{
		Tickets:   tracker,
		Notes:     tracker,
		Tabs:      terminal,
		Starter:   terminal,
		Namer:     terminal,
		Agents:    terminal,
		Checkout:  repo,
		Worktrees: repo,
		Merger:    repo,
		History:   repo,
		Advisor:   organ.Client{Bin: "claude", Model: cfg.OrganModel},
		AdviceCtx: organCtx,
	})
	if off := organ.Unavailable("claude"); off != "" && (cfg.Triage || cfg.Review) {
		log.Line(time.Now(), "organs off: "+off)
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
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
		orch.SetSink(tui.Printer{})
		orch.ReportInterrupt = true
		code := orch.Run(ctx)
		stop()
		organPhase(orch, cfg, log, code, orch.Final(), tui.Printer{}, cancelOrgans)
		return status(code)
	}

	// Clear the screen so the dashboard starts at the top; earlier output stays in the scrollback.
	// Done here rather than as a Bubble Tea command, which a run that ends at once can outpace.
	fmt.Fprint(stdout, "\x1b[H\x1b[2J")
	p := tea.NewProgram(tui.NewDashboard(cfg.Config, cancel), tea.WithInput(stdin), tea.WithOutput(stdout))
	orch.SetSink(tui.NewProgramSink(p))
	codes := make(chan int, 1)
	go func() {
		codes <- orch.Run(ctx)
		p.Send(tui.Finished{})
	}()
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(stderr, "orchestra:", err)
	}
	sink := tui.Printer{Styled: true, Width: width}
	m, _ := final.(tui.Dashboard)
	if m.Interrupted() {
		// The loop may be in the middle of a command; log the stop and leave the workers to the user.
		msg := "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree"
		if running := m.Running(); len(running) > 0 {
			var names []string
			for _, st := range running {
				names = append(names, fmt.Sprintf("%s (tab %s)", st.Ticket, st.Tab))
			}
			msg = fmt.Sprintf("INTERRUPTED: stopped with Ctrl+C while %s were running; their tabs and worktrees are left open", strings.Join(names, ", "))
		}
		ev := dispatch.Event{Kind: dispatch.EvStop, Text: msg, Time: time.Now()}
		log.Line(ev.Time, msg)
		sink.Event(ev)
		// Let the loop notice the cancellation before the reviewer reads its state.
		select {
		case <-codes:
		case <-time.After(15 * time.Second):
		}
		orch.SetSink(sink)
		organPhase(orch, cfg, log, dispatch.ExitInterrupted, msg, sink, cancelOrgans)
		return exitStatus(dispatch.ExitInterrupted)
	}
	if m.Final() != nil {
		sink.Event(*m.Final())
	}
	code := <-codes
	orch.SetSink(sink)
	organPhase(orch, cfg, log, code, orch.Final(), sink, cancelOrgans)
	return status(code)
}

// organPhase runs after the loop stops: it waits for pending triage, then has the reviewer write
// the run report. Ctrl+C skips whatever is left.
func organPhase(orch *dispatch.Loop, c options, log *dispatch.Log, code int, final string, out tui.Printer, cancelOrgans func()) {
	if !c.Triage && !c.Review {
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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
	report, path, err := orch.Review(ctx, code, final)
	if err != nil {
		if ctx.Err() == nil {
			msg := "REVIEW_FAILED: " + firstLine(err.Error())
			log.Line(time.Now(), msg)
			out.Say(msg)
		}
		return
	}
	fmt.Println()
	out.Report(report)
	log.Line(time.Now(), "REPORT written to "+path)
	out.Say("report saved to " + tui.Tildify(path))
}
