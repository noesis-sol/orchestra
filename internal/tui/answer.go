package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The answer talk is where the maintainer answers the questions tickets wait on, in a Claude Code
// session orchestra opens beside the dashboard. By default it opens by itself as soon as a question
// waits, without taking the keyboard focus, and the hint says a question is there and that a goes to
// it ("a question for you · a to answer"); with "questions": "key" in the settings it opens on a,
// focused ("a to answer 2 questions"). Questions that come to wait while it is open are queued into
// it: one talk, never one per question. The run goes on meanwhile, and takes an answered question's
// ticket back as it does whoever answered it.

// AnswerTalk is the answer talk as the dashboard opens it: none of its callbacks may block.
type AnswerTalk struct {
	// Open opens it beside the dashboard, with the keyboard focus or without; the program is sent
	// AnswerOver once it is over.
	Open func(focus bool)
	// Queue has the open talk take in the questions that have come to wait since it opened.
	Queue func()
	// Focus gives the open talk's pane the keyboard focus.
	Focus func()
}

// AnswerOver tells the dashboard the answer talk it opened is over: the questions it asked, by ID,
// those of them it answered, and why it failed, if it did.
type AnswerOver struct {
	Asked, Answered []string
	Err             error
}

// WithAnswer is the dashboard with the answer talk, which opens by itself, unfocused, as soon as a
// question waits with auto, else on the a key. Without it, as where no pane can be opened beside the
// dashboard's, the dashboard offers no talk.
func (m Dashboard) WithAnswer(talk AnswerTalk, auto bool) Dashboard {
	m.answer, m.answerAuto = &talk, auto
	return m
}

// openAnswer marks the answer talk open, offered every question waiting, and reports whether it is to
// be opened: only where the dashboard has one, not open already, with questions waiting.
func (m *Dashboard) openAnswer() bool {
	qs := m.waitingQuestions()
	if m.answer == nil || m.answering || m.quitting || len(qs) == 0 {
		return false
	}
	m.answering, m.answerFailed = true, ""
	m.offered = append(m.offered, qs...)
	return true
}

// takeQuestions, after one of the loop's events, hands the answer talk the questions that wait and it
// hasn't been offered yet: queued into it while it is open, or, with auto, opening it unfocused.
// A question the talk was offered isn't again: one left open there waits for the a key.
func (m *Dashboard) takeQuestions() {
	if m.answer == nil || m.quitting {
		return
	}
	var fresh []string
	for _, q := range m.waitingQuestions() {
		if !slices.Contains(m.offered, q) {
			fresh = append(fresh, q)
		}
	}
	switch {
	case len(fresh) == 0:
	case m.answering:
		m.offered, m.queued = append(m.offered, fresh...), append(m.queued, fresh...)
		m.answer.Queue()
	case m.answerAuto && m.openAnswer():
		m.answer.Open(false)
	}
}

// answerOver takes in the answer talk's end: its questions answered no longer wait, and why it
// failed, if it did, shows until it opens again. A question queued into it that it ended before asking
// counts as not offered, for the talk to take up again.
func (m *Dashboard) answerOver(o AnswerOver) {
	m.answering = false
	m.answered = append(m.answered, o.Answered...)
	if o.Err != nil {
		m.answerFailed = "the answer talk: " + dispatch.FirstLine(o.Err.Error())
	} else {
		m.offered = slices.DeleteFunc(m.offered, func(q string) bool {
			return slices.Contains(m.queued, q) && !slices.Contains(o.Asked, q)
		})
	}
	m.queued = nil
	m.takeQuestions()
}

// waitingQuestions are the questions tickets wait on, each once, in the rows' order: those of the rows
// waiting on one (each row whose event named no question counts as one of its own), less those the
// answer talk has answered.
func (m Dashboard) waitingQuestions() []string {
	var qs []string
	for _, r := range m.rows() {
		q := r.question
		if q == "" {
			q = "for " + r.id
		}
		if r.state == rowAsked && !slices.Contains(qs, q) && !slices.Contains(m.answered, q) {
			qs = append(qs, q)
		}
	}
	return qs
}

// questionOf is the ID of the question an EvAsked's Detail names ("Q: title"); "" when it names none.
func questionOf(detail string) string {
	f := strings.Fields(detail)
	if len(f) == 0 {
		return ""
	}
	return strings.TrimSuffix(f[0], ":")
}

// answerKey is the hint's word on the answer talk: while questions wait, the key that opens it, or
// once it is open that they wait for the maintainer there and the key that goes to it; that it is open
// once none does; ok is false with nothing to say, the stop question open, or no talk to open.
func (m Dashboard) answerKey() (k []keyHint, ok bool) {
	switch n := len(m.waitingQuestions()); {
	case m.answer == nil || m.asking:
		return nil, false
	case m.answering && n > 0:
		waiting := keyHint{does: "a question for you", short: "1 question"}
		if n > 1 {
			waiting.does, waiting.short = strconv.Itoa(n)+" questions for you", strconv.Itoa(n)+" questions"
		}
		return []keyHint{waiting, {key: "a", does: "answer", short: "answer"}}, true
	case m.answering:
		return []keyHint{{does: "answering in the pane on the right", short: "answering"}}, true
	case n > 0:
		return []keyHint{{key: "a", does: "answer " + plural(n, "question", "questions"),
			short: "answer " + strconv.Itoa(n)}}, true
	}
	return nil, false
}

// answerFailedLine is why the answer talk last failed, on a line of its own above the hint, at width w;
// "" when it didn't.
func (m Dashboard) answerFailedLine(w int) string {
	if m.answerFailed == "" {
		return ""
	}
	return deferredStyle.Render(ansi.Truncate(" ! "+m.answerFailed, w, "…"))
}
