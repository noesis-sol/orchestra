package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"

	"golang.org/x/term"
)

// A breakdown with the user (spec_refine.go: a spec's with --refine, or a feature's typed at the
// start, feature_refine.go) is an interactive Claude Code session in the main checkout, in a Herdr,
// tmux or cmux pane split off orchestra's, to its right (paneSession), or, when that can't be done, on
// orchestra's own terminal. orchestra's pane says meanwhile where the session is and how it is going,
// and closes the session's pane once claude has handed its epic back and finished its turn.

// epicTracker is bd as a feature's session reads the epic it handed back (beads.Tracker).
type epicTracker interface {
	Show(ctx context.Context, id string) (dispatch.Ticket, error)
	Children(ctx context.Context, id string) ([]dispatch.Ticket, []dispatch.Link, error)
}

// interviewPanes is the terminal as a session in a pane uses it (herdr.Terminal, tmux.Terminal,
// cmux.Terminal).
type interviewPanes interface {
	SplitPane(ctx context.Context, pane, cwd string) (string, error)
	LaunchInPane(ctx context.Context, pane, kind string, args []string) error
	PaneAgent(ctx context.Context, pane string) (name, kind string, state dispatch.AgentState, err error)
	PaneOpen(ctx context.Context, pane string) (bool, error)
	ClosePane(ctx context.Context, pane string) error
}

// paneSession is a breakdown with the user in a pane split off orchestra's, to its right: claude
// talks the spec or the feature through there, with its own screen and keys, while orchestra's pane
// stays in view and says how the session ends.
type paneSession struct {
	panes interviewPanes
	pane  string // orchestra's own (HERDR_PANE_ID, TMUX_PANE, CMUX_SURFACE_ID)
	// hooks has claude report through hooks, to project.InterviewRun: a terminal that reads its
	// state from them (tmux, cmux) needs them.
	hooks    bool
	repo     string                                         // the main checkout, where claude runs
	effort   string                                         // claude's --effort; "" for Claude Code's default
	fallback func(ctx context.Context, prompt string) error // the session on the terminal
	out, err io.Writer
	// Where the session stands, on a line under the fixed message, when out is a terminal (nil
	// otherwise).
	progress *tui.InterviewLine
	about    paneSubject // what the session is about
}

// paneSubject is what a session in a pane beside orchestra's is about, and what that changes: its
// first message, the file it hands its epic back in, orchestra's words for it, and the progress
// orchestra's pane shows. A feature talked through (featureSubject, feature_refine.go) is one; a spec
// refined and broken down (refineSubject, spec_refine.go) another.
type paneSubject interface {
	// message is the session's first message, one line: Herdr types claude's command into the pane's
	// shell, which takes no argument with line breaks. It may write a file the message brings in.
	message(repo string) (string, error)
	// handedBack is the epic the session has handed back in its file in repo; ok is false until one
	// names an epic.
	handedBack(repo string) (epic string, ok bool)
	name() string   // the session, as orchestra's messages call it ("the interview")
	inPane() string // what orchestra's pane says while the session runs in the pane beside it
	// shown is what orchestra's pane shows of the session's progress, begun at began, on line (nil for
	// none).
	shown(line *tui.InterviewLine, began time.Time) paneShown
}

// paneShown is what orchestra's pane shows of a session in a pane, under its fixed message, from
// paneSession.wait's readings.
type paneShown interface {
	// show draws the progress after a reading: epic is the epic the session has handed back ("" for
	// none), st claude's state ("" when the terminal couldn't say, which keeps the last one shown).
	show(ctx context.Context, epic string, st dispatch.AgentState)
	clear() // removes what show drew, before orchestra's pane goes on
}

// How often the session's pane is looked at, and how long claude may take to appear in it, as
// Herdr's AdoptAgent gives an agent.
const (
	interviewPoll  = time.Second
	interviewStart = time.Minute
)

