package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A feature, typed at the start of a run ("New feature", see work.go) or given with --feature, is
// talked through with the user by the spec's refinement session (refineBreakdown, spec_refine.go),
// in the Feature mode of the spec breakdown's instructions (internal/project's spec-prompt.md): an
// interactive Claude Code session in the main checkout, in a pane beside orchestra's or on its
// terminal, whatever agent the workers are.
// Its first message brings the description in from a file. Claude interviews the user until they
// share an understanding of the feature, files it as an epic and its tickets once the user agrees,
// and hands the epic back in .orchestra/run/feature.json. orchestra then checks the epic as it checks
// a spec's tree (checkFeature), says what was filed, and starts the run on it without asking. There
// is no unattended feature run: a request written to a file runs unattended with --spec <file>.

// featureLead opens the session's first message, before the feature run and the file that holds the
// description.
const featureLead = "Here is the feature I'd like to talk through"

// featureMessage is the session's first message, one line: featureLead, the feature run (featureRun),
// which every issue the session files carries in its metadata (project.FeatureRunKey), and the file,
// relative to the main checkout, that holds the description.
func featureMessage(run, file string) string {
	return featureLead + " (feature run " + run + "): @" + file
}

// featureRun is the feature run of a run that started at started: that time in UTC, to the second,
// which no other run in the main checkout shares while the run lock holds one at a time.
func featureRun(started time.Time) string { return started.UTC().Format("20060102T150405Z") }

// pastedFeature is the feature's description as the session's first message brings it in: between
// an opening and a closing pasted_content tag carrying the same random ID, each tag on its own line.
// The description is often pasted from an issue or a web page, and Claude Code tags only what is
// pasted into its own input box; the session's instructions say the text in the tags may carry
// instructions the user didn't write. The ID is drawn once the description is known, and drawn
// again while the description holds it, so no text in it can close the block early.
func pastedFeature(request string) string {
	id := organ.EvidenceID()
	for strings.Contains(request, id) {
		id = organ.EvidenceID()
	}
	return "<pasted_content id=\"" + id + "\">\n" + strings.Trim(request, "\n") + "\n</pasted_content id=\"" + id + "\">"
}

// noFeature is what orchestra says when the session handed no feature back to run.
const noFeature = "No feature was filed; nothing to run."

// errFeatureNeedsTerminal is a feature's session's error where it would take orchestra's terminal
// and there is none to take: the feature is the user's to talk through.
var errFeatureNeedsTerminal = errors.New("talking the feature through needs you at a terminal, and orchestra's " +
	"input or output isn't one: orchestra --spec <file> breaks a request written to a file down unattended")

// featureEpicTracker is bd as a feature run reads the epic its session handed back: the epic, the
// tickets under it, and the blocks cycles; and, when it handed none back, what it filed under the
// feature run.
type featureEpicTracker interface {
	epicTracker
	specLister
	Cycles(ctx context.Context) ([][]string, error)
}

// featureTalk is a feature run's step before its loop: talked through and filed in its session,
// then checked.
type featureTalk struct {
	request   string // the feature's description, as typed or given with --feature
	runID     string // the feature run (featureRun), which every issue the session files carries
	repo      string // the main checkout
	breakdown refineBreakdown
	tracker   featureEpicTracker
	workers   int           // the run's concurrency, which checkFeature checks the shape of the tree filed against
	log       *dispatch.Log // nil in tests
	out, err  io.Writer
}

// talkFeature talks the feature of the run s, typed at its start or given with --feature, through
// with the user, in a session that files it, checks the epic it hands back, and returns its ID, which
// the run is then scoped to. When there is nothing to run it returns "" and the exit code; what
// happened has been reported.
func (s *session) talkFeature(ctx context.Context) (string, int) {
	// SIGTERM and SIGHUP stop the session as they stop the run; Ctrl+C does too, except while the
	// session has the terminal.
	ctx, stop := s.stops.context(ctx)
	defer stop()
	c := s.cfg
	return featureTalk{
		request: c.work().feature, runID: featureRun(s.started), repo: c.Repo, breakdown: newRefineBreakdown(s),
		tracker: beads.Tracker{Repo: c.Repo}, workers: c.Concurrency, log: s.log, out: s.e.stdout, err: s.e.stderr,
	}.run(ctx)
}

