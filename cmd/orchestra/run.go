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
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
	"github.com/noesis-sol/orchestra/internal/update"

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

// cmdEnv is what a command reads and writes besides its arguments: the working directory, the
// environment, the three standard streams, this build's version and where its releases are. The
// commands it runs get the environment the context gives them (command.WithEnv).
type cmdEnv struct {
	dir            string // relative paths, such as --spec's, are relative to it
	getenv         func(string) string
	stdin          io.Reader
	stdout, stderr io.Writer
	version        string                         // this build's version, buildVersion's
	releases       update.Updater                 // where the latest release is read: GitHub for the zero Updater
	breakdown      func(s *session) specBreakdown // a spec run's breakdown, nil for breakdownOf's: tests give their own
}

// subcommand is a command of orchestra's other than a run, named by the argument after 'orchestra'.
type subcommand struct {
	name string
	// synopsis is its flags, as its usage line gives them after 'orchestra <name> ', a newline where
	// the line breaks: the main usage, the hint for flags given before it and its own -h all read it.
	synopsis string
	run      func(ctx context.Context, args []string, e cmdEnv) int // its exit code
}

// subcommands are orchestra's subcommands, in the order the main usage lists them.
var subcommands = []subcommand{
	{"init", initSynopsis, func(ctx context.Context, args []string, e cmdEnv) int {
		return runInit(ctx, e.dir, args, e.getenv, e.stdin, e.stdout, e.stderr)
	}},
	{"plan", planSynopsis, func(ctx context.Context, args []string, e cmdEnv) int {
		return runPlan(ctx, e.dir, args, e.stdout, e.stderr)
	}},
	{"update", updateSynopsis, func(ctx context.Context, args []string, e cmdEnv) int {
		return runUpdateHere(ctx, args, e)
	}},
}

// findSubcommand is the subcommand called name, if there is one.
func findSubcommand(name string) (subcommand, bool) {
	for _, c := range subcommands {
		if c.name == name {
			return c, true
		}
	}
	return subcommand{}, false
}

// usageLines is the usage line of 'orchestra name' with flags synopsis, after prefix: its lines
// after the first lined up under the first flag, each ending in a newline.
func usageLines(prefix, name, synopsis string) string {
	head := prefix + "orchestra " + name + " "
	return head + strings.ReplaceAll(synopsis, "\n", "\n"+strings.Repeat(" ", len(head))) + "\n"
}

// parseArgs parses the flags of 'orchestra cmd' in args with fs, whose output is stderr, and
// refuses a positional argument. ok is false where the command stops there with code: after -h, or
// once fs's output says what is wrong with them.
func parseArgs(fs *flag.FlagSet, args []string, cmd string) (code int, ok bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return dispatch.ExitOK, false
		}
		return dispatch.ExitSetup, false
	}
	if rest := fs.Args(); len(rest) > 0 {
		fmt.Fprintf(fs.Output(), "orchestra %s: unexpected argument %q (see orchestra %s -h)\n", cmd, rest[0], cmd)
		return dispatch.ExitSetup, false
	}
	return dispatch.ExitOK, true
}

// run is orchestra: a subcommand ('orchestra init …', 'orchestra plan …', 'orchestra update …') or a
// run. It returns nil or an exitStatus.
//
// A run reads its configuration, opens its session (held until it ends: the lock, the stop
// signals' watch, the log), settles what it works on (the question, and a feature typed there or
// given with --feature), then runs its loop and the organ phase after it.
func run(ctx context.Context, args []string, e cmdEnv) (err error) {
	if len(args) > 1 {
		if args[1] == "hook" { // hidden: Claude Code runs it as a worker's hook, which package claude serves itself
			return status(claude.Hook(args[2:], e.getenv, e.stdin, e.stderr))
		}
		if c, ok := findSubcommand(args[1]); ok {
			return status(c.run(ctx, args[2:], e))
		}
	}
	cfg, code, ok := configure(ctx, args[1:], e)
	if !ok {
		return status(code)
	}
	s, code := openSession(ctx, cfg, e)
	defer s.close(&err)
	if code != dispatch.ExitOK {
		return status(code)
	}
	if code, ok := s.chooseWork(ctx); !ok {
		return status(code)
	}
	return status(s.runLoop(ctx))
}

