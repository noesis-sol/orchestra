package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A spec run with --refine breaks the spec down with the user, who is there. The spec is mostly
// settled, so the interactive Claude Code session asks only where it leaves open a decision that
// changes what gets built and that neither the spec nor the repository answers, in the feature
// interview's rounds; then it sums up the epics and tickets it will file, files them once the user
// agrees, with the headless breakdown's rules (the refine sections of internal/project's
// spec-prompt.md), and hands the root epic back in .orchestra/run/spec.json, the one file it writes.
// The session runs as the feature interview's does: in a pane split off orchestra's, to its right
// (a paneSession about the spec), or, when that can't be done, on orchestra's own terminal. Once
// spec.json names the root epic and claude's turn has ended, orchestra closes the pane, checks the
// tree as after any breakdown (specRun.check), and starts the run on it without asking. Leaving the
// session (/exit, or closing its pane) without that files nothing more and ends the run.

// errNotHandedBack is a refinement's error when the user left it before it handed a root epic back.
var errNotHandedBack = errors.New("the refinement ended without handing back a root epic")

// errRefineNeedsTerminal is a refinement's error where it would take orchestra's terminal and there
// is none to take: the session is the user's to answer.
var errRefineNeedsTerminal = errors.New("refining the spec needs you at a terminal, and orchestra's input or " +
	"output isn't one: run it in a terminal, or without --refine to have the spec broken down unattended")

// refineCountEvery is how often orchestra's pane counts, from bd, what a refinement in the pane beside
// it has filed.
const refineCountEvery = 5 * time.Second

// refineBreakdown breaks a spec down with the user (--refine), as a specBreakdown.
type refineBreakdown struct {
	repo  string // the main checkout, where claude runs
	pane  string // orchestra's own pane; "" when it runs in none, and the session takes its terminal
	panes interviewPanes
	hooks bool // claude reports through hooks (see paneSession)
	// Where orchestra's pane shows the session's progress; nil when its output is plain.
	line    *tui.InterviewLine
	tracker specLister // bd, which counts what the session files, and has its root epic if it stops
	// terminal runs the session on orchestra's own terminal, message its first message, in update mode
	// with update: where orchestra runs in no pane, or the pane can't be opened.
	terminal func(ctx context.Context, message string, update bool) error
	log      *dispatch.Log // nil in tests
	out, err io.Writer
}

// newRefineBreakdown is the refinement of the spec run s: in a pane split off orchestra's when it
// runs in one, its progress shown there unless the run is plain.
func newRefineBreakdown(s *session) refineBreakdown {
	c, e := s.cfg, s.e
	b := refineBreakdown{repo: c.Repo, pane: s.holder.Pane, tracker: beads.Tracker{Repo: c.Repo}, log: s.log,
		out: e.stdout, err: e.stderr}
	if b.pane != "" {
		b.panes, b.hooks = terminalOf(c), terminalNamed(c.Terminal).hookDriven
	}
	if out, ok := e.stdout.(*os.File); ok && !plainOutput(c.Plain, e.getenv, e.stdin, e.stdout) {
		b.line = &tui.InterviewLine{Out: out, Width: func() int { return termWidth(out) }}
	}
	b.terminal = func(ctx context.Context, message string, update bool) error {
		return refineOnTerminal(ctx, s.stops, c.Repo, message, update, e.stdin, e.stdout, e.stderr)
	}
	return b
}

