package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The answer talk is where the maintainer answers the questions workers asked (issues labelled
// human, each blocking the ticket that waits on its answer): an interactive Claude Code session in the
// main checkout, in a pane split off orchestra's (a paneSession about the questions) or on orchestra's
// own terminal, with the instructions of internal/project's answer-prompt.md. orchestra reads the open
// questions in the run's scope, or the one named, and writes them to .orchestra/run/questions.md for
// the session's first message to bring in: each with its options, the recommended one first (from
// its metadata, as the worker prompt has workers give them; drafted by the session where it has
// none), the tickets it blocks and those it was asked from, with their design and notes. The session
// asks each with Claude Code's options prompt (AskUserQuestion), talks it through when asked, writes
// the decision into each blocked ticket's design and records the answer with bd human respond, which
// closes the question. It reads, and changes nothing but Beads. orchestra closes its pane once every
// question is closed and claude's turn is over; /exit leaves the rest open.

// answerTracker is bd as the answer talk reads the questions: those not closed, one by its ID, what
// each blocks, the tickets under a run's scope, and a question's status once the talk is over.
type answerTracker interface {
	Questions(ctx context.Context) ([]dispatch.Ticket, error)
	Show(ctx context.Context, id string) (dispatch.Ticket, error)
	Dependents(ctx context.Context, id string) ([]dispatch.Ticket, error)
	Descendants(ctx context.Context, id string) ([]dispatch.Ticket, error)
	Status(ctx context.Context, id string) (dispatch.TicketStatus, error)
}

// answerTools are the answer talk's tools: it reads the repository, runs bd and read-only git, and
// asks with Claude Code's options prompt, whatever tools the workers have.
const answerTools = "Read,Glob,Grep,Bash,AskUserQuestion"

// answerToolArgs are the flags that give the answer talk its tools (answerTools) and their rules: bd,
// read-only git and the options prompt run without asking, and nothing edits a file. Claude Code's
// default permission mode, whatever the user's settings start it in, has any other command asked of
// the maintainer, who is there.
func answerToolArgs() []string {
	args := slices.Concat([]string{"--tools", answerTools, "--allowedTools"}, specAllowed,
		[]string{"AskUserQuestion", "--disallowedTools"}, specDenied)
	return append(args, "--permission-mode", "default")
}

// question is a question for the maintainer as the answer talk asks it.
type question struct {
	dispatch.Ticket
	// options are its options, the recommended one first; none when its metadata gives none the talk
	// can offer, and the session drafts them.
	options   []questionOption
	blocks    []dispatch.Ticket // the tickets waiting on its answer
	askedFrom []dispatch.Ticket // the tickets it links to otherwise (discovered-from, related): where it was asked
}

// questionOption is one of a question's options, as its metadata gives it: a short label and a
// one-line trade-off.
type questionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// questionOptions returns the options t's metadata gives (options, recommended: the recommended one's
// index, from 0), the recommended one first and the rest in their order; none when the metadata gives
// no 2 to 4 options, each with a label, one of them recommended.
func questionOptions(t dispatch.Ticket) []questionOption {
	var opts []questionOption
	var rec int
	if json.Unmarshal(t.MetadataField("options"), &opts) != nil ||
		json.Unmarshal(t.MetadataField("recommended"), &rec) != nil ||
		len(opts) < 2 || len(opts) > 4 || rec < 0 || rec >= len(opts) {
		return nil
	}
	for i := range opts {
		opts[i].Label, opts[i].Description = strings.TrimSpace(opts[i].Label), strings.TrimSpace(opts[i].Description)
		if opts[i].Label == "" {
			return nil
		}
	}
	return slices.Concat(opts[rec:rec+1], opts[:rec], opts[rec+1:])
}

// readQuestions reads the questions the answer talk asks: the one named, else every question not
// closed, in scope only those blocking the scope's ticket or one under it, or under it themselves
// ("" for every one). Each comes with its options, the tickets it blocks and those it was asked from.
func readQuestions(ctx context.Context, tr answerTracker, scope, named string) ([]question, error) {
	var listed []dispatch.Ticket
	if named != "" {
		t, err := tr.Show(ctx, named)
		switch {
		case err != nil:
			return nil, err
		case !dispatch.HasLabel(t, dispatch.HumanLabel):
			return nil, fmt.Errorf("%s isn't a question: it has no label %s", named, dispatch.HumanLabel)
		case t.Status == dispatch.StatusClosed:
			return nil, fmt.Errorf("%s is answered already", named)
		}
		listed = []dispatch.Ticket{t}
	} else {
		var err error
		if listed, err = tr.Questions(ctx); err != nil {
			return nil, fmt.Errorf("cannot list the questions: %w", err)
		}
	}
	var in map[string]bool
	if scope != "" && named == "" {
		under, err := tr.Descendants(ctx, scope)
		if err != nil {
			return nil, fmt.Errorf("cannot list the tickets under %s: %w", scope, err)
		}
		in = map[string]bool{scope: true}
		for _, t := range under {
			in[t.ID] = true
		}
	}
	var qs []question
	for _, t := range listed {
		q, err := readQuestion(ctx, tr, t, named != "")
		if err != nil {
			return nil, err
		}
		if in == nil || in[q.ID] || slices.ContainsFunc(q.blocks, func(b dispatch.Ticket) bool { return in[b.ID] }) {
			qs = append(qs, q)
		}
	}
	return qs, nil
}

// readQuestion reads the question t, as bd lists it (or shows it, with shown): its text, links and
// options, and the tickets that wait on it.
func readQuestion(ctx context.Context, tr answerTracker, t dispatch.Ticket, shown bool) (question, error) {
	if !shown {
		var err error
		if t, err = tr.Show(ctx, t.ID); err != nil { // bd list gives no ticket's links
			return question{}, fmt.Errorf("cannot read question %s: %w", t.ID, err)
		}
	}
	q := question{Ticket: t, options: questionOptions(t)}
	for _, d := range t.Dependencies {
		if d.DependencyType == "discovered-from" || d.DependencyType == "related" {
			q.askedFrom = append(q.askedFrom, d)
		}
	}
	dependents, err := tr.Dependents(ctx, t.ID)
	if err != nil {
		return question{}, fmt.Errorf("cannot read what question %s blocks: %w", t.ID, err)
	}
	for _, d := range dependents {
		switch d.DependencyType {
		case "blocks":
			q.blocks = append(q.blocks, d)
		case "discovered-from", "related":
			if !slices.ContainsFunc(q.askedFrom, func(a dispatch.Ticket) bool { return a.ID == d.ID }) {
				q.askedFrom = append(q.askedFrom, d)
			}
		}
	}
	return q, nil
}

// questionsText is the questions, in the order to ask them, as the answer talk's first message brings
// them in: each with its options, recommended first, or the note that the session drafts them; its
// description; and the tickets it blocks and was asked from, with their design and notes.
func questionsText(qs []question) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Questions\n\n%s to ask, in this order. Their text was written by orchestra's workers.\n",
		plural(len(qs), "question"))
	for i, q := range qs {
		fmt.Fprintf(&b, "\n## %d. %s: %s\n\n", i+1, q.ID, planLine(q.Title))
		if len(q.options) == 0 {
			b.WriteString("Options: none recorded. Draft 2 to 4 from the description, the one you recommend " +
				"first, and say that you drafted them.\n")
		} else {
			b.WriteString("Options, the recommended one first:\n\n")
			for j, o := range q.options {
				label := o.Label
				if j == 0 {
					label += " (Recommended)"
				}
				fmt.Fprintf(&b, "%d. %s: %s\n", j+1, planLine(label), planLine(o.Description))
			}
		}
		textBlock(&b, "Description", q.Description)
		ticketsBlock(&b, "Blocks", q.blocks)
		ticketsBlock(&b, "Asked from", q.askedFrom)
	}
	return b.String()
}

