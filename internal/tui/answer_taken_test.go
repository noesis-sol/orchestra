package tui

import (
	"slices"
	"testing"
)

// An answer the talk records while it stays open counts no longer in the hint: with nothing ready but
// questions, once each is answered the hint says the talk is open rather than count them, and the loop
// still waits on the talk.
func TestAnswerTakenWhileTheTalkIsOpen(t *testing.T) {
	var calls talkCalls
	over := make(chan struct{})
	f := autoRun(t, &calls, true).send(AskQuestions{IDs: []string{"q-1", "q-2"}, Over: over})
	const stop = "  s to stop after the current tickets · "
	if got, want := hintOf(f.dash()), stop+"2 questions for you · a to answer"; got != want {
		t.Fatalf("hint %q, want %q", got, want)
	}
	f.send(AnswerTaken{IDs: []string{"q-1"}})
	if got, want := hintOf(f.dash()), stop+"a question for you · a to answer"; got != want {
		t.Errorf("q-1 answered: hint %q, want %q", got, want)
	}
	f.send(AnswerTaken{IDs: []string{"q-2"}})
	if got, want := hintOf(f.dash()), stop+"answering in the pane on the right"; got != want {
		t.Errorf("both answered: hint %q, want %q", got, want)
	}
	if isClosed(over) {
		t.Error("the loop heard the talk was over while it is open")
	}
	f.send(AnswerOver{Asked: []string{"q-1", "q-2"}, Answered: []string{"q-1", "q-2"}})
	if !isClosed(over) {
		t.Error("the talk is over, and the loop wasn't told")
	}
	if !slices.Equal(calls, talkCalls{"open"}) {
		t.Errorf("the talk got %q, want it opened once", calls)
	}
}

// A row's question answered in the open talk counts no longer either, though the row waits until the
// loop takes its ticket back.
func TestAnswerTakenForARowsQuestion(t *testing.T) {
	var calls talkCalls
	f := autoRun(t, &calls, true).picked("k-1", "One").event(asked("k-1", "q-1: Which way?")).
		send(AnswerTaken{IDs: []string{"q-1"}})
	if qs := f.dash().waitingQuestions(); len(qs) != 0 {
		t.Errorf("questions still waiting: %q", qs)
	}
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · answering in the pane on the right"; got != want {
		t.Errorf("hint %q, want %q", got, want)
	}
}