// BreakDown runs the session on the spec r names, and returns the root epic it handed back. Stopped,
// or failing, it names the root epic the session filed, if any, as bd has it; ended by the user
// without a hand-back, it is errNotHandedBack.
func (b refineBreakdown) BreakDown(ctx context.Context, r breakdownRequest) (breakdownResult, error) {
	if why := organ.Unavailable("claude"); why != "" {
		return breakdownResult{}, errors.New(why)
	}
	prompt, err := project.WriteRefinePrompt(b.repo, r.Update != nil)
	if err != nil {
		return breakdownResult{}, fmt.Errorf("cannot write the refinement's instructions: %w", err)
	}
	message := refineMessage(r.Spec, r.Update)
	terminal := func(ctx context.Context, _ string) error { return b.terminal(ctx, message, r.Update != nil) }
	session := terminal
	if b.pane != "" {
		session = paneSession{panes: b.panes, pane: b.pane, hooks: b.hooks, repo: b.repo, fallback: terminal,
			out: b.out, err: b.err, progress: b.line,
			about: refineSubject{spec: r.Spec, update: r.Update, tracker: b.tracker, every: refineCountEvery}}.talk
	}
	b.logLine("SPEC: refining " + r.Path + " with claude and you")
	err = session(ctx, prompt)
	root, rootErr := project.FiledSpec(b.repo)
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		if root == "" {
			root = filedRoot(ctx, b.tracker, r.Spec)
		}
		return breakdownResult{Root: root}, ctx.Err()
	case errors.As(err, &exit):
		fmt.Fprintln(b.err, "claude ended with", exit) // it may have handed the root epic back all the same
	case err != nil:
		return breakdownResult{Root: filedRoot(ctx, b.tracker, r.Spec)}, err
	}
	switch {
	case errors.Is(rootErr, project.ErrNoSpecRoot):
		return breakdownResult{}, errNotHandedBack
	case rootErr != nil:
		return breakdownResult{Root: filedRoot(ctx, b.tracker, r.Spec)},
			fmt.Errorf("can't read the root epic it handed back: %w", rootErr)
	}
	return breakdownResult{Root: root}, nil
}

// refineMessage is the refinement's first message, one line: the spec it breaks down with the user;
// in update mode (u), the root epic whose tree it updates for the spec's change, the version of the
// spec that tree was filed for, and where to read the change and the tree.
func refineMessage(s project.Spec, u *specUpdate) string {
	if u == nil {
		return fmt.Sprintf("Break the spec at %s down into Beads tickets with me (sha256 %s, commit %s).", s.Path,
			s.SHA256, s.Commit)
	}
	return fmt.Sprintf("The spec at %s has changed since its Beads tickets were filed under %s (for sha256 %s, "+
		"commit %s): update them for the change with me (sha256 %s, commit %s). The change and the tree are in %s.",
		s.Path, u.Root, u.Was.SHA256, cmp.Or(u.Was.Commit, "not recorded"), s.SHA256, s.Commit, u.File)
}

// refineOnTerminal runs the refinement on orchestra's own terminal, message its first message, in
// update mode with update, until the user exits it. orchestra can't end the session there once it
// has handed back, as it does in a pane, so its instructions, rewritten for the terminal, have claude
// say how to. Ctrl+C is Claude Code's meanwhile, which interrupts and clears with it.
func refineOnTerminal(ctx context.Context, stops *stopWatch, repo, message string, update bool, stdin io.Reader,
	stdout, stderr io.Writer) error {
	if !isTerminal(stdin) || !isTerminal(stdout) {
		return errRefineNeedsTerminal
	}
	prompt, err := project.WriteTerminalRefine(repo, update)
	if err != nil {
		return fmt.Errorf("cannot write the refinement's instructions: %w", err)
	}
	fmt.Fprintln(stdout, "Breaking the spec down with claude, which asks you only what the spec leaves open and "+
		"files the tickets once you agree. Type /exit to come back.")
	defer stops.dropInterrupts()()
	defer keepTerminal(stdin)()
	// -- ends claude's options: the message after it is none of them.
	err = command.Interactive(ctx, repo, stdin, stdout, stderr,
		"claude", "--append-system-prompt-file", prompt, "--", message)
	if e, ok := errors.AsType[*command.Error](err); ok && !errors.As(err, new(*exec.ExitError)) {
		e.Args = nil // the instructions' path and the message would bury why it failed
		return fmt.Errorf("couldn't start %w", e)
	}
	return err
}

// notHandedBack ends a spec run whose refinement the user left without it handing a root epic back
// (/exit, or closing its pane): with nothing filed for the spec, there is nothing to run, and the run
// ends there, exit 0; else orchestra says what was filed, as after a breakdown that didn't finish. It
// returns the exit code.
func (r specRun) notHandedBack(ctx context.Context, spec project.Spec) int {
	if issues, err := r.tracker.WithMetadata(ctx, dispatch.SpecKey, spec.Path); err == nil && len(issues) == 0 {
		fmt.Fprintln(r.out, "The refinement filed nothing; nothing to run.")
		r.logLine("SPEC: the refinement ended with nothing filed for " + spec.Path)
		return dispatch.ExitOK
	}
	if r.update != nil {
		fmt.Fprintf(r.err, "orchestra: the refinement ended without handing back %s, so its tree isn't updated for "+
			"the spec's change, and nothing runs.\n", r.update.Root)
	} else {
		fmt.Fprintln(r.err, "orchestra: the refinement ended without handing back a root epic, so there is "+
			"nothing to run.")
	}
	r.logLine("SPEC: the refinement ended without handing back a root epic")
	return r.unfinished(ctx, spec, "")
}