// configure reads a run's flags in args and its setup, and reports what stops it before it starts:
// after -h or --version, or with a problem, ok is false and the run ends with code, all said.
func configure(ctx context.Context, args []string, e cmdEnv) (cfg options, code int, ok bool) {
	cfg, problems, err := loadConfig(ctx, e.dir, args, e.getenv, e.stderr)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return cfg, dispatch.ExitOK, false
	case err != nil:
		return cfg, dispatch.ExitSetup, false
	case cfg.showVersion:
		fmt.Fprintln(e.stdout, "orchestra", e.version)
		return cfg, dispatch.ExitOK, false
	}
	if cfg.notSetUp || len(problems) > 0 {
		heading := "orchestra cannot start:"
		if cfg.notSetUp {
			fmt.Fprintln(e.stderr, notSetUpMessage)
			heading = "\nAlso fix before a run:"
		}
		if len(problems) > 0 {
			fmt.Fprintln(e.stderr, heading)
		}
		for _, p := range problems {
			fmt.Fprintln(e.stderr, "  - "+p)
		}
		return cfg, dispatch.ExitSetup, false
	}
	return cfg, dispatch.ExitOK, true
}

// session is a run's setup, held until it ends: its lock, the worker prompt, the stop signals'
// watch, its log and the check for a newer release, and what the run settles on as it goes.
type session struct {
	cfg     options
	e       cmdEnv
	started time.Time      // the run's start, as its lock and each record of its event stream give it
	holder  project.Holder // what the lock says of the run
	lock    *project.RunLock
	prompt  string
	stops   *stopWatch
	log     *dispatch.Log
	undo    []func(err error) // what close does, the last added first, given the run's result
	// filing is a spec's breakdown going on as the loop starts on its environment ticket
	// (spec_early.go); nil for none
	filing *specFiling
	say    *tui.Printer // what printer gives, once it has
}

// printer is where the run's lines go before its loop, and beside the spec's breakdown that goes on
// as the loop starts: a terminal's printer, which draws a busy line and prints above it, unless the
// run is plain. Each call gives a copy of the same printer, sharing its busy line.
func (s *session) printer() tui.Printer {
	if s.say == nil {
		c, e := s.cfg, s.e
		say := tui.Printer{Out: e.stdout}
		if out, ok := e.stdout.(*os.File); ok && !plainOutput(c.Plain, e.getenv, e.stdin, e.stdout) {
			say = tui.Terminal(out, termWidth(out))
		}
		s.say = &say
	}
	return *s.say
}

// onClose adds f to what close does, before everything added so far.
func (s *session) onClose(f func(err error)) { s.undo = append(s.undo, f) }

// close undoes what openSession set up, the last first, *err being the run's result: the check for a
// newer release says what it found, the log records the end and closes, the stop signals have their
// default effect again, and the lock is released.
func (s *session) close(err *error) {
	for _, undo := range slices.Backward(s.undo) {
		undo(*err)
	}
}

// openSession sets up the run cfg describes, and goes into its repository. The session is never nil:
// where code isn't 0 it stopped there, all said, and its close undoes what it got to.
func openSession(ctx context.Context, cfg options, e cmdEnv) (s *session, code int) {
	s = &session{cfg: cfg, e: e, started: time.Now()}
	// One run at a time in a repository: a second would race this one for the same tickets, worktrees
	// and branch. Taken before anything changes, a --feature request's session included, and held
	// until orchestra exits, after the organ phase.
	s.holder = project.Holder{
		PID: os.Getpid(), Started: s.started, Version: e.version, Branch: cfg.Base,
		Ticket: cfg.Ticket, Feature: cfg.Feature, Spec: cfg.Spec, Pane: cfg.where.pane,
	}
	lock, lockErr := project.LockRun(ctx, cfg.Repo, s.holder)
	var held *project.HeldError
	switch {
	case errors.As(lockErr, &held):
		fmt.Fprintf(e.stderr, "orchestra cannot start:\n  - %v. One run at a time works on a repository: "+
			"follow that one, or stop it first.\n", held)
		return s, dispatch.ExitSetup
	case errors.Is(lockErr, errors.ErrUnsupported): // logged below
	case lockErr != nil:
		fmt.Fprintln(e.stderr, "orchestra cannot take its run lock:", lockErr)
		return s, dispatch.ExitSetup
	}
	s.lock = lock
	s.onClose(func(error) { _ = lock.Close() }) // only releases it; orchestra exiting would too

	if cfg.specSetup == nil { // else read once the spec run's setup has written it (spec_setup.go)
		if code := s.readPrompt(); code != dispatch.ExitOK {
			return s, code
		}
	}
	for _, dir := range []string{cfg.WTRoot, filepath.Dir(cfg.LogPath)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(e.stderr, "orchestra cannot create its folder:", err)
			return s, dispatch.ExitSetup
		}
	}
	// From here to the end, closing the log included, a stop signal doesn't end orchestra outright.
	s.stops = catchStops()
	s.onClose(func(error) { s.stops.release() })
	var err error
	if s.log, err = dispatch.OpenLogContext(ctx, cfg.LogPath, cfg.Notify, filepath.Base(cfg.Repo)); err != nil {
		fmt.Fprintln(e.stderr, "orchestra cannot open its log:", err)
		return s, dispatch.ExitSetup
	}
	s.onClose(func(err error) { // records how the run ended, and shows its last notifications before orchestra exits
		s.log.End(exitCode(err))
		if err := s.log.Close(); err != nil {
			fmt.Fprintln(e.stderr, "orchestra cannot close its log:", err)
		}
	})
	if errors.Is(lockErr, errors.ErrUnsupported) {
		s.log.Line(time.Now(), "no run lock on "+runtime.GOOS+": nothing stops a second run in this repository")
	}
	// A newer release is looked for while the run goes, and mentioned as it ends (newer.go).
	check := startUpdateCheck(ctx, e)
	s.onClose(func(error) { showNewer(check, !cfg.Plain && isTerminal(e.stdout), e) })
	if err := project.EnsureRunExcluded(ctx, cfg.Repo); err != nil {
		s.log.Raw("", fmt.Errorf("cannot keep %s/%s/ out of git: %w", project.Dir, project.RunName, err))
	}
	return s, dispatch.ExitOK
}