func (f featureTalk) run(ctx context.Context) (string, int) {
	if why := organ.Unavailable(ctx, "claude"); why != "" {
		fmt.Fprintf(f.err, "orchestra can't talk the feature through: it needs Claude Code, and %s.\n", why)
		f.logLine("FEATURE not talked through: " + why)
		return "", dispatch.ExitSetup
	}
	switch code := cleanCheckout(ctx, f.repo, f.err); code {
	case dispatch.ExitOK:
	case dispatch.ExitInterrupted:
		return "", f.stopped("")
	default:
		return "", code
	}
	f.logLine("FEATURE interview: " + dispatch.FeatureLine(f.request))
	epic, err := f.breakdown.talkFeature(ctx, f.request, f.runID)
	switch {
	case ctx.Err() != nil:
		if epic == "" {
			epic = f.filedEpic(ctx)
		}
		return "", f.stopped(epic)
	case errors.Is(err, errNotHandedBack):
		return "", f.notHandedBack(ctx)
	case err != nil:
		detail := tui.Printable(dispatch.FirstLine(err.Error()))
		fmt.Fprintln(f.err, "orchestra couldn't talk the feature through:", detail)
		f.logLine("FEATURE not talked through: " + detail)
		if errors.Is(err, errFeatureNeedsTerminal) {
			return "", dispatch.ExitSetup
		}
		return "", dispatch.ExitTool
	}

	filed, warnings, err := checkFeature(ctx, f.tracker, epic, f.workers)
	for _, w := range warnings {
		w = planLine(w)
		fmt.Fprintln(f.err, "warning: "+w)
		f.logLine("FEATURE warning: " + w)
	}
	switch {
	case ctx.Err() != nil:
		return "", f.stopped(epic)
	case err != nil:
		detail := tui.Printable(dispatch.FirstLine(err.Error()))
		fmt.Fprintln(f.err, "orchestra won't run what the interview handed back:", detail)
		f.logLine("FEATURE not run: " + detail)
		return "", dispatch.ExitSetup
	}
	fmt.Fprintf(f.out, "filed the feature as %s, %s%s\n", epic, planLine(filed.title), filed.counts())
	f.logLine(fmt.Sprintf("FEATURE interview filed epic %s%s: %s", epic, filed.counts(),
		dispatch.FeatureLine(f.request)))
	return epic, dispatch.ExitOK
}

// stopped reports that a stop signal ended the feature's session, or what came after it, before the
// run started, naming the epic it filed, if it got that far, and returns the exit code.
func (f featureTalk) stopped(epic string) int {
	fmt.Fprintln(f.err, "orchestra: stopped before the run started.")
	if epic != "" {
		fmt.Fprintf(f.err, "The interview filed %s; start the run on it with: orchestra --ticket %s\n", epic, epic)
	}
	return dispatch.ExitInterrupted
}

