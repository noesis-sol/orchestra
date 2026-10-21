package tui

import (
	"errors"
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// talkCalls records what the dashboard asked of its answer talk, in order: "open", "open focused",
// "queue", "focus".
type talkCalls []string

// autoRun is a run whose dashboard has the answer talk, opening by itself with auto, its calls in
// *calls.
func autoRun(t *testing.T, calls *talkCalls, auto bool) *fixture {
	f := run(t)
	f.m = f.m.WithAnswer(AnswerTalk{
		Open: func(focus bool) {
			if focus {
				*calls = append(*calls, "open focused")
			} else {
				*calls = append(*calls, "open")
			}
		},
		Queue: func() { *calls = append(*calls, "queue") },
		Focus: func() { *calls = append(*calls, "focus") },
	}, auto)
	return f
}

// By default the talk opens by itself, unfocused, as a question comes to wait; one that comes while
// it is open is queued into it, never opening another; a goes to it, and the hint says so.
func TestAnswerTalkOpensUnfocusedAndQueues(t *testing.T) {
	var calls talkCalls
	f := autoRun(t, &calls, true).picked("k-1", "One").picked("k-2", "Two").picked("k-3", "Three").
		event(asked("k-1", "q-1: Which way?"))
	if !slices.Equal(calls, talkCalls{"open"}) {
		t.Fatalf("on a question the talk got %q, want it opened unfocused", calls)
	}
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · a question for you · a to answer"; got != want {
		t.Errorf("hint %q, want %q", got, want)
	}
	if got, want := ansi.Strip(f.dash().hintLine(45)), " s stop after current · 1 question · a answer"; got != want {
		t.Errorf("narrow hint %q, want %q", got, want)
	}
	f.event(asked("k-2", "q-2: How many?"), asked("k-3", "q-2: How many?"))
	if !slices.Equal(calls, talkCalls{"open", "queue"}) {
		t.Errorf("a second question: the talk got %q, want it queued once", calls)
	}
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · 2 questions for you · a to answer"; got != want {
		t.Errorf("hint %q, want %q", got, want)
	}
	if f.keys("a"); !slices.Equal(calls, talkCalls{"open", "queue", "focus"}) {
		t.Errorf("a: the talk got %q, want it focused", calls)
	}
}

// A question left open in the talk isn't offered again by itself: a opens the talk for it, focused.
// One queued that the talk ended before asking is: the talk opens again for it.
func TestAnswerTalkReopensOnlyForQuestionsNotAsked(t *testing.T) {
	var calls talkCalls
	f := autoRun(t, &calls, true).picked("k-1", "One").picked("k-2", "Two").
		event(asked("k-1", "q-1: Which way?")).
		send(AnswerOver{Asked: []string{"q-1"}})
	if !slices.Equal(calls, talkCalls{"open"}) {
		t.Fatalf("a question left open: the talk got %q", calls)
	}
	if f.keys("a"); !slices.Equal(calls, talkCalls{"open", "open focused"}) {
		t.Errorf("a: the talk got %q, want it opened focused", calls)
	}
	calls = nil
	f.event(asked("k-2", "q-2: How many?")).send(AnswerOver{Asked: []string{"q-1"}})
	if !slices.Equal(calls, talkCalls{"queue", "open"}) {
		t.Errorf("a question queued but not asked: the talk got %q, want it opened again", calls)
	}
	calls = nil
	f.send(AnswerOver{Err: errors.New("no pane")})
	if f.event(asked("k-1", "q-1: Which way?")); len(calls) != 0 {
		t.Errorf("after a talk that failed, the talk got %q", calls)
	}
}

// With "questions": "key", nothing opens until a, which opens the talk focused; a question that comes
// to wait while it is open is queued into it all the same.
func TestAnswerTalkOnTheKey(t *testing.T) {
	var calls talkCalls
	f := autoRun(t, &calls, false).picked("k-1", "One").picked("k-2", "Two").
		event(asked("k-1", "q-1: Which way?"))
	if len(calls) != 0 {
		t.Fatalf("with the setting at key, a question: the talk got %q", calls)
	}
	if got, want := hintOf(f.dash()), "  s to stop after the current tickets · a to answer 1 question"; got != want {
		t.Errorf("hint %q, want %q", got, want)
	}
	f.keys("a").event(asked("k-2", "q-2: How many?"))
	if !slices.Equal(calls, talkCalls{"open focused", "queue"}) {
		t.Errorf("the talk got %q", calls)
	}
}
