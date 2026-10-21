package dispatch

import (
	"strings"
	"testing"
)

// A blocker closed with a reason that hands nothing on (bd's Closed and Responded, the worker prompt's
// nothing for dependents) and an answered question are left out, and don't count toward handoffMax: a
// ticket blocked by more of them than that is still given the one real hand-off after them.
func TestHandoffNoteLeavesOutPlaceholderReasons(t *testing.T) {
	t.Parallel()
	question := closedBlocker("H", "Which flag name?", "Answered: -x, as the other commands spell it.")
	question.Labels = []string{HumanLabel}
	tk := Ticket{ID: "C", Dependencies: []Ticket{
		closedBlocker("A", "a", "Closed"),
		closedBlocker("B", "b", "Responded"),
		closedBlocker("D", "d", "nothing for dependents"),
		closedBlocker("E", "e", "`Nothing for dependents.`"),
		closedBlocker("F", "f", " closed. "),
		question,
		closedBlocker("G", "g", "Added the -x flag to orchestra plan."),
	}}
	want := "\n\n" + handoffHeading + "\n- G (g): Added the -x flag to orchestra plan.\n"
	if got := handoffNote(tk); got != want {
		t.Errorf("handoffNote\n got %q\nwant %q", got, want)
	}
}

// A real reason that merely starts or ends like a placeholder is a hand-off.
func TestHandoffNoteKeepsReasonsLikePlaceholders(t *testing.T) {
	t.Parallel()
	for _, reason := range []string{"Closed the socket on stop.", "Nothing for dependents but the -x flag."} {
		tk := Ticket{ID: "C", Dependencies: []Ticket{closedBlocker("A", "a", reason)}}
		if got := handoffNote(tk); !strings.Contains(got, "- A (a): "+reason+"\n") {
			t.Errorf("handoffNote leaves out %q:\n%q", reason, got)
		}
	}
}
