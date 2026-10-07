package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"

	"golang.org/x/term"
)

// A feature typed at the start of a run ("New feature", see work.go) is talked through rather than
// planned by the organs. orchestra starts an interactive Claude Code session in the main checkout,
// the interview's instructions (internal/project's interview-prompt.md) appended to its system
// prompt and the description as its first message: in a Herdr pane split off orchestra's, to its
// right (paneSession), or, when that can't be done, on orchestra's own terminal. Claude interviews
// the user until they share an understanding of the feature, files it in Beads as an epic and its
// tickets once the user agrees, and names the epic in .orchestra/run/feature.json. When the session
// ends (in a pane, orchestra ends it once claude has filed the feature and finished its turn; on the
// terminal, the user exits it), orchestra shows the epic's tickets and asks whether to run them; on
// yes the run is the one --feature starts after filing its plan. --feature itself, for scripts and
// agents, keeps the screen and plan organs (feature.go).

// featureLead opens the interview's first message, before the description or the file that holds it.
const featureLead = "Here is the feature I'd like to talk through:"

// pastedFeature is the feature's description as the interview's first message gives it: between an
// opening and a closing pasted_content tag carrying the same random ID, each tag on its own line.
// The description is often pasted from an issue or a web page, and Claude Code tags only what is
// pasted into its own input box; the interview's instructions say the text in the tags may carry
// instructions the user didn't write. The ID is drawn once the description is known, and drawn
// again while the description holds it, so no text in it can close the block early.
func pastedFeature(request string) string {
	id := organ.EvidenceID()
	for strings.Contains(request, id) {
		id = organ.EvidenceID()
	}
	return "<pasted_content id=\"" + id + "\">\n" + strings.Trim(request, "\n") + "\n</pasted_content id=\"" + id + "\">"
}

// noFeature is what orchestra says when the interview filed no feature to run.
const noFeature = "No feature was filed; nothing to run."

// interviewOff says why the run c can't talk a typed feature through, or returns "": the interview
// is a Claude Code session, so it needs claude, and a run whose workers are another agent may have
// no Claude Code to talk to.
func interviewOff(c options) string {
	if !c.ClaudeWorkers() {
		return "the workers' agent is " + c.AgentKind
	}
	return organ.Unavailable("claude")
}

// featureInterview is a feature typed at the start, talked through and filed in a Claude Code
// session, on its way to a run.
type featureInterview struct {
	request  string
	repo     string
	session  func(ctx context.Context, prompt string) error // the session, its instructions in prompt
	tracker  epicTracker
	log      *dispatch.Log // nil in tests
	in       io.Reader
	out, err io.Writer
}

// epicTracker is bd as the interview reads the epic it filed (beads.Tracker).
type epicTracker interface {
	Show(ctx context.Context, id string) (dispatch.Ticket, error)
	Children(ctx context.Context, id string) ([]dispatch.Ticket, []dispatch.Link, error)
}

// runInterview talks the feature in c.Feature through in a Claude Code session, which files it,
// then shows what was filed and asks whether to run it. The session is in a pane split off pane,
// orchestra's own Herdr pane, or on the terminal when pane is "" or the split fails. It returns the
// epic's ID, which the run is then scoped to. When there is nothing to run it returns "" and the
// exit code; what happened has been reported.
func runInterview(
	ctx context.Context, stops *stopWatch, c options, pane string, log *dispatch.Log,
	stdin io.Reader, stdout, stderr io.Writer,
) (string, int) {
	// SIGTERM and SIGHUP stop the interview as they stop the feature run; Ctrl+C does too, except
	// while the session has the terminal.
	ctx, stop := stops.context(ctx)
	defer stop()
	terminal := func(ctx context.Context, _ string) error {
		// The instructions the session was given, rewritten for the terminal: orchestra can't end the
		// session there once the feature is filed, as it does in a pane, so claude says how to.
		prompt, err := project.WriteTerminalInterview(c.Repo)
		if err != nil {
			return fmt.Errorf("cannot write the interview's instructions: %w", err)
		}
		fmt.Fprintln(stdout, "Talking the feature through with claude, which files it as tickets once you agree. "+
			"Type /exit to come back.")
		defer stops.dropInterrupts()() // Ctrl+C is Claude Code's, which interrupts and clears with it
		defer keepTerminal(stdin)()
		// -- ends claude's options: the message after it is none of them.
		return command.Interactive(ctx, c.Repo, stdin, stdout, stderr,
			"claude", "--append-system-prompt-file", prompt, "--", featureLead+"\n\n"+pastedFeature(c.Feature))
	}
	tracker := beads.Tracker{Repo: c.Repo}
	session := terminal
	if pane != "" {
		s := paneSession{herdr: herdr.Terminal{}, pane: pane, repo: c.Repo, request: c.Feature, fallback: terminal,
			out: stdout, err: stderr}
		if out, ok := stdout.(*os.File); ok && isTerminal(out) {
			s.progress = &tui.InterviewLine{Out: out, Width: func() int { return termWidth(out) }}
			s.tracker = tracker
		}
		session = s.talk
	}
	return featureInterview{
		request: c.Feature,
		repo:    c.Repo,
		session: session,
		tracker: tracker,
		log:     log,
		in:      stdin,
		out:     stdout,
		err:     stderr,
	}.run(ctx)
}

