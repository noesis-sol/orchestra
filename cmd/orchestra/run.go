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
	"runtime/debug"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"

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
	case errors.Is(err, flag.ErrHelp):
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
