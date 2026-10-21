package tui

import (
	"slices"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The answer talk is where the maintainer answers the questions tickets wait on, in a Claude Code
// session orchestra opens beside the dashboard. While questions wait, the hint offers a to open it
// ("a to answer 2 questions"); while it is open, the hint says so in place of the key. The run goes on
// meanwhile, and takes an answered question's ticket back as it does whoever answered it.

// AnswerOver tells the dashboard the answer talk it opened is over: the questions it answered, by ID,
// and why it failed, if it did.
type AnswerOver struct {
	Answered []string
	Err      error
}

// WithAnswer is the dashboard with the answer talk: open opens it beside the dashboard, without
// waiting for it, and the program is sent AnswerOver once it is over; onAsk opens it by itself as a
// ticket comes to wait on a question. Without it, as where no pane can be opened beside the
// dashboard's, the dashboard offers no talk.
func (m Dashboard) WithAnswer(open func(), onAsk bool) Dashboard {
	m.answer, m.answerOnAsk = open, onAsk
	return m
}

// openAnswer marks the answer talk open and reports whether it is to be opened: only where the
// dashboard has one, not open already, with questions waiting.
func (m *Dashboard) openAnswer() bool {
	if m.answer == nil || m.answering || m.quitting || len(m.waitingQuestions()) == 0 {
		return false
	}
	m.answering, m.answerFailed = true, ""
	return true
}

// answerOver takes in the answer talk's end: its questions answered no longer wait, and why it
// failed, if it did, shows until it opens again.
func (m *Dashboard) answerOver(o AnswerOver) {
	m.answering = false
	m.answered = append(m.answered, o.Answered...)
	if o.Err != nil {
		m.answerFailed = "the answer talk: " + dispatch.FirstLine(o.Err.Error())
	}
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

// answerKey is the hint's word on the answer talk: the key that opens it while questions wait, or
// that it is open; ok is false with nothing to say, the stop question open, or no talk to open.
func (m Dashboard) answerKey() (k keyHint, ok bool) {
	switch n := len(m.waitingQuestions()); {
	case m.answer == nil || m.asking:
		return keyHint{}, false
	case m.answering:
		return keyHint{does: "answering in the pane on the right", short: "answering"}, true
	case n > 0:
		return keyHint{key: "a", does: "answer " + plural(n, "question", "questions"),
			short: "answer " + strconv.Itoa(n)}, true
	}
	return keyHint{}, false
}

// answerFailedLine is why the answer talk last failed, on a line of its own above the hint, at width w;
// "" when it didn't.
func (m Dashboard) answerFailedLine(w int) string {
	if m.answerFailed == "" {
		return ""
	}
	return deferredStyle.Render(ansi.Truncate(" ! "+m.answerFailed, w, "…"))
}