func (f featureInterview) run(ctx context.Context) (string, int) {
	switch code := cleanCheckout(ctx, f.repo, f.err); code {
	case dispatch.ExitOK:
	case dispatch.ExitInterrupted:
		return "", f.stopped("")
	default:
		return "", code
	}
	prompt, err := project.WriteInterview(f.repo)
	if err != nil {
		fmt.Fprintln(f.err, "orchestra cannot write the interview's instructions:", err)
		return "", dispatch.ExitSetup
	}
	f.logLine("FEATURE interview: " + dispatch.FeatureLine(f.request))
	err = f.session(ctx, prompt)
	epic, fileErr := project.FiledFeature(f.repo)
	if ctx.Err() != nil {
		return "", f.stopped(epic)
	}
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		// The session may have filed the feature all the same.
		fmt.Fprintln(f.err, "claude ended with", exit)
	case err != nil:
		fmt.Fprintln(f.err, "orchestra couldn't start claude:", err)
		return "", dispatch.ExitTool
	}
	switch {
	case errors.Is(fileErr, project.ErrNoFeature):
		fmt.Fprintln(f.out, noFeature)
		f.logLine("FEATURE interview filed nothing")
		return "", dispatch.ExitOK
	case fileErr != nil:
		fmt.Fprintln(f.err, "orchestra can't read the feature the interview filed:", fileErr)
		fmt.Fprintln(f.out, noFeature)
		f.logLine("FEATURE interview filed nothing: " + dispatch.FirstLine(fileErr.Error()))
		return "", dispatch.ExitOK
	}

	t, err := f.tracker.Show(ctx, epic)
	if err != nil {
		if ctx.Err() != nil {
			return "", f.stopped(epic)
		}
		fmt.Fprintf(f.err, "orchestra can't find %s, the epic the interview named: %s\n", epic,
			dispatch.FirstLine(err.Error()))
		fmt.Fprintln(f.out, noFeature)
		f.logLine("FEATURE interview named an unknown epic: " + epic)
		return "", dispatch.ExitOK
	}
	children, links, err := f.tracker.Children(ctx, epic)
	if err != nil {
		if ctx.Err() != nil {
			return "", f.stopped(epic)
		}
		fmt.Fprintf(f.err, "orchestra couldn't read %s's tickets: %v\nStart the run on it with: orchestra --ticket %s\n",
			epic, err, epic)
		return "", dispatch.ExitTool
	}
	showPlan(f.out, filedPlan(t, children, links))
	ok, err := confirm(ctx, f.in, f.out, fmt.Sprintf("Start the run on %s (%s)?", epic, plural(len(children), "ticket")))
	switch {
	case err != nil:
		return "", f.stopped(epic)
	case !ok:
		fmt.Fprintln(f.out, "Start it later with: orchestra --ticket "+epic)
		f.logLine("FEATURE interview filed epic " + epic + ", not run")
		return "", dispatch.ExitOK
	}
	f.logLine(fmt.Sprintf("FEATURE interview filed epic %s with %s: %s",
		epic, plural(len(children), "ticket"), dispatch.FeatureLine(f.request)))
	return epic, dispatch.ExitOK
}

