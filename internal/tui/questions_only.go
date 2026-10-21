package tui

import "slices"

// A run with nothing ready but tickets waiting on questions asks for the answer talk rather than end
// (dispatch.Answers): the dashboard opens it on them, unfocused, whatever the questions setting, as it
// opens by itself when a question waits, or queues them into the talk open, and tells the loop once
// that talk is over.

// AskQuestions has the dashboard open the answer talk, unfocused, on the questions IDs, or queue them
// into the talk open: the loop has nothing ready but the tickets waiting on them. Over is closed once
// that talk is over, or at once when none can open.
type AskQuestions struct {
	IDs  []string
	Over chan<- struct{}
}

// askQuestions takes in the loop's AskQuestions: its questions wait, as a row's question does, open as
// the loop read them from bd, and the talk opens on them, or takes in those it hasn't been offered
// while open.
func (m *Dashboard) askQuestions(a AskQuestions) {
	m.idleQuestions = a.IDs
	m.answered = slices.DeleteFunc(m.answered, func(q string) bool { return slices.Contains(a.IDs, q) })
	m.talkOver = append(m.talkOver, a.Over)
	switch {
	case m.answering:
		m.takeQuestions()
	case m.openAnswer():
		m.answer.Open(false)
	default: // quitting, or no talk to open
		m.endIdleTalk()
	}
}

// endIdleTalk tells the loop waiting on the answer talk that it is over: the questions it asked for
// wait no longer, unless it asks again.
func (m *Dashboard) endIdleTalk() {
	for _, over := range m.talkOver {
		close(over)
	}
	m.idleQuestions, m.talkOver = nil, nil
}
