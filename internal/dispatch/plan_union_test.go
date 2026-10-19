package dispatch

import (
	"slices"
	"testing"
)

// A changelog git merges by union links no tickets, however short: both tickets' lines are kept. A
// file the union check doesn't name, and every file without one, still does.
func TestPlanLinksLeaveOutUnionMergedFiles(t *testing.T) {
	open := []Ticket{
		planTicket("a", 1, "", "Add a CHANGELOG.md entry for the retry."),
		planTicket("b", 2, "", "Add a CHANGELOG.md entry for the dashboard."),
	}
	var asked []string
	union := func(f string) bool {
		asked = append(asked, f)
		return f == "CHANGELOG.md"
	}
	if got := PlanLinks(open, nil, trackedHere, "", linesOf(50, nil), union); len(got) != 0 {
		t.Errorf("a changelog merged by union: links %q, want none", linkList(got))
	}
	if !slices.Contains(asked, "CHANGELOG.md") {
		t.Errorf("the union check was asked about %q, not the changelog", asked)
	}

	want := []string{"a>b:CHANGELOG.md"}
	if got := linkList(PlanLinks(open, nil, trackedHere, "", linesOf(50, nil), nil)); !slices.Equal(got, want) {
		t.Errorf("no union check: links %q, want %q", got, want)
	}
	readme := func(f string) bool { return f == "README.md" }
	if got := linkList(PlanLinks(open, nil, trackedHere, "", linesOf(50, nil), readme)); !slices.Equal(got, want) {
		t.Errorf("only README.md merged by union: links %q, want %q", got, want)
	}
}