// stopped reports that a stop signal ended the interview before the run started, naming the epic
// it filed, if it got that far, and returns the exit code.
func (f featureInterview) stopped(epic string) int {
	fmt.Fprintln(f.err, "orchestra: stopped before the run started.")
	if epic != "" {
		fmt.Fprintf(f.err, "The interview filed %s; start the run on it with: orchestra --ticket %s\n", epic, epic)
	}
	return dispatch.ExitInterrupted
}

func (f featureInterview) logLine(text string) {
	if f.log != nil {
		f.log.Line(time.Now(), text)
	}
}

// interviewPanes is Herdr as the interview in a pane uses it (herdr.Terminal).
type interviewPanes interface {
	SplitPane(ctx context.Context, pane, cwd string) (string, error)
	LaunchInPane(ctx context.Context, pane, kind string, args []string) error
	PaneAgent(ctx context.Context, pane string) (name, kind string, state dispatch.AgentState, err error)
	PaneOpen(ctx context.Context, pane string) (bool, error)
	ClosePane(ctx context.Context, pane string) error
}

// paneSession is a featureInterview's session in a Herdr pane split off orchestra's, to its right:
// claude talks the feature through there, with its own screen and keys, while orchestra's pane stays
// in view and says how the interview ends.
type paneSession struct {
	herdr    interviewPanes
	pane     string // orchestra's own (HERDR_PANE_ID)
	repo     string // the main checkout, where claude runs
	request  string
	fallback func(ctx context.Context, prompt string) error // the session on the terminal
	out, err io.Writer
	// Where the interview stands, on a line under the fixed message, when out is a terminal (nil
	// otherwise), and bd, which has the title and tickets of the epic it filed.
	progress *tui.InterviewLine
	tracker  epicTracker
}

// How often the interview's pane is looked at, and how long claude may take to appear in it, as
// Herdr's AdoptAgent gives an agent.
const (
	interviewPoll  = time.Second
	interviewStart = time.Minute
)

// errPaneClosed is paneSession.open's error when the user closed the interview's pane before claude
// appeared in it.
var errPaneClosed = errors.New("the interview's pane was closed")

// talk opens the interview's pane, starts claude there with the instructions in prompt, and waits
// until claude has filed the feature and finished its turn, has left the pane (/exit), or the pane
// is gone. It then closes the pane, which ends claude if it is still there and gives orchestra's
// pane the keyboard focus back: Herdr returns it to the pane that had it before the split,
// orchestra's, where the description was typed. When ctx ends first (Ctrl+C in orchestra's
// pane, SIGTERM, SIGHUP) the pane is closed all the same, which ends claude. When the pane can't be
// opened or claude doesn't appear in it, orchestra says why, closes any pane it made, and hands
// claude its terminal instead (fallback).
func (p paneSession) talk(ctx context.Context, prompt string) error {
	began := time.Now()
	interview, err := p.open(ctx, prompt)
	switch {
	case ctx.Err() != nil, errors.Is(err, errPaneClosed): // the interview is over
	case err != nil:
		p.close(ctx, interview)
		fmt.Fprintln(p.err, "orchestra couldn't open the interview in a pane beside its own, so claude takes this "+
			"terminal: "+dispatch.FirstLine(err.Error()))
		return p.fallback(ctx, prompt)
	default:
		fmt.Fprintln(p.out, "Talking the feature through with claude in the pane on the right, which closes once "+
			"the feature is filed. Type /exit there to leave without filing; Ctrl+C here stops.")
		p.wait(ctx, interview, began)
	}
	p.close(ctx, interview)
	return nil
}

