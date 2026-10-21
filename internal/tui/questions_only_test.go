package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// isClosed reports whether over is closed.
func isClosed(over chan struct{}) bool {
	select {
	case <-over:
		return true
	default:
		return false
	}
}

// With nothing ready but questions, the loop asks for the answer talk: it opens unfocused, even with
// the questions setting at key, the hint offers it, and the loop hears once it is over.
func TestLoopAskingOpensTheAnswerTalk(t *testing.T) {
	var calls talkCalls
	over := make(chan struct{})
	f := autoRun(t, &calls, false).send(AskQuestions{IDs: []string{"q-1"}, Over: over})
	if !slices.Equal(calls, talkCalls{"open"}) {
		t.Fatalf("the loop asking: the talk got %q, want it opened unfocused", calls)
	}
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · a question for you · a to answer"; got != want {
		t.Errorf("hint %q, want %q", got, want)
	}
	if isClosed(over) {
		t.Fatal("the loop heard the talk was over while it is open")
	}
	f.send(AnswerOver{Asked: []string{"q-1"}, Answered: []string{"q-1"}})
	if !isClosed(over) {
		t.Error("the talk is over, and the loop wasn't told")
	}
	if qs := f.dash().waitingQuestions(); len(qs) != 0 {
		t.Errorf("questions still waiting: %q", qs)
	}
}

// A talk open already, as a worker's question opened it, takes in the questions the loop asks for that
// it wasn't offered, and the loop hears once that talk is over.
func TestLoopAskingQueuesIntoTheTalkOpen(t *testing.T) {
	var calls talkCalls
	over := make(chan struct{})
	f := autoRun(t, &calls, true).picked("k-1", "One").event(asked("k-1", "q-1: Which way?")).
		send(AskQuestions{IDs: []string{"q-1", "q-2"}, Over: over})
	if !slices.Equal(calls, talkCalls{"open", "queue"}) {
		t.Fatalf("the loop asking with the talk open: the talk got %q, want q-2 queued into it", calls)
	}
	f.send(AnswerOver{Asked: []string{"q-1", "q-2"}})
	if !isClosed(over) {
		t.Error("the talk is over, and the loop wasn't told")
	}
}

// Where the talk can't open, as the dashboard quits, the loop hears at once that it is over.
func TestLoopAskingWithNoTalkHearsAtOnce(t *testing.T) {
	over := make(chan struct{})
	run(t).send(AskQuestions{IDs: []string{"q-1"}, Over: over})
	if !isClosed(over) {
		t.Error("no talk opened, and the loop wasn't told")
	}
}

// The run's closing names the questions it ended on, on a line of their own, before SETUP_WAITS and
// the scope.
func TestRunEndNamesTheQuestionsLeftOpen(t *testing.T) {
	questions := "QUESTIONS_OPEN: q-1 (holding k-1, k-2): answer with orchestra answer, then run again"
	text := "READY_EMPTY after 0 tickets; " + questions + "; SCOPE_OPEN: k-1: …"
	var b strings.Builder
	Printer{Out: &b, Styled: true, Width: 400}.End(ended(t, dispatch.Event{Kind: dispatch.EvDone, Text: text}))
	if got, want := ansi.Strip(b.String()), "♪ Completed the Run  no tickets · 1h12m\n  "+questions+
		"\n  SCOPE_OPEN: k-1: …\n"; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}