// readPrompt reads the worker prompt, for the run's workers. It returns the exit code, having said
// what went wrong.
func (s *session) readPrompt() int {
	prompt, err := os.ReadFile(s.cfg.WorkerPrompt)
	if err != nil {
		fmt.Fprintln(s.e.stderr, "orchestra cannot read the worker prompt:", err)
		return dispatch.ExitSetup
	}
	s.prompt = string(prompt)
	return dispatch.ExitOK
}

// chooseWork settles what the run works on: in a terminal it asks, and a feature typed there or
// given with --feature is talked through with the user and filed, the run then scoped to its epic; a
// spec chosen there or given with --spec is committed and broken down, the run then scoped to its
// root epic. It records the run's start. ok is false where the run ends there with code: the question
// left, a spec chosen that can't be run, or nothing filed to run.
func (s *session) chooseWork(ctx context.Context) (code int, ok bool) {
	cfg, e := &s.cfg, s.e
	if asksWork(*cfg, isTerminal(e.stdin) && isTerminal(e.stdout)) {
		work, code := askWork(ctx, s.stops, *cfg, e.stdin, e.stdout, e.stderr)
		if code != dispatch.ExitOK { // before the run's start: nothing to record
			return code, false
		}
		switch work.Kind {
		case tui.WorkFeature:
			s.featureChosen(work)
		case tui.WorkSpec:
			if code := s.specChosen(ctx, work); code != dispatch.ExitOK {
				return code, false
			}
		}
	}
	filedCode := dispatch.ExitOK
	switch { // the run is scoped to the epic filed
	case cfg.specFile != "": // broken down from the spec, once it is committed (spec.go)
		cfg.Ticket, filedCode = s.runSpec(ctx)
	case cfg.Feature != "": // talked through with the user, and filed, in a Claude Code session (feature_refine.go)
		cfg.Ticket, filedCode = s.talkFeature(ctx)
	}
	s.log.Begin(cfg.Repo, dispatch.RunStart{Started: s.started, Version: s.e.version, Repo: cfg.Repo,
		Branch: cfg.Base, Scope: cfg.Ticket, Feature: cfg.Feature, Spec: cfg.Spec, Concurrency: cfg.Concurrency})
	if (cfg.Feature != "" || cfg.specFile != "") && cfg.Ticket == "" { // nothing filed to run
		return filedCode, false
	}
	return dispatch.ExitOK, true
}

