package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A finished ticket that can't merge until the maintainer acts shows as blocked, saying why, and
// counts under Needs you; a worker still running when the run stops shows as stopped, and a ticket
// whose own work needs a look as for review.

// rowLine is the tickets table's line for ticket id in view, or "".
func rowLine(view, id string) string {
	for l := range strings.SplitSeq(view, "\n") {
		if strings.Contains(l, " "+id+" ") {
			return l
		}
	}
	return ""
}

// needsYouIn is the Needs you total in view's strip of totals, or "" without one.
func needsYouIn(view string) string {
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		labels := strings.Split(l, "│")
		for j, label := range labels {
			if strings.TrimSpace(label) == "Needs you" && i+1 < len(lines) {
				if values := strings.Split(lines[i+1], "│"); j < len(values) {
					return strings.TrimSpace(values[j])
				}
			}
		}
	}
	return ""
}

const dirtyWhy = "main checkout has uncommitted changes"

// A hold met before merging (DIRTY_TREE) blocks the finished ticket while another still runs; once
// the run stops over it, that other is stopped, and the blocked one stays blocked.
func TestAHoldBeforeMergingShowsTheTicketBlocked(t *testing.T) {
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Undo a property edit"},
		dispatch.Event{Kind: dispatch.EvHold, Ticket: "k-1", Blocked: dirtyWhy,
			Text: "HOLD: DIRTY_TREE: uncommitted changes in /repo; stopping before merging wt/k-1; …"})
	v := ansi.Strip(m.View())
	if row := rowLine(v, "k-1"); !strings.Contains(row, "■ blocked") || !strings.Contains(row, dirtyWhy) {
		t.Errorf("k-1 should show blocked, saying %q:\n%s", dirtyWhy, v)
	}
	if row := rowLine(v, "k-2"); !strings.Contains(row, "▶ working") {
		t.Errorf("k-2 should still be working:\n%s", v)
	}
	if got := needsYouIn(v); got != "? 1" {
		t.Errorf("Needs you %q, want ? 1:\n%s", got, v)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvStop, Ticket: "k-1", Detail: "DIRTY_TREE", Blocked: dirtyWhy,
		Text: "DIRTY_TREE: uncommitted changes in /repo; stopping before merging wt/k-1; …"})
	final := ansi.Strip(printedEnd(m, 80))
	if row := rowLine(final, "k-1"); !strings.Contains(row, "■ blocked") || !strings.Contains(row, dirtyWhy) {
		t.Errorf("the summary should show k-1 blocked:\n%s", final)
	}
	if row := rowLine(final, "k-2"); !strings.Contains(row, "■ stopped") {
		t.Errorf("the summary should show k-2, running when the run stopped, as stopped:\n%s", final)
	}
	if got := needsYouIn(final); got != "? 1" {
		t.Errorf("Needs you %q, want ? 1:\n%s", got, final)
	}
}

// A run that stops over a finished ticket's merge with nothing else running, so with no hold
// before it, shows that ticket blocked rather than stopped.
func TestAStopOverAMergeShowsTheTicketBlocked(t *testing.T) {
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
		dispatch.Event{Kind: dispatch.EvStop, Ticket: "k-1", Detail: "MERGE_FAILED", Blocked: "does not fast-forward onto batch",
			Text: "MERGE_FAILED: wt/k-1 does not fast-forward onto batch; …"})
	final := ansi.Strip(printedEnd(m, 80))
	if row := rowLine(final, "k-1"); !strings.Contains(row, "■ blocked") ||
		!strings.Contains(row, "does not fast-forward onto batch") || strings.Contains(final, "■ stopped") {
		t.Errorf("the summary should show k-1 blocked:\n%s", final)
	}
	if got := needsYouIn(final); got != "? 1" {
		t.Errorf("Needs you %q, want ? 1:\n%s", got, final)
	}
}

// A merge conflict sets its ticket aside blocked, naming the files, where a failing check leaves
// it for review.
func TestAMergeConflictShowsTheTicketBlocked(t *testing.T) {
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Undo a property edit"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1", Aside: true, Detail: "conflicts with batch",
			Blocked: "merge conflict in shared.txt", Text: "  MERGE_CONFLICT: k-1 closed, but wt/k-1 conflicts with batch, …"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-2", Aside: true, Detail: "checks failed",
			Text: "  CHECKS_FAILED: k-2 closed, but 'scripts/check.sh' fails on wt/k-2 rebased onto batch; …"})
	v := ansi.Strip(m.View())
	if row := rowLine(v, "k-1"); !strings.Contains(row, "■ blocked") || !strings.Contains(row, "merge conflict in shared.txt") {
		t.Errorf("k-1 should show blocked by its conflict:\n%s", v)
	}
	if row := rowLine(v, "k-2"); !strings.Contains(row, "! review") || !strings.Contains(row, "checks failed") {
		t.Errorf("k-2 should show for review:\n%s", v)
	}
	if got := needsYouIn(v); got != "? 1" {
		t.Errorf("Needs you %q, want ? 1 for k-1 alone:\n%s", got, v)
	}
}

// A hold over a ticket that isn't about its merge (a worker paused) shows the ticket stopped, and
// doesn't count it under Needs you.
func TestAHoldThatIsNotAboutAMergeShowsTheTicketStopped(t *testing.T) {
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
		dispatch.Event{Kind: dispatch.EvHold, Ticket: "k-1", Text: "HOLD: PAUSED: k-1 still in_progress after 12m; …"})
	v := ansi.Strip(m.View())
	if row := rowLine(v, "k-1"); !strings.Contains(row, "■ stopped") || strings.Contains(v, "blocked") {
		t.Errorf("k-1 should show stopped:\n%s", v)
	}
	if got := needsYouIn(v); got != "0" {
		t.Errorf("Needs you %q, want 0:\n%s", got, v)
	}
}

// Needs you counts the questions and the blocked merges together; the narrow line says the same.
func TestNeedsYouCountsQuestionsAndBlockedMerges(t *testing.T) {
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Repeat a timeline"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-2", Aside: true, Detail: "conflicts with batch",
			Blocked: "merge conflict in shared.txt", Text: "  MERGE_CONFLICT: …"})
	if got := needsYouIn(ansi.Strip(m.View())); got != "? 2" {
		t.Errorf("Needs you %q, want ? 2:\n%s", got, ansi.Strip(m.View()))
	}
	if line := ansi.Strip(m.statsLine(80)); !strings.Contains(line, "? 2") {
		t.Errorf("the totals' line %q should count 2", line)
	}
}
