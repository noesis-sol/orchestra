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
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentkind"
	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/herdr"
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

// answerPanes is the terminal as the answer talk uses it: a session's panes, opened aside and focused
// on the dashboard's a key, and the claude in one named and prompted with the questions queued into it.
type answerPanes interface {
	interviewPanes
	SplitPaneAside(ctx context.Context, pane, cwd string) (string, error)
	FocusPane(ctx context.Context, pane string) error
	AdoptAgent(ctx context.Context, pane, kind, name string) (dispatch.AgentState, error)
	Prompt(ctx context.Context, name, prompt string) error
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
// closed (labelled HumanLabel), in scope only those blocking the scope's ticket or one under it, or
// under it themselves ("" for every one). Each comes with its options, the tickets it blocks and those
// it was asked from.
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
		if !dispatch.HasLabel(t, dispatch.HumanLabel) {
			continue // not a question, whatever bd listed
		}
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

// moreMessage is the message that brings the n questions queued into the open talk in from file, one
// line, as answerMessage is.
func moreMessage(file string, n int) string {
	come := fmt.Sprintf("%d more open questions have", n)
	if n == 1 {
		come = "1 more open question has"
	}
	return fmt.Sprintf("%s come in: let's answer %s in @%s too, the same way.", come, itOrThem(n), file)
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
	panes  answerPanes
	// aside opens the session's pane without the keyboard focus, as the dashboard opens it by itself.
	aside bool
	// live is the talk as the dashboard reaches it while it is open, for its a key and the questions
	// queued into it; nil where nothing does.
	live *answerLive
	// deferred is what it says of the deferred tickets, which aren't questions: how many, and where
	// (see deferredText); "" for nothing, as only a run with nothing ready but questions gives it.
	deferred string
	// skip leaves out of the talk the questions it reports true for, those that come to wait while it is
	// open too: the questions put to the maintainer in a talk now over, for a talk the run opens by
	// itself (see answerTalks.start); nil for none.
	skip  func(id string) bool
	hooks bool // claude reports through hooks (see paneSession)
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
	a := answerTalkIn(c, s.holder.Pane)
	a.out, a.err = e.stdout, e.stderr
	if out, ok := e.stdout.(*os.File); ok && !plainOutput(c.Plain, e.getenv, e.stdin, e.stdout) {
		a.line = &tui.InterviewLine{Out: out, Width: func() int { return termWidth(out) }}
	}
	a.terminal = func(ctx context.Context, message string) error {
		return answerOnTerminal(ctx, s.stops, a.claude, message, e.stdin, e.stdout, e.stderr)
	}
	return a
}

// answerTalkIn is the answer talk of the project c is about, on bd in its main checkout: in a pane
// split off pane, orchestra's own, or on orchestra's terminal where pane is "". Its caller says where
// it writes (out, err), how it shows its progress (line) and how it takes the terminal (terminal).
func answerTalkIn(c options, pane string) answerTalk {
	a := answerTalk{claude: c.claudeSession(c.WorkerEffort), pane: pane, tracker: beads.Tracker{Repo: c.Repo},
		every: answerCountEvery}
	a.claude.tools = answerToolArgs()
	if pane != "" {
		a.panes, a.hooks = terminalOf(c), terminalNamed(c.Terminal).hookDriven
	}
	return a
}

// answer reads the questions in scope ("" for every one), or the one named, and talks them through
// with the user: in a pane split off orchestra's when it runs in one, else on orchestra's terminal. It
// returns which were answered and which are still open; none of either when there was none to ask.
func (a answerTalk) answer(ctx context.Context, scope, named string) (answerResult, error) {
	qs, err := a.read(ctx, scope, named)
	if err != nil || len(qs) == 0 {
		return answerResult{}, err
	}
	return a.talkThrough(ctx, scope, qs)
}

// read reads the questions the talk asks, in scope ("" for every one), or the one named, less those it
// skips.
func (a answerTalk) read(ctx context.Context, scope, named string) ([]question, error) {
	qs, err := readQuestions(ctx, a.tracker, scope, named)
	if a.skip != nil {
		qs = slices.DeleteFunc(qs, func(q question) bool { return a.skip(q.ID) })
	}
	return qs, err
}

// talkThrough talks the questions qs, read in scope, through with the user, as answer does.
func (a answerTalk) talkThrough(ctx context.Context, scope string, qs []question) (answerResult, error) {
	if why := organ.Unavailable(ctx, "claude"); why != "" {
		return answerResult{}, errors.New(why)
	}
	repo := a.claude.repo
	file, err := project.WriteAnswerQuestions(repo, []byte(questionsText(qs)+deferredText(a.deferred)))
	if err != nil {
		return answerResult{}, fmt.Errorf("cannot write the questions for the answer talk: %w", err)
	}
	prompt, err := project.WriteAnswerPrompt(repo, false)
	if err != nil {
		return answerResult{}, fmt.Errorf("cannot write the answer talk's instructions: %w", err)
	}
	asked := &answerAsked{read: qs}
	for _, q := range qs {
		asked.questions = append(asked.questions, q.ID)
	}
	message := answerMessage(file, len(qs))
	terminal := func(ctx context.Context, _ string) error { return a.terminal(ctx, message) }
	session := terminal
	if a.pane != "" {
		about := answerSubject{first: message, asked: asked, tracker: a.tracker, every: a.every, live: a.live,
			scope: scope, skip: a.skip, panes: a.panes, agent: herdr.AgentName("answer-" + a.pane), repo: repo,
			deferred: a.deferred, err: a.err}
		ps := paneSession{panes: a.panes, pane: a.pane, hooks: a.hooks, claude: a.claude, fallback: terminal,
			out: a.out, err: a.err, progress: a.line, about: about}
		if a.aside {
			ps.aside = a.panes.SplitPaneAside
		}
		session = ps.talk
	}
	err = session(ctx, prompt)
	read := ctx
	if ctx.Err() != nil {
		read = context.WithoutCancel(ctx)
	}
	r := a.left(read, asked)
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return r, ctx.Err()
	case errors.As(err, &exit):
		fmt.Fprintln(a.err, "claude ended with", exit) // it may have recorded answers all the same
	case err != nil:
		return r, err
	}
	return r, nil
}

