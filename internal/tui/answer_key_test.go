package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// asked is the event of ticket id coming to wait on the question detail names ("Q: title").
func asked(id, detail string) dispatch.Event {
	return dispatch.Event{Kind: dispatch.EvAsked, Ticket: id, Title: "Ticket " + id, Detail: detail}
}

// answerRun is a run whose dashboard has the answer talk, opened by itself on a question with onAsk;
// *opened counts the times it was opened.
func answerRun(t *testing.T, opened *int, onAsk bool) *fixture {
	f := run(t)
	f.m = f.m.WithAnswer(func() { *opened++ }, onAsk)
	return f
}

// hintOf is the dashboard's hint line, 100 columns wide, as plain text.
func hintOf(m Dashboard) string { return ansi.Strip(m.hintLine(100)) }

// The hint offers a only while questions wait, each counted once however many tickets wait on it, and
// only on a dashboard that has the talk.
func TestAnswerKeyOnlyWhileQuestionsWait(t *testing.T) {
	var opened int
	f := answerRun(t, &opened, false).done("k-0", "Earlier", "abc1234")
	if got := hintOf(f.dash()); strings.Contains(got, "answer") {
		t.Errorf("with no question waiting, the hint reads %q", got)
	}
	f.picked("k-1", "One").picked("k-2", "Two").picked("k-3", "Three").
		event(asked("k-1", "q-1: Which way?"), asked("k-2", "q-1: Which way?"), asked("k-3", "q-2: How many?"))
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · a to answer 2 questions"; got != want {
		t.Errorf("hint %q, want %q", got, want)
	}
	if got, want := ansi.Strip(f.dash().hintLine(34)), " s stop after current · a answer 2"; got != want {
		t.Errorf("narrow hint %q, want %q", got, want)
	}
	without := run(t).picked("k-1", "One").event(asked("k-1", "q-1: Which way?")).dash()
	if got := hintOf(without); strings.Contains(got, "answer") {
		t.Errorf("with no talk to open, the hint reads %q", got)
	}
	if press(without, "a"); opened != 0 {
		t.Errorf("a opened a talk the dashboard doesn't have")
	}
}

// a opens the talk once; while it is open the hint says so, and a opens nothing more. Once it is
// over, the questions it answered no longer count, and the hint offers a for the rest; a ticket the
// loop takes back counts no longer either.
func TestAnswerKeyOpensTheTalk(t *testing.T) {
	var opened int
	f := answerRun(t, &opened, false).picked("k-1", "One").picked("k-2", "Two").
		event(asked("k-1", "q-1: Which way?"), asked("k-2", "q-2: How many?")).keys("a")
	if opened != 1 {
		t.Fatalf("a opened the talk %d times, want once", opened)
	}
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · answering in the pane on the right"; got != want {
		t.Errorf("hint %q, want %q", got, want)
	}
	if f.keys("a"); opened != 1 {
		t.Errorf("a opened the talk again while it was open")
	}
	f.send(AnswerOver{Answered: []string{"q-1"}})
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · a to answer 1 question"; got != want {
		t.Errorf("after the talk, hint %q, want %q", got, want)
	}
	f.event(dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-2", Title: "Two", Detail: "q-2: How many?"})
	if got := hintOf(f.dash()); strings.Contains(got, "answer") {
		t.Errorf("with every ticket taken back, the hint reads %q", got)
	}
	if f.keys("a"); opened != 1 {
		t.Errorf("a opened the talk with no question waiting")
	}
}

// A talk that failed says why above the hint until it opens again; the stop question takes a for
// nothing.
func TestAnswerTalkFailureShowsUntilItOpensAgain(t *testing.T) {
	var opened int
	f := answerRun(t, &opened, false).sized(100, 30).picked("k-1", "One").
		event(asked("k-1", "q-1: Which way?")).keys("a").
		send(AnswerOver{Err: errors.New("no pane could be opened beside orchestra's\nsecond line")})
	const failed = " ! the answer talk: no pane could be opened beside orchestra's"
	if got := ansi.Strip(f.dash().View()); !strings.Contains(got, failed) {
		t.Errorf("the dashboard lacks %q:\n%s", failed, got)
	}
	if f.keys("s", "a"); opened != 1 {
		t.Errorf("a opened the talk with the stop question open")
	}
	f.keys("esc", "a")
	if got := ansi.Strip(f.dash().View()); opened != 2 || strings.Contains(got, failed) {
		t.Errorf("opened %d times; the dashboard:\n%s", opened, got)
	}
}

// With the setting on, the talk opens by itself as a ticket comes to wait on a question, unless it is
// open already.
func TestAnswerTalkOpensByItselfOnAQuestion(t *testing.T) {
	var opened int
	f := answerRun(t, &opened, true).picked("k-1", "One").picked("k-2", "Two")
	if f.event(asked("k-1", "q-1: Which way?")); opened != 1 {
		t.Fatalf("the talk opened %d times on a question, want once", opened)
	}
	if f.event(asked("k-2", "q-2: How many?")); opened != 1 {
		t.Errorf("the talk opened again while it was open")
	}
	var off int
	if answerRun(t, &off, false).picked("k-1", "One").event(asked("k-1", "q-1: Which way?")); off != 0 {
		t.Errorf("with the setting off, the talk opened by itself")
	}
}

// questionOf reads the question's ID from an EvAsked's Detail.
func TestQuestionOf(t *testing.T) {
	for detail, want := range map[string]string{"q-1: Which way?": "q-1", "q-9 Which\nway?": "q-9", "": ""} {
		if got := questionOf(detail); got != want {
			t.Errorf("questionOf(%q) = %q, want %q", detail, got, want)
		}
	}
}