// errPaneClosed is paneSession.open's error when the user closed the session's pane before claude
// appeared in it.
var errPaneClosed = errors.New("the session's pane was closed")

// talk opens the session's pane, starts claude there with the instructions in prompt, and waits
// until claude has handed its epic back and finished its turn, has left the pane (/exit), or the
// pane is gone. It then closes the pane, which ends claude if it is still there and gives
// orchestra's pane the keyboard focus back: Herdr returns it to the pane that had it before the
// split, orchestra's. When ctx
// ends first (Ctrl+C in orchestra's pane, SIGTERM, SIGHUP) the pane is closed all the same, which
// ends claude. When the pane can't be opened or claude doesn't appear in it, orchestra says why,
// closes any pane it made, and hands claude its terminal instead (fallback).
func (p paneSession) talk(ctx context.Context, prompt string) error {
	began := time.Now()
	session, err := p.open(ctx, prompt)
	switch {
	case ctx.Err() != nil, errors.Is(err, errPaneClosed): // the session is over
	case err != nil:
		p.close(ctx, session)
		fmt.Fprintln(p.err, "orchestra couldn't open "+p.about.name()+" in a pane beside its own, so claude "+
			"takes this terminal: "+dispatch.FirstLine(err.Error()))
		return p.fallback(ctx, prompt)
	default:
		fmt.Fprintln(p.out, p.about.inPane())
		p.wait(ctx, session, began)
	}
	p.close(ctx, session)
	return nil
}

// open splits orchestra's pane and starts claude in the new one, in the main checkout, and returns
// the new pane's ID ("" if none was made) once Herdr sees claude in it.
func (p paneSession) open(ctx context.Context, prompt string) (string, error) {
	message, err := p.about.message(p.repo)
	if err != nil {
		return "", err
	}
	args := append([]string{"--append-system-prompt-file", prompt}, effortArgs(p.effort)...)
	if p.hooks {
		report, err := claude.Reporter{}.ReportArgsIn(p.repo, project.InterviewRun)
		if err != nil {
			return "", fmt.Errorf("cannot set up its hooks: %w", err)
		}
		args = append(args, report...)
	}
	pane, err := p.panes.SplitPane(ctx, p.pane, p.repo)
	if err != nil {
		return "", err
	}
	if err := p.panes.LaunchInPane(ctx, pane, "claude", append(args, "--", message)); err != nil {
		return pane, err
	}
	return pane, p.started(ctx, pane)
}

// started waits for Herdr to see claude in the pane, for up to interviewStart: until then, no agent
// in it means claude hasn't started yet, not that it has left. A pane the user closed meanwhile is
// errPaneClosed.
func (p paneSession) started(ctx context.Context, pane string) error {
	deadline := time.Now().Add(interviewStart)
	for {
		_, kind, st, err := p.panes.PaneAgent(ctx, pane)
		switch {
		case err == nil && st != dispatch.StateGone && kind == "claude":
			return nil
		case err == nil && st == dispatch.StateGone:
			// No agent in a pane that is gone, too: Herdr can't tell them apart.
			if open, err := p.panes.PaneOpen(ctx, pane); err == nil && !open {
				return errPaneClosed
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("claude didn't appear in pane %s within %v; the terminal could not say what it holds: %w",
					pane, interviewStart, err)
			}
			return fmt.Errorf("claude didn't appear in pane %s within %v", pane, interviewStart)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interviewPoll):
		}
	}
}