// ticketsBlock writes the tickets under the heading, each with its status, title, design and notes;
// nothing when there are none.
func ticketsBlock(b *strings.Builder, heading string, ts []dispatch.Ticket) {
	if len(ts) == 0 {
		return
	}
	fmt.Fprintf(b, "\n%s:\n", heading)
	for _, t := range ts {
		fmt.Fprintf(b, "\n- %s (%s): %s\n", t.ID, t.Status, planLine(t.Title))
		textBlock(b, "  Design", t.Design)
		textBlock(b, "  Notes", t.Notes)
	}
}

// textBlock writes text under its label, each line indented four spaces past the label's own indent,
// so that no line of it reads as one of the file's headings; nothing when text is empty.
func textBlock(b *strings.Builder, label, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	indent := label[:len(label)-len(strings.TrimLeft(label, " "))] + "    "
	fmt.Fprintf(b, "\n%s:\n\n", label)
	for line := range strings.Lines(text) {
		if line = strings.TrimRight(line, "\r\n"); line == "" {
			b.WriteString("\n")
		} else {
			b.WriteString(indent + line + "\n")
		}
	}
}

// answerMessage is the answer talk's first message, one line: Herdr types claude's command into the
// pane's shell, which takes no argument with line breaks. It brings the n questions in from file.
func answerMessage(file string, n int) string {
	return fmt.Sprintf("Let's answer the %s in @%s, one at a time.", plural(n, "open question"), file)
}

