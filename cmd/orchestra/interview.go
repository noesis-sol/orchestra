package main

import (
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

	"golang.org/x/term"
)

// A feature typed at the start of a run ("New feature", see work.go) is talked through rather than
// planned by the organs. orchestra hands the terminal to an interactive Claude Code session in the
// main checkout, the interview's instructions (internal/project's interview-prompt.md) appended to
// its system prompt and the description as its first message. Claude interviews the user until
// they share an understanding of the feature, files it in Beads as an epic and its tickets once the
// user agrees, and names the epic in .orchestra/run/feature.json. When the user exits the session,
// orchestra shows the epic's tickets and asks whether to run them; on yes the run is the one
// --feature starts after filing its plan. --feature itself, for scripts and agents, keeps the
// screen and plan organs (feature.go).

// noFeature is what orchestra says when the interview filed no feature to run.
const noFeature = "No feature was filed; nothing to run."

// interviewOff says why the run c can't talk a typed feature through, or returns "": the interview
// is a Claude Code session, so it needs claude, and a run whose workers are another agent may have
// no Claude Code to talk to.
func interviewOff(c options) string {
	if c.AgentKind != "claude" {
		return "the workers' agent is " + c.AgentKind
	}
	return organ.Unavailable("claude")
}

// featureInterview is a feature typed at the start, talked through and filed in a Claude Code
// session, on its way to a run.
type featureInterview struct {
	request string
	repo    string
	session func(ctx context.Context, prompt string) error // the session on the terminal, its instructions in prompt
	tracker interface {
		Show(ctx context.Context, id string) (dispatch.Ticket, error)
		Children(ctx context.Context, id string) ([]dispatch.Ticket, []dispatch.Link, error)
	}
	log      *dispatch.Log // nil in tests
	in       io.Reader
	out, err io.Writer
}

// runInterview talks the feature in c.Feature through in a Claude Code session on the terminal,
// which files it, then shows what was filed and asks whether to run it. It returns the epic's ID,
// which the run is then scoped to. When there is nothing to run it returns "" and the exit code;
// what happened has been reported.
func runInterview(
	ctx context.Context, stops *stopWatch, c options, log *dispatch.Log, stdin io.Reader, stdout, stderr io.Writer,
) (string, int) {
	// SIGTERM and SIGHUP stop the interview as they stop the feature run; Ctrl+C does too, except
	// while the session has the terminal.
	ctx, stop := stops.context(ctx)
	defer stop()
	return featureInterview{
		request: c.Feature,
		repo:    c.Repo,
		session: func(ctx context.Context, prompt string) error {
			defer stops.dropInterrupts()() // Ctrl+C is Claude Code's, which interrupts and clears with it
			defer keepTerminal(stdin)()
			// -- ends claude's options: a description that starts with "- ", a list, is none of them.
			return command.Interactive(ctx, c.Repo, stdin, stdout, stderr,
				"claude", "--append-system-prompt-file", prompt, "--", c.Feature)
		},
		tracker: beads.Tracker{Repo: c.Repo},
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
	fmt.Fprintln(f.out, "Talking the feature through with claude, which files it as tickets once you agree. "+
		"Type /exit to come back.")
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
