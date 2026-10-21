package dispatch

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"unicode/utf8"
)

// handoffHeading opens what a new worker is told of the hand-offs of the closed tickets blocking its own.
const handoffHeading = "The closed tickets yours builds on handed on, as they closed, what it needs to know of them:"

// closedBlocker is a closed ticket blocking another, as bd show gives it among the other's dependencies.
func closedBlocker(id, title, reason string) Ticket {
	return Ticket{ID: id, Title: title, Status: StatusClosed, DependencyType: "blocks", CloseReason: reason}
}

// A new worker is given the close reason of each closed ticket blocking its own, on one line each, a long
// one cut; a blocker closed without a reason, one not closed and a ticket linked otherwise are left out.
func TestHandoffNoteGivesTheClosedBlockersReasons(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("Renamed Foo to Bar in internal/x. ", 40)
	tk := Ticket{ID: "C", Dependencies: []Ticket{
		closedBlocker("A", "first", "Added Loop.withLinks;\nfirstNotes reads it."),
		closedBlocker("Q", "no reason", ""),
		closedBlocker("B", "second", long),
		{ID: "O", Title: "open", Status: StatusOpen, DependencyType: "blocks"},
		{ID: "R", Title: "related", Status: StatusClosed, DependencyType: "related", CloseReason: "Not a blocker."},
		{ID: "P", Title: "parent", Status: StatusClosed, DependencyType: "parent-child", CloseReason: "All merged."},
	}}
	got := handoffNote(tk)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if !strings.HasPrefix(got, "\n\n") || !strings.HasSuffix(got, "\n") || len(lines) != 3 || lines[0] != handoffHeading {
		t.Fatalf("handoffNote:\n%q", got)
	}
	if want := "- A (first): Added Loop.withLinks; firstNotes reads it."; lines[1] != want {
		t.Errorf("A's hand-off %q, want %q", lines[1], want)
	}
	reason, ok := strings.CutPrefix(lines[2], "- B (second): ")
	if !ok || !strings.HasSuffix(reason, "…") || utf8.RuneCountInString(reason) > handoffChars ||
		!strings.HasPrefix(long, strings.TrimSuffix(reason, "…")) {
		t.Errorf("B's hand-off isn't its reason cut to %d characters: %q", handoffChars, lines[2])
	}
	for _, not := range []string{"Q (", "O (", "R (", "P ("} {
		if strings.Contains(got, not) {
			t.Errorf("handoffNote gives %s, which isn't a closed blocker with a reason:\n%s", not, got)
		}
	}
}

// At most handoffMax hand-offs are given, in the order bd gives the blockers.
func TestHandoffNoteGivesAtMostFive(t *testing.T) {
	t.Parallel()
	var tk Ticket
	for i := range handoffMax + 2 {
		tk.Dependencies = append(tk.Dependencies, closedBlocker(fmt.Sprintf("K%d", i), "t", "Done."))
	}
	got := handoffNote(tk)
	if n := strings.Count(got, "\n- "); n != handoffMax || !strings.Contains(got, "- K0 (t)") ||
		strings.Contains(got, fmt.Sprintf("K%d", handoffMax)) {
		t.Errorf("handoffNote gives %d hand-offs, want the first %d:\n%s", n, handoffMax, got)
	}
}

// A ticket blocked by nothing, or only by tickets closed without a reason, gets nothing new.
func TestHandoffNoteWithoutBlockers(t *testing.T) {
	t.Parallel()
	for _, tk := range []Ticket{{ID: "A"}, {ID: "A", Dependencies: []Ticket{closedBlocker("B", "b", " \n ")}}} {
		if got := handoffNote(tk); got != "" {
			t.Errorf("handoffNote of %+v = %q, want nothing", tk, got)
		}
		if got := firstNotes(tk, ""); got != "" {
			t.Errorf("firstNotes of %+v = %q, want nothing", tk, got)
		}
	}
}