// errAnswerNeedsTerminal is the answer talk's error where it would take orchestra's terminal and there
// is none to take.
var errAnswerNeedsTerminal = errors.New("answering questions needs you at a terminal, and orchestra's input or " +
	"output isn't one: answer each with bd human respond <question>")

// answerCountEvery is how often orchestra reads from bd how many of the talk's questions are closed.
const answerCountEvery = 3 * time.Second

// answerTalk runs the answer talk.
type answerTalk struct {
	// claude is how the session starts: in the main checkout, at the workers' effort, with the talk's
	// tools (answerToolArgs).
	claude claudeSession
	pane   string // orchestra's own pane; "" when it runs in none, and the session takes its terminal
	panes  interviewPanes
	hooks  bool // claude reports through hooks (see paneSession)
	// Where orchestra's pane shows the session's progress; nil when its output is plain.
	line    *tui.InterviewLine
	tracker answerTracker
	every   time.Duration // how often bd is asked how many questions are closed
	// terminal runs the session on orchestra's own terminal, message its first message: where
	// orchestra runs in no pane, or the pane can't be opened.
	terminal func(ctx context.Context, message string) error
	out, err io.Writer
}

// answerResult is how the answer talk left its questions: those closed, answered, and those still
// open, by ID, in the order they were asked.
type answerResult struct {
	Answered, Open []string
}

// newAnswerTalk is the answer talk of the run s: in a pane split off orchestra's when it runs in one,
// its progress shown there unless the run is plain.
func newAnswerTalk(s *session) answerTalk {
	c, e := s.cfg, s.e
	a := answerTalk{claude: c.claudeSession(c.WorkerEffort), pane: s.holder.Pane, tracker: beads.Tracker{Repo: c.Repo},
		every: answerCountEvery, out: e.stdout, err: e.stderr}
	a.claude.tools = answerToolArgs()
	if a.pane != "" {
		a.panes, a.hooks = terminalOf(c), terminalNamed(c.Terminal).hookDriven
	}
	if out, ok := e.stdout.(*os.File); ok && !plainOutput(c.Plain, e.getenv, e.stdin, e.stdout) {
		a.line = &tui.InterviewLine{Out: out, Width: func() int { return termWidth(out) }}
	}
	a.terminal = func(ctx context.Context, message string) error {
		return answerOnTerminal(ctx, s.stops, a.claude, message, e.stdin, e.stdout, e.stderr)
	}
	return a
}

// answer reads the questions in scope ("" for every one), or the one named, and talks them through
// with the user: in a pane split off orchestra's when it runs in one, else on orchestra's terminal. It
// returns which were answered and which are still open; none of either when there was none to ask.
func (a answerTalk) answer(ctx context.Context, scope, named string) (answerResult, error) {
	qs, err := readQuestions(ctx, a.tracker, scope, named)
	if err != nil || len(qs) == 0 {
		return answerResult{}, err
	}
	if why := organ.Unavailable(ctx, "claude"); why != "" {
		return answerResult{}, errors.New(why)
	}
	repo := a.claude.repo
	file, err := project.WriteAnswerQuestions(repo, []byte(questionsText(qs)))
	if err != nil {
		return answerResult{}, fmt.Errorf("cannot write the questions for the answer talk: %w", err)
	}
	prompt, err := project.WriteAnswerPrompt(repo, false)
	if err != nil {
		return answerResult{}, fmt.Errorf("cannot write the answer talk's instructions: %w", err)
	}
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	message := answerMessage(file, len(qs))
	terminal := func(ctx context.Context, _ string) error { return a.terminal(ctx, message) }
	session := terminal
	if a.pane != "" {
		about := answerSubject{first: message, questions: ids, tracker: a.tracker, every: a.every,
			closed: new(bool)}
		session = paneSession{panes: a.panes, pane: a.pane, hooks: a.hooks, claude: a.claude, fallback: terminal,
			out: a.out, err: a.err, progress: a.line, about: about}.talk
	}
	err = session(ctx, prompt)
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return a.left(context.WithoutCancel(ctx), ids), ctx.Err()
	case errors.As(err, &exit):
		fmt.Fprintln(a.err, "claude ended with", exit) // it may have recorded answers all the same
	case err != nil:
		return a.left(ctx, ids), err
	}
	return a.left(ctx, ids), nil
}