// runLoop runs the loop, plain or on the dashboard, and the organ phase after it, unless there is
// nothing to run. It returns the exit code.
func (s *session) runLoop(ctx context.Context) int {
	cfg, e := s.cfg, s.e
	out, _ := e.stdout.(*os.File)
	plain := plainOutput(cfg.Plain, e.getenv, e.stdin, e.stdout)
	if s.filing != nil { // the spec's breakdown goes on: the loop starts on its environment ticket alone
		cfg.Filing = s.filing.gate()
	} else if nothingToRun(ctx, cfg, s.log, e.stdout, plain) {
		return dispatch.ExitOK
	}

	// The organs outlast the loop's context, keeping only the environment it gives commands.
	base := context.WithoutCancel(ctx)
	organCtx, cancelOrgans := context.WithCancel(base)
	defer cancelOrgans()
	cfg.Version, cfg.Machine = s.e.version, runsDir(e.getenv)
	orch := newLoop(organCtx, &cfg, s.log, s.prompt)
	if cfg.Triage {
		orch.StartTriage()
	}
	r := loopRun{orch: orch, cfg: cfg, stops: s.stops, log: s.log, base: base, cancelOrgans: cancelOrgans,
		filing: s.filing}
	switch {
	case plain:
		return runPlain(ctx, r, e.stdout)
	case s.filing != nil:
		return runFiling(ctx, r, s.printer(), e.stdin, out, e.stderr)
	}
	return runDashboard(ctx, r, e.stdin, out, e.stderr)
}

// plainOutput reports whether the run prints plain lines rather than drawing its dashboard: when
// asked to (--plain), when stdout isn't a terminal, when the terminal can't draw one (TERM=dumb, as the
// forms read it for their accessible form), or when stdin isn't one, as the dashboard's keys come in
// on it.
func plainOutput(asked bool, getenv func(string) string, stdin io.Reader, stdout io.Writer) bool {
	return asked || getenv("TERM") == "dumb" || !isTerminal(stdin) || !isTerminal(stdout)
}

// nothingToRun ends the run c before its loop when it has nothing to run (see
// dispatch.CheckNothingToRun), and reports whether it did: it says why on stdout, plain or in a box,
// and gives the run's done line to the log and the event stream, as the loop's end would, without
// a notification; run's deferred End records the end, with code 0. Nothing is cleared, opened or
// asked of claude. A run whose limit is reached is left to the loop, which ends it LIMIT_REACHED.
// A spec run whose root epic is all done says the spec is built.
func nothingToRun(ctx context.Context, c options, log *dispatch.Log, stdout io.Writer, plain bool) bool {
	if c.DoneSoFar >= c.Limit {
		return false
	}
	tracker, repo := beads.Tracker{Repo: c.Repo, ExcludeTypes: c.ExcludeTypes}, git.Git{}
	n, err := dispatch.CheckNothingToRun(ctx, c.Config, tracker, repo, repo)
	if err != nil || n == nil { // what the check couldn't read, the loop reads again and reports
		return false
	}
	if c.Spec != "" && n.AllDone && n.Scope != "" {
		n.Built = c.Spec
	}
	ev := dispatch.Event{Kind: dispatch.EvDone, N: c.DoneSoFar, Limit: c.Limit, Text: n.Done, Time: time.Now()}
	log.Line(ev.Time, ev.Text)
	log.Record(ev)
	if n.FreeErr != nil {
		log.Raw("", n.FreeErr) // the box goes without its deferred tickets free to run
	}
	sink := tui.Printer{Out: stdout}
	if out, ok := stdout.(*os.File); ok && !plain {
		sink.Styled, sink.Width = true, termWidth(out)
	}
	sink.Nothing(*n)
	return true
}

// termWidth is the width of the terminal out, or 80 when it can't be read or says it has no columns,
// as a fresh pseudo-terminal can.
func termWidth(out *os.File) int {
	if w, _, err := term.GetSize(int(out.Fd())); err == nil && w > 0 {
		return w
	}
	return 80
}

// newLoop makes the run's loop on Beads, the terminal (Herdr or tmux), git and the workers' reports,
// its organs advising under organCtx. Organs that can't run are turned off in cfg, and the log says so.
func newLoop(organCtx context.Context, cfg *options, log *dispatch.Log, prompt string) *dispatch.Loop {
	organsOff := organ.Unavailable(organCtx, "claude")
	cfg.Predict = organsOff == "" // the predictor goes with footprints, which the loop checks
	tracker, terminal, repo := beads.Tracker{Repo: cfg.Repo, ExcludeTypes: cfg.ExcludeTypes}, terminalOf(*cfg), git.Git{}
	orch := dispatch.New(cfg.Config, log, prompt, dispatch.Deps{
		Tickets:   tracker,
		Notes:     tracker,
		Tabs:      terminal,
		Starter:   terminal,
		Namer:     terminal,
		Agents:    terminal,
		Processes: terminal,
		Reporter:  claude.NewReporter(func(text string) { log.Line(time.Now(), text) }),
		Help:      claude.CLI{Bin: "claude"},
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