// A new worker's first message names the spec, then the hand-offs, then what an earlier attempt left.
func TestFirstNotesPutTheHandoffsAfterTheSpec(t *testing.T) {
	t.Parallel()
	tk := Ticket{ID: "A", Metadata: json.RawMessage(`{"spec":".orchestra/specs/app.md"}`),
		Dependencies: []Ticket{closedBlocker("B", "b", "Moved x.go to y.go.")}}
	earlier := "\n\nAn earlier attempt at this ticket left work on wt/A: uncommitted changes (git status). …\n"
	want := "\n\n" + specStartA + "\n\n\n" + handoffHeading + "\n- B (b): Moved x.go to y.go.\n" + earlier
	if got := firstNotes(tk, earlier); got != want {
		t.Errorf("firstNotes\n got %q\nwant %q", got, want)
	}
}

// A ticket that closes with a hand-off hands it to the worker of the ticket it blocks, which starts after
// it, in its first message; the hand-off of a blocker closed before the run is given too, and a blocker
// closed without one gives nothing. A ticket blocked by nothing is told of no hand-off.
func TestNewWorkerIsGivenItsBlockersHandoffs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("Z", "done before", 1)
		h.beads.add("Y", "done quietly", 1)
		h.beads.add("D", "independent", 3)
		h.beads.set("Z", "closed")
		h.beads.set("Y", "closed")
		h.beads.tickets["Z"].CloseReason = "Added the -x flag."
		h.beads.link("B", "A", "blocks")
		h.beads.link("B", "Z", "blocks")
		h.beads.link("B", "Y", "blocks")
		f := &runFiles{rules: map[string]string{}, first: map[string]string{}}
		h.worker("A", f.then(closesWith("a.txt", "Renamed Loop.run to Loop.Run.")))
		h.worker("B", f.then(finishes("b.txt")))
		h.worker("D", f.then(finishes("d.txt")))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		first := f.first["B"]
		for _, want := range []string{handoffHeading, "\n- A (first): Renamed Loop.run to Loop.Run.\n",
			"\n- Z (done before): Added the -x flag.\n"} {
			if !strings.Contains(first, want) {
				t.Errorf("B's first message lacks %q:\n%s", want, first)
			}
		}
		if strings.Contains(first, "Y (") {
			t.Errorf("B's first message gives Y, closed without a reason:\n%s", first)
		}
		for _, id := range []string{"A", "D"} {
			if strings.Contains(f.first[id], handoffHeading) {
				t.Errorf("%s, blocked by nothing, was told of hand-offs:\n%s", id, f.first[id])
			}
		}
		if strings.Contains(f.rules["B"], handoffHeading) {
			t.Errorf("B's standing rules hold the hand-offs, which are for its first message:\n%s", f.rules["B"])
		}
	})
}

// A resumed worker isn't given its blockers' hand-offs again: it was given them as it started.
func TestResumedWorkerIsNotGivenTheHandoffsAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "B")
		h.beads.add("B", "second", 2)
		h.beads.add("Z", "done before", 1)
		h.beads.set("Z", "closed")
		h.beads.tickets["Z"].CloseReason = "Added the -x flag."
		h.beads.link("B", "Z", "blocks")
		f := &runFiles{rules: map[string]string{}, first: map[string]string{}}
		h.worker("B", f.then(vanishes), finishes("b.txt"))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !strings.Contains(f.first["B"], handoffHeading) {
			t.Errorf("B's new worker wasn't given Z's hand-off:\n%s", f.first["B"])
		}
		starts := h.herdr.argsFor("B")
		if len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Fatalf("B's starts %q; want a new worker, then one resuming %s", starts, testSession)
		}
		if msg := starts[1][len(starts[1])-1]; strings.Contains(msg, handoffHeading) || strings.Contains(msg, "-x flag") {
			t.Errorf("the resumed worker was given the hand-offs again: %q", msg)
		}
	})
}