// left reads from bd how the talk left its questions, those queued into it too: closed ones answered,
// the rest open. One bd can't read counts as open. A question queued that claude was never given and
// is still open wasn't asked, for the dashboard to queue into the next talk.
func (a answerTalk) left(ctx context.Context, asked *answerAsked) answerResult {
	var r answerResult
	for _, id := range asked.questions {
		switch st, err := a.tracker.Status(ctx, id); {
		case err == nil && st == dispatch.StatusClosed:
			r.Answered = append(r.Answered, id)
		case !slices.Contains(asked.waiting, id):
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
// over, as an epic handed back ends a breakdown's, once all of them are (asked.closed, as answerShown
// last read it). Under the dashboard (live), questions that come to wait while it is open are read
// from bd in scope and added to the questions file, which claude reads again after each question,
// and given to claude in the pane, by the name agent, those still open once it waits on nothing.
type answerSubject struct {
	first   string
	asked   *answerAsked
	tracker answerTracker
	every   time.Duration
	live    *answerLive // nil where nothing queues questions into the talk
	scope   string
	skip    func(id string) bool // the questions in scope it doesn't take in (see answerTalk.skip); nil for none
	panes   answerPanes
	agent   string // claude's name in the pane, once given the questions queued
	repo    string
	// deferred is what the questions file says of the deferred tickets, as the talk's deferred, for
	// the file rewritten with the questions queued.
	deferred string
	err      io.Writer
}

// answerAsked is the questions the talk asks, in order, those queued into it included, and whether bd
// last said each was closed; those of them queued that claude hasn't been given in a message (waiting),
// though the questions file has them; and the questions as that file gives them.
type answerAsked struct {
	questions []string
	closed    bool
	waiting   []string
	read      []question
}

func (s answerSubject) message(string) (string, error) { return s.first, nil }

func (s answerSubject) handedBack(string) (string, bool) { return "", s.asked.closed }

func (answerSubject) name() string { return "the answer talk" }

func (s answerSubject) inPane() string {
	return "Answering " + plural(len(s.asked.questions), "question") + " with claude in the pane on the right, " +
		"which closes once they are answered. Type /exit there to leave the rest open; Ctrl+C here stops."
}

func (s answerSubject) shown(line *tui.InterviewLine, began time.Time) paneShown {
	return &answerShown{line: line, subject: s, began: began}
}

// opened takes in the talk's pane, for the dashboard's a key, and goes to it if the key was pressed
// before it opened.
func (s answerSubject) opened(ctx context.Context, pane string) {
	if s.live == nil || !s.live.opened(pane) {
		return
	}
	if err := s.panes.FocusPane(ctx, pane); err != nil {
		fmt.Fprintf(s.err, "orchestra couldn't go to the answer talk's pane %s: %s\n", pane, dispatch.FirstLine(err.Error()))
	}
}

// answerLive is the answer talk open under the dashboard as the dashboard's callbacks reach it: the
// pane it opened in, once it has, and the questions queued into it.
type answerLive struct {
	more chan struct{} // a question has come to wait: the talk reads bd for those it doesn't ask yet
	// took tells the dashboard of the questions bd has closed as the talk records their answers, before
	// it is over; nil for nothing to tell
	took  func(ids []string)
	mu    sync.Mutex
	pane  string // "" until the talk has opened
	focus bool   // the a key was pressed before it had
}

func newAnswerLive() *answerLive { return &answerLive{more: make(chan struct{}, 1)} }

// queue tells the talk a question has come to wait, without waiting for it to look.
func (l *answerLive) queue() {
	select {
	case l.more <- struct{}{}:
	default: // told already, and not yet looked
	}
}

// opened records the talk's pane, and reports whether the a key asked for it before.
func (l *answerLive) opened(pane string) (focus bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pane = pane
	return l.focus
}

// toFocus is the talk's pane for the a key to go to; "" before it has opened, which it then goes to.
func (l *answerLive) toFocus() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.focus = l.pane == ""
	return l.pane
}

// openPane is the talk's pane; "" before it has opened.
func (l *answerLive) openPane() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.pane
}

// answerShown reads, every so often, how many of the answer talk's questions bd has closed, for
// answerSubject to tell when the talk is over, and shows it in orchestra's pane with claude's state
// and the time so far, as an interview's line has them (drawn only when line isn't nil). Under the
// dashboard, it reads the questions queued into the talk from bd, adds them to the questions file and
// gives claude those still open once it waits on nothing: until claude has them or bd has closed
// them, the talk isn't over.
type answerShown struct {
	line    *tui.InterviewLine
	subject answerSubject
	began   time.Time
	now     tui.InterviewProgress
	counted time.Time          // when bd was last asked
	count   <-chan answerCount // how many are closed, once bd has said; nil when none is awaited
	last    answerCount        // bd's last count
	busy    time.Time          // when claude was last seen at work, not idle or done
	more    <-chan answerMore  // the questions queued, once read from bd; nil when none are awaited
	message string             // the message that brings in the questions waiting (subject.asked.waiting)
	given   <-chan answerGiven // claude given them; nil when not awaited
	batches int                // the files of questions written, the first one's included
	took    []string           // the questions closed that the dashboard was told of (see answerLive.took)
}

// answerCount is how many of the talk's first total questions bd has closed, as asked from began:
// which were counted (ids) and which of them are open; closed is -1 when bd couldn't say.
type answerCount struct {
	closed, total int
	ids, open     []string
	began         time.Time
}

// answerMore is questions queued into the talk, by ID and as read, and the message that brings them in.
type answerMore struct {
	ids     []string
	qs      []question
	message string
	err     error // why bd couldn't be read for them
}

// answerGiven is questions queued into the talk, and why claude couldn't be given them, if it couldn't.
type answerGiven struct {
	ids []string
	err error
}

// show reads bd's count, once given, and asks again every so often, takes in the questions queued,
// then draws the progress: st is claude's state ("" when the terminal couldn't say, which keeps the
// last one shown).
func (s *answerShown) show(ctx context.Context, _ string, st dispatch.AgentState) {
	asked := s.subject.asked
	select {
	case n := <-s.count: // never ready while count is nil
		s.count = nil
		if n.closed >= 0 {
			s.now.Questions, s.now.Answered = n.total, n.closed
			asked.closed = n.closed == n.total && n.total == len(asked.questions)
			s.last = n
			s.tell(n)
		}
	default: // not counted yet
	}
	if st != "" && st != dispatch.StateIdle && st != dispatch.StateDone {
		s.busy = time.Now()
	}
	if s.count == nil && (s.counted.IsZero() || time.Since(s.counted) >= s.subject.every) {
		s.counted, s.count = time.Now(), s.countClosed(ctx)
	}
	if s.subject.live != nil {
		s.queued(ctx, st)
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

// tell tells the dashboard, under which the talk is open, of the questions bd's count n has closed that
// it wasn't told of yet, so that they wait no longer while the talk goes on.
func (s *answerShown) tell(n answerCount) {
	live := s.subject.live
	if live == nil || live.took == nil {
		return
	}
	var fresh []string
	for _, id := range n.ids {
		if !slices.Contains(n.open, id) && !slices.Contains(s.took, id) {
			fresh = append(fresh, id)
		}
	}
	if len(fresh) > 0 {
		s.took = append(s.took, fresh...)
		live.took(fresh)
	}
}

// queued takes in the questions queued into the talk: it reads from bd, once told, those in scope it
// doesn't ask yet, which it then asks, and adds them to the questions file, which claude reads again
// after each question. It types nothing into claude's input while the talk waits on the maintainer,
// whose answer may be half typed there: it gives claude those still open, in a message, only once
// claude waits on nothing (see atRest), one batch at a time. Those claude couldn't be given are asked
// no longer: the dashboard queues them into the next talk.
func (s *answerShown) queued(ctx context.Context, st dispatch.AgentState) {
	asked := s.subject.asked
	select {
	case g := <-s.given: // never ready while given is nil
		s.given = nil
		if g.err != nil {
			fmt.Fprintf(s.subject.err, "orchestra couldn't give the answer talk %s: %s\n", strings.Join(g.ids, ", "),
				dispatch.FirstLine(g.err.Error()))
			asked.questions = slices.DeleteFunc(asked.questions, func(id string) bool { return slices.Contains(g.ids, id) })
		}
	default:
	}
	select {
	case m := <-s.more:
		s.more = nil
		switch {
		case m.err != nil:
			fmt.Fprintln(s.subject.err, "orchestra couldn't read the questions queued into the answer talk:",
				dispatch.FirstLine(m.err.Error()))
		case len(m.ids) > 0:
			asked.questions, asked.closed = append(asked.questions, m.ids...), false
			asked.waiting, asked.read = append(asked.waiting, m.ids...), append(asked.read, m.qs...)
			s.message = m.message // it brings in the earlier ones too
		}
	default:
	}
	if s.more == nil {
		select {
		case <-s.subject.live.more:
			s.more = s.readMore(ctx)
		default:
		}
	}
	if len(asked.waiting) > 0 && s.given == nil && (st == dispatch.StateIdle || st == dispatch.StateDone) &&
		s.atRest() {
		s.given = s.give(ctx, answerMore{ids: asked.waiting, message: s.message})
		asked.waiting, s.message = nil, ""
	}
}

// atRest reports whether claude may be given the questions waiting, in a message typed into its input:
// only when bd's last count, asked after claude was last seen at work, has every question claude was
// given closed, so that the talk waits on the maintainer for none, and one of those waiting still
// open. Those waiting that bd has closed, which claude took up from the questions file, wait no longer.
func (s *answerShown) atRest() bool {
	asked, n := s.subject.asked, s.last
	if n.began.IsZero() || !n.began.After(s.busy) {
		return false
	}
	open := false
	for _, id := range asked.questions {
		closed := slices.Contains(n.ids, id) && !slices.Contains(n.open, id)
		switch waiting := slices.Contains(asked.waiting, id); {
		case !waiting && !closed:
			return false // the talk may wait on the maintainer's answer to it
		case waiting && !closed:
			open = true
		}
	}
	if !open {
		asked.waiting, s.message = nil, ""
	}
	return open
}

// readMore reads from bd, in the background, the questions in the talk's scope that it doesn't ask
// yet nor skips, adds them at the end of the questions file, and writes them, with those read before
// and not given to claude yet, to a file of their own for a message to bring in; the channel gives them
// once read, none when there are none.
func (s *answerShown) readMore(ctx context.Context) <-chan answerMore {
	read := make(chan answerMore, 1) // the send doesn't block once the wait is over
	asked, pending := slices.Clone(s.subject.asked.questions), slices.Clone(s.subject.asked.waiting)
	all := slices.Clone(s.subject.asked.read)
	s.batches = max(s.batches, 1) + 1
	batch, skip := s.batches, s.subject.skip
	go func() {
		qs, err := readQuestions(ctx, s.subject.tracker, s.subject.scope, "")
		if err != nil {
			read <- answerMore{err: err}
			return
		}
		var give, fresh []question
		var ids []string
		for _, q := range qs {
			switch {
			case !slices.Contains(asked, q.ID) && skip != nil && skip(q.ID):
			case !slices.Contains(asked, q.ID):
				ids, fresh = append(ids, q.ID), append(fresh, q)
				give = append(give, q)
			case slices.Contains(pending, q.ID):
				give = append(give, q)
			}
		}
		if len(fresh) == 0 {
			read <- answerMore{}
			return
		}
		text := questionsText(slices.Concat(all, fresh)) + deferredText(s.subject.deferred)
		if _, err := project.WriteAnswerQuestions(s.subject.repo, []byte(text)); err != nil {
			read <- answerMore{err: err}
			return
		}
		file, err := project.WriteAnswerMore(s.subject.repo, batch, []byte(questionsText(give)))
		if err != nil {
			read <- answerMore{err: err}
			return
		}
		read <- answerMore{ids: ids, qs: fresh, message: moreMessage(file, len(give))}
	}()
	return read
}

// give gives claude in the talk's pane the questions queued, in the background: it names claude, as
// the terminal prompts an agent by name, and prompts it with their message. The channel says once
// claude has started on them, or why it couldn't be given them.
func (s *answerShown) give(ctx context.Context, m answerMore) <-chan answerGiven {
	given := make(chan answerGiven, 1) // the send doesn't block once the wait is over
	pane, panes, agent := s.subject.live.openPane(), s.subject.panes, s.subject.agent
	go func() {
		_, err := panes.AdoptAgent(ctx, pane, agentkind.Claude.Name, agent)
		if err == nil {
			err = panes.Prompt(ctx, agent, m.message)
		}
		given <- answerGiven{ids: m.ids, err: err}
	}()
	return given
}

// countClosed counts the questions bd has closed, of those the talk asks now, in the background so
// that the wait goes on reading the terminal meanwhile; the channel gives the count once read.
func (s *answerShown) countClosed(ctx context.Context) <-chan answerCount {
	read := make(chan answerCount, 1) // the send doesn't block once the wait is over
	questions, began := slices.Clone(s.subject.asked.questions), time.Now()
	go func() {
		n := 0
		var open []string
		for _, id := range questions {
			st, err := s.subject.tracker.Status(ctx, id)
			if err != nil {
				read <- answerCount{closed: -1}
				return
			}
			if st == dispatch.StatusClosed {
				n++
			} else {
				open = append(open, id)
			}
		}
		read <- answerCount{closed: n, total: len(questions), ids: questions, open: open, began: began}
	}()
	return read
}

// clear removes the line, if one is drawn, before orchestra's pane goes on.
func (s *answerShown) clear() {
	if s.line != nil {
		s.line.Clear()
	}
}