// notHandedBack ends a feature run whose session the user left without it handing the epic back
// (/exit, or closing its pane), exit 0: nothing runs. With nothing filed under the feature run there
// is nothing to run; else orchestra says what the session filed, as bd has it, and how to start the
// run on it.
func (f featureTalk) notHandedBack(ctx context.Context) int {
	issues, err := f.tracker.WithMetadata(ctx, project.FeatureRunKey, f.runID)
	switch {
	case err != nil:
		detail := tui.Printable(dispatch.FirstLine(err.Error()))
		fmt.Fprintln(f.err, "orchestra: the interview handed no feature back, and orchestra can't list what it "+
			"filed:", detail)
		f.logLine("FEATURE interview handed nothing back; can't list what it filed: " + detail)
		return dispatch.ExitOK
	case len(issues) == 0:
		fmt.Fprintln(f.out, noFeature)
		f.logLine("FEATURE interview filed nothing")
		return dispatch.ExitOK
	}
	epics := 0
	for _, t := range issues {
		if t.IssueType == "epic" {
			epics++
		}
	}
	filed := plural(epics, "epic") + " and " + plural(len(issues)-epics, "ticket")
	fmt.Fprintf(f.out, "The interview ended without handing the feature back. It filed %s:\n", filed)
	for i, t := range issues {
		if i == unfinishedShown {
			fmt.Fprintf(f.out, "  … and %d more: bd list --metadata-field %s --all\n", len(issues)-i,
				command.ShellWord(project.FeatureRunKey+"="+f.runID))
			break
		}
		fmt.Fprintf(f.out, "  %s  %s  %s\n", t.ID, t.IssueType, planLine(t.Title))
	}
	if tops := topEpics(issues); len(tops) == 1 {
		fmt.Fprintln(f.out, "Start the run on it with: orchestra --ticket", tops[0].ID)
		filed += " under " + tops[0].ID
	} else {
		fmt.Fprintln(f.out, "Start the run on one of them with: orchestra --ticket <ID>")
	}
	f.logLine("FEATURE interview handed nothing back; it filed " + filed)
	return dispatch.ExitOK
}

// filedEpic is the epic a stopped session filed under the feature run, as bd has it: the one epic
// among what it filed whose parent isn't among it, or "" when there is none or more than one. bd is
// read after the stop all the same, with its own limit.
func (f featureTalk) filedEpic(ctx context.Context) string {
	issues, err := f.tracker.WithMetadata(context.WithoutCancel(ctx), project.FeatureRunKey, f.runID)
	if tops := topEpics(issues); err == nil && len(tops) == 1 {
		return tops[0].ID
	}
	return ""
}

// logLine writes text to the run's log, if there is one.
func (f featureTalk) logLine(text string) {
	if f.log != nil {
		f.log.Line(time.Now(), text)
	}
}

// talkFeature runs the session that talks the feature request through with the user, in Feature
// mode: in a pane split off orchestra's when it runs in one, else on orchestra's terminal, its first
// message naming the feature run, run. It returns the epic the session handed back. Stopped, it
// returns the epic handed back so far, if any; ended by the user without a hand-back, or with one
// orchestra can't read (said on b.err), errNotHandedBack.
func (b refineBreakdown) talkFeature(ctx context.Context, request, run string) (string, error) {
	// One line, which brings the description in: Claude Code attaches a file named after @ to the
	// message, as the user's own message would, and Herdr types claude's command into the pane's
	// shell, which takes no argument with line breaks.
	file, err := project.WriteFeatureRequest(b.claude.repo, pastedFeature(request))
	if err != nil {
		return "", fmt.Errorf("cannot write the description for it: %w", err)
	}
	message := featureMessage(run, file)
	return b.talk(ctx, talkSubject{
		mode:    project.RefineFeature,
		message: message,
		pane:    featureSubject{first: message, tracker: b.epics},
		filed:   func(context.Context) string { return "" }, // featureTalk looks for it under the feature run
		unreadable: func(err error) error {
			fmt.Fprintln(b.err, "orchestra can't read the epic the interview handed back:", err)
			return errNotHandedBack
		},
	})
}

// featureSubject is a feature's session as the subject of a session in a pane: its first message,
// and bd, which has the title and tickets of the epic the session hands back.
type featureSubject struct {
	first   string
	tracker epicTracker
}

func (f featureSubject) message(string) (string, error) { return f.first, nil }

func (featureSubject) handedBack(repo string) (string, bool) {
	return handedBackIn(repo, project.RefineFeature)
}

func (featureSubject) name() string { return "the interview" }

func (featureSubject) inPane() string {
	return "Talking the feature through with claude in the pane on the right, which closes once the feature is " +
		"filed. Type /exit there to leave without filing; Ctrl+C here stops."
}

func (f featureSubject) shown(line *tui.InterviewLine, began time.Time) paneShown {
	return &interviewShown{line: line, tracker: f.tracker, began: began}
}
