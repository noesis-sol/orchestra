package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// a pressed as the run ends opens the talk, and the loop's Finished just after still closes the
// dashboard, the talk open: orchestra waits for it once the dashboard has gone (answerTalks.finish).
// The talk's end, come after, changes nothing; a pressed after Finished opens no talk.
func TestAnswerKeyThenFinished(t *testing.T) {
	var opened int
	f := answerRun(t, &opened, false).picked("k-1", "One").event(asked("k-1", "q-1: Which way?")).keys("a")
	if opened != 1 {
		t.Fatalf("a opened the talk %d times, want once", opened)
	}
	next, cmd := f.dash().Update(Finished{})
	if cmd == nil || cmd() != tea.Quit() {
		t.Fatalf("Finished with the talk open didn't quit the dashboard")
	}
	m := receive(next.(Dashboard), AnswerOver{Asked: []string{"q-1"}, Answered: []string{"q-1"}})
	if !m.quitting {
		t.Error("the talk's end took the dashboard out of quitting")
	}
	late := run(t)
	late.m = late.m.WithAnswer(AnswerTalk{Open: func(bool) { opened++ }, Queue: func() {}, Focus: func() {}}, false)
	late.picked("k-1", "One").event(asked("k-1", "q-1: Which way?")).send(Finished{}).keys("a")
	if opened != 1 {
		t.Errorf("a after Finished opened the talk")
	}
}
