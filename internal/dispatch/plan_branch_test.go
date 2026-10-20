package dispatch

import (
	"slices"
	"testing"
)

// A ticket whose branch changes files, reopened or carried over, is planned by them, as the loop
// footprints it: it is linked with a ticket over a file its branch changes that its text never names,
// and not over one only its text names. A ticket whose branch changes nothing goes by its text.
func TestPlanLinksGoByABranchsFiles(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("a", 1, "", "Split internal/dispatch/loop.go in two."),
		planTicket("b", 2, "", "Speed up picking. Reopened: internal/dispatch/loop.go calls it twice."),
		planTicket("c", 3, "", "Redraw internal/tui/run.go."),
	}
	want := []string{"a>b:internal/dispatch/loop.go"}
	if got := linkList(PlanLinks(open, nil, trackedHere, "", linesOf(50, nil), nil, nil)); !slices.Equal(got, want) {
		t.Errorf("no branches: links %q, want %q", got, want)
	}

	branches := map[string][]string{"b": {"internal/tui/run.go"}, "c": nil}
	want = []string{"b>c:internal/tui/run.go"}
	if got := linkList(PlanLinks(open, nil, trackedHere, "", linesOf(50, nil), nil, branches)); !slices.Equal(got, want) {
		t.Errorf("b's branch changes internal/tui/run.go: links %q, want %q", got, want)
	}

	// The files git merges by union are left out of a branch's files too.
	open = []Ticket{
		planTicket("a", 1, "", "Add a CHANGELOG.md entry for the retry."),
		planTicket("b", 2, "", "Add the dashboard."),
	}
	branches = map[string][]string{"b": {"CHANGELOG.md"}}
	union := func(f string) bool { return f == "CHANGELOG.md" }
	if got := PlanLinks(open, nil, trackedHere, "", linesOf(50, nil), union, branches); len(got) != 0 {
		t.Errorf("b's branch changes only a changelog merged by union: links %q, want none", linkList(got))
	}
}