// open splits orchestra's pane and starts claude in the new one, in the main checkout, and returns
// the new pane's ID ("" if none was made) once Herdr sees claude in it.
func (p paneSession) open(ctx context.Context, prompt string) (string, error) {
	// Herdr types claude's command into the pane's shell, which takes no argument with line breaks;
	// the description can have several. Its first message names the file instead: Claude Code
	// attaches a file named after @ to the message, as the user's own message would. The file holds
	// the description in its pasted_content tags.
	request, err := project.WriteFeatureRequest(p.repo, pastedFeature(p.request))
	if err != nil {
		return "", fmt.Errorf("cannot write the description for it: %w", err)
	}
	pane, err := p.herdr.SplitPane(ctx, p.pane, p.repo)
	if err != nil {
		return "", err
	}
	if err := p.herdr.LaunchInPane(ctx, pane, "claude", []string{"--append-system-prompt-file", prompt, "--",
		featureLead + " @" + request}); err != nil {
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
		_, kind, st, err := p.herdr.PaneAgent(ctx, pane)
		switch {
		case err == nil && st != dispatch.StateGone && kind == "claude":
			return nil
		case err == nil && st == dispatch.StateGone:
			// No agent in a pane that is gone, too: Herdr can't tell them apart.
			if open, err := p.herdr.PaneOpen(ctx, pane); err == nil && !open {
				return errPaneClosed
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("claude didn't appear in pane %s within %v; Herdr could not say what it holds: %w",
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

// wait waits until the interview is over, or ctx ends. It is over once claude has left the pane or
// the pane is gone, which Herdr answers alike, with no agent in it, or once claude has filed the
// feature, its feature.json naming the epic, and its turn has ended (idle, or done until the pane
// is looked at). claude writes the file during its last turn, before it tells the user the feature
// is filed, so the file is looked at before claude's state is read: a turn's end read after the
// file was there is the end of that turn or a later one. A read that fails says nothing about
// claude: Herdr is asked again. After each reading, orchestra's pane shows where the interview
// stands, since began, on a line that is gone once wait returns.
func (p paneSession) wait(ctx context.Context, pane string, began time.Time) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // ends a read of the filed epic still going
	shown := &interviewShown{line: p.progress, tracker: p.tracker, began: began}
	defer shown.clear()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(interviewPoll):
		}
		epic, err := project.FiledFeature(p.repo)
		filed := err == nil // a file that names no epic files nothing: /exit ends that interview
		_, _, st, err := p.herdr.PaneAgent(ctx, pane)
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

// interviewShown is what orchestra's pane shows of an interview in a pane, under its fixed message,
// from paneSession.wait's readings; nothing when line is nil.
type interviewShown struct {
	line    *tui.InterviewLine
	tracker epicTracker
	began   time.Time
	now     tui.InterviewProgress
	epic    <-chan tui.InterviewProgress // the filed epic's title and tickets once bd has given them
}

// show draws the interview's progress after a reading: epic is the epic feature.json names ("" for
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

// clear removes the line, if one is drawn, before the plan and the question are shown.
func (s *interviewShown) clear() {
	if s.line != nil {
		s.line.Clear()
	}
}

// close closes the interview's pane, if one was made and is still open, which ends claude if it is
// still there. It runs once ctx has ended too, as on a stop.
func (p paneSession) close(ctx context.Context, pane string) {
	if pane == "" {
		return
	}
	if err := p.herdr.ClosePane(context.WithoutCancel(ctx), pane); err != nil {
		fmt.Fprintf(p.err, "orchestra couldn't close the interview's pane %s: %s\n", pane,
			dispatch.FirstLine(err.Error()))
	}
}

// filedPlan is the epic and the tickets under it as bd has them, in the shape showPlan shows a
// plan in: each ticket by its ID, after the tickets that block it.
func filedPlan(epic dispatch.Ticket, children []dispatch.Ticket, links []dispatch.Link) organ.FeaturePlan {
	p := organ.FeaturePlan{Epic: organ.PlannedEpic{Title: epic.ID + " " + epic.Title, Description: epic.Description}}
	for _, c := range children {
		t := organ.PlannedTicket{Key: c.ID, Title: c.Title, Type: c.IssueType, Priority: dispatch.PriorityOf(c),
			Files: dispatch.TicketFiles(c)}
		for _, l := range links {
			if l.Blocked == c.ID {
				t.BlockedBy = append(t.BlockedBy, l.Blocker)
			}
		}
		p.Tickets = append(p.Tickets, t)
	}
	return p
}

// keepTerminal saves the state of the terminal in, if it is one, and returns what restores it: a
// program that ends without putting the terminal back, in raw mode say, would leave orchestra's
// question after it unanswerable, Enter no longer ending a line.
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