// wait waits until the session is over, or ctx ends. It is over once claude has left the pane or
// the pane is gone, which Herdr answers alike, with no agent in it, or once claude has handed its
// epic back, in its subject's file (feature.json, spec.json), and its turn has ended (idle, or done
// until the pane is looked at). claude writes the file during its last turn, before it tells the
// user the tickets are filed, so the file is looked at before claude's state is read: a turn's end
// read after the file was there is the end of that turn or a later one. A read that fails says
// nothing about claude: Herdr is asked again. After each reading, orchestra's pane shows where the
// session stands, since began, on a line that is gone once wait returns.
func (p paneSession) wait(ctx context.Context, pane string, began time.Time) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // ends a read of bd for the progress still going
	about := p.about
	shown := about.shown(p.progress, began)
	defer shown.clear()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interviewPoll):
		}
		epic, filed := about.handedBack(p.repo)
		_, _, st, err := p.panes.PaneAgent(ctx, pane)
		switch {
		case err != nil:
			st = "" // the line keeps the state last read
		case st == dispatch.StateGone:
			return
		case filed && (st == dispatch.StateIdle || st == dispatch.StateDone):
			return
		}
		shown.show(ctx, epic, st)
	}
}

// interviewShown is what orchestra's pane shows of a feature's session in a pane, under its fixed
// message, from paneSession.wait's readings; nothing when line is nil.
type interviewShown struct {
	line    *tui.InterviewLine
	tracker epicTracker
	began   time.Time
	now     tui.InterviewProgress
	epic    <-chan tui.InterviewProgress // the filed epic's title and tickets once bd has given them
}

// show draws the session's progress after a reading: epic is the epic feature.json names ("" for
// none), st claude's state ("" when Herdr couldn't say, which keeps the last one shown). The epic,
// once named, stays: claude doesn't unfile it. bd is read for its title and tickets meanwhile, which
// the next reading shows.
func (s *interviewShown) show(ctx context.Context, epic string, st dispatch.AgentState) {
	if s.line == nil {
		return
	}
	if st != "" {
		s.now.Agent = st
	}
	s.now.Elapsed = time.Since(s.began)
	select {
	case e := <-s.epic:
		s.now.Title, s.now.Tickets = e.Title, e.Tickets
	default: // not read yet, or no epic to read
	}
	if epic != "" && epic != s.now.Epic {
		s.now.Epic, s.now.Title, s.now.Tickets = epic, "", 0
		s.epic = s.readEpic(ctx, epic)
	}
	s.line.Show(s.now)
}

// readEpic reads the epic's title and how many tickets it has from bd, in the background so that
// the wait goes on reading Herdr meanwhile; the channel gives them once read, or nothing if bd can't
// say, in which case the line names the epic alone.
func (s *interviewShown) readEpic(ctx context.Context, epic string) <-chan tui.InterviewProgress {
	read := make(chan tui.InterviewProgress, 1) // the send doesn't block once the wait is over
	go func() {
		t, err := s.tracker.Show(ctx, epic)
		if err != nil {
			return
		}
		children, _, err := s.tracker.Children(ctx, epic)
		if err != nil {
			return
		}
		read <- tui.InterviewProgress{Title: t.Title, Tickets: len(children)}
	}()
	return read
}

// clear removes the line, if one is drawn, before orchestra's pane goes on.
func (s *interviewShown) clear() {
	if s.line != nil {
		s.line.Clear()
	}
}

// close closes the session's pane, if one was made and is still open, which ends claude if it is
// still there. It runs once ctx has ended too, as on a stop.
func (p paneSession) close(ctx context.Context, pane string) {
	if pane == "" {
		return
	}
	if err := p.panes.ClosePane(context.WithoutCancel(ctx), pane); err != nil {
		fmt.Fprintf(p.err, "orchestra couldn't close %s's pane %s: %s\n", p.about.name(), pane,
			dispatch.FirstLine(err.Error()))
	}
}

// keepTerminal saves the state of the terminal in, if it is one, and returns what restores it: a
// program that ends without putting the terminal back, in raw mode say, would leave orchestra's
// output after it garbled, line ends no longer returning to the first column.
func keepTerminal(in io.Reader) (restore func()) {
	f, ok := in.(*os.File)
	if !ok {
		return func() {}
	}
	st, err := term.GetState(int(f.Fd()))
	if err != nil { // not a terminal
		return func() {}
	}
	return func() { _ = term.Restore(int(f.Fd()), st) } // best effort: the question may still be answered
}