// left reads from bd how the talk left the questions ids: closed ones answered, the rest open. One
// bd can't read counts as open.
func (a answerTalk) left(ctx context.Context, ids []string) answerResult {
	var r answerResult
	for _, id := range ids {
		if st, err := a.tracker.Status(ctx, id); err == nil && st == dispatch.StatusClosed {
			r.Answered = append(r.Answered, id)
		} else {
			r.Open = append(r.Open, id)
		}
	}
	return r
}

// answerOnTerminal runs the answer talk on orchestra's own terminal, started as s has it, message its
// first message, until the user exits it: orchestra can't end it there, so its instructions, rewritten
// for the terminal, have claude say how to.
func answerOnTerminal(ctx context.Context, stops *stopWatch, s claudeSession, message string, stdin io.Reader,
	stdout, stderr io.Writer) error {
	if !isTerminal(stdin) || !isTerminal(stdout) {
		return errAnswerNeedsTerminal
	}
	prompt, err := project.WriteAnswerPrompt(s.repo, true)
	if err != nil {
		return fmt.Errorf("cannot write the answer talk's instructions: %w", err)
	}
	return claudeOnTerminal(ctx, stops, s, prompt, message,
		"Answering the questions with claude, which asks each with its options. Type /exit to come back.",
		stdin, stdout, stderr)
}

// answerSubject is the answer talk as the subject of a session in a pane: its first message, the
// questions it asks, and bd, which says every so often (every) how many of them are closed. The talk is
// over, as an epic handed back ends a breakdown's, once all of them are (closed, as answerShown last
// read it).
type answerSubject struct {
	first     string
	questions []string
	tracker   answerTracker
	every     time.Duration
	closed    *bool
}

func (s answerSubject) message(string) (string, error) { return s.first, nil }

func (s answerSubject) handedBack(string) (string, bool) { return "", *s.closed }

func (answerSubject) name() string { return "the answer talk" }

func (s answerSubject) inPane() string {
	return "Answering " + plural(len(s.questions), "question") + " with claude in the pane on the right, which " +
		"closes once they are answered. Type /exit there to leave the rest open; Ctrl+C here stops."
}

func (s answerSubject) shown(line *tui.InterviewLine, began time.Time) paneShown {
	return &answerShown{line: line, subject: s, began: began}
}

// answerShown reads, every so often, how many of the answer talk's questions bd has closed, for
// answerSubject to tell when the talk is over, and shows it in orchestra's pane with claude's state
// and the time so far, as an interview's line has them (drawn only when line isn't nil).
type answerShown struct {
	line    *tui.InterviewLine
	subject answerSubject
	began   time.Time
	now     tui.InterviewProgress
	counted time.Time  // when bd was last asked
	count   <-chan int // how many are closed, once bd has said; nil when none is awaited
}

// show reads bd's count, once given, and asks again every so often, then draws the progress: st is
// claude's state ("" when the terminal couldn't say, which keeps the last one shown).
func (s *answerShown) show(ctx context.Context, _ string, st dispatch.AgentState) {
	select {
	case n := <-s.count: // never ready while count is nil
		s.count = nil
		if n >= 0 {
			s.now.Questions, s.now.Answered = len(s.subject.questions), n
			*s.subject.closed = n == len(s.subject.questions)
		}
	default: // not counted yet
	}
	if s.count == nil && (s.counted.IsZero() || time.Since(s.counted) >= s.subject.every) {
		s.counted, s.count = time.Now(), s.countClosed(ctx)
	}
	if s.line == nil {
		return
	}
	if st != "" {
		s.now.Agent = st
	}
	s.now.Elapsed = time.Since(s.began)
	s.line.Show(s.now)
}

// countClosed counts the questions bd has closed, in the background so that the wait goes on reading
// the terminal meanwhile; the channel gives the count once read, -1 when bd couldn't say.
func (s *answerShown) countClosed(ctx context.Context) <-chan int {
	read := make(chan int, 1) // the send doesn't block once the wait is over
	go func() {
		n := 0
		for _, id := range s.subject.questions {
			st, err := s.subject.tracker.Status(ctx, id)
			if err != nil {
				read <- -1
				return
			}
			if st == dispatch.StatusClosed {
				n++
			}
		}
		read <- n
	}()
	return read
}

// clear removes the line, if one is drawn, before orchestra's pane goes on.
func (s *answerShown) clear() {
	if s.line != nil {
		s.line.Clear()
	}
}