// refineSubject is a spec's refinement as the subject of a session in a pane: the spec, its update
// (nil but in update mode), and bd, which counts what the session has filed for it every so often
// (every).
type refineSubject struct {
	spec    project.Spec
	update  *specUpdate
	tracker specLister
	every   time.Duration
}

func (s refineSubject) message(string) (string, error) { return refineMessage(s.spec, s.update), nil }

func (refineSubject) handedBack(repo string) (string, bool) {
	root, err := project.FiledSpec(repo)
	return root, err == nil // a file that names no epic hands nothing back: /exit ends that session
}

func (refineSubject) name() string { return "the refinement" }

func (refineSubject) inPane() string {
	return "Breaking the spec down with claude in the pane on the right, which asks you only what the spec " +
		"leaves open and closes once the tickets are filed. Type /exit there to leave without filing more; " +
		"Ctrl+C here stops."
}

func (s refineSubject) shown(line *tui.InterviewLine, began time.Time) paneShown {
	r := &refineShown{line: line, tracker: s.tracker, spec: s.spec.Path, every: s.every, began: began}
	if s.update != nil {
		r.before = s.update.Before
	}
	return r
}

// refineShown is what orchestra's pane shows of a spec's refinement in the pane beside it, under its
// fixed message: claude's state and the time so far, as an interview's line has them, and how many
// epics and tickets carry the spec's metadata, counted from bd every so often (every), but for those
// that did before it began (before); nothing when line is nil.
type refineShown struct {
	line    *tui.InterviewLine
	tracker specLister
	spec    string // the spec's path, which everything filed for it carries in its metadata
	every   time.Duration
	began   time.Time
	before  filedCount // in update mode, the tree filed already
	now     tui.InterviewProgress
	counted time.Time         // when bd was last asked
	count   <-chan filedCount // its answer, once it has given it; nil when none is awaited
}

// filedCount is how many epics and tickets bd has for a spec; ok is false when it couldn't say.
type filedCount struct {
	epics, tickets int
	ok             bool
}

// countOf counts issues, those bd has for a spec, by epics and the rest.
func countOf(issues []dispatch.Ticket) filedCount {
	n := filedCount{ok: true}
	for _, t := range issues {
		if t.IssueType == "epic" {
			n.epics++
		} else {
			n.tickets++
		}
	}
	return n
}

// show draws the refinement's progress after a reading: st is claude's state ("" when the terminal
// couldn't say, which keeps the last one shown). bd's count, once given, shows until the next.
func (s *refineShown) show(ctx context.Context, _ string, st dispatch.AgentState) {
	if s.line == nil {
		return
	}
	if st != "" {
		s.now.Agent = st
	}
	s.now.Elapsed = time.Since(s.began)
	select {
	case n := <-s.count: // never ready while count is nil
		s.count = nil
		if n.ok {
			s.now.Counted, s.now.FiledEpics, s.now.FiledTickets = true, max(n.epics-s.before.epics, 0),
				max(n.tickets-s.before.tickets, 0)
		}
	default: // not counted yet
	}
	if s.count == nil && (s.counted.IsZero() || time.Since(s.counted) >= s.every) {
		s.counted, s.count = time.Now(), s.countFiled(ctx)
	}
	s.line.Show(s.now)
}

// countFiled counts what bd has for the spec, in the background so that the wait goes on reading the
// terminal meanwhile; the channel gives the count once read.
func (s *refineShown) countFiled(ctx context.Context) <-chan filedCount {
	read := make(chan filedCount, 1) // the send doesn't block once the wait is over
	go func() {
		issues, err := s.tracker.WithMetadata(ctx, dispatch.SpecKey, s.spec)
		if err != nil {
			read <- filedCount{}
			return
		}
		read <- countOf(issues)
	}()
	return read
}

// clear removes the line, if one is drawn, before orchestra's pane goes on.
func (s *refineShown) clear() {
	if s.line != nil {
		s.line.Clear()
	}
}

// logLine writes text to the run's log, if there is one.
func (b refineBreakdown) logLine(text string) {
	if b.log != nil {
		b.log.Line(time.Now(), text)
	}
}
