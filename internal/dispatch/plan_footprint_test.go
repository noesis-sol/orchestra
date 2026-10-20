package dispatch

import (
	"slices"
	"strings"
	"testing"
)

// A ticket naming only functions works on the files that mention them, so it is ordered with one
// naming such a file, when the file is small.
func TestPlanLinksFindFunctionsInFiles(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("a", 1, "", "Retry in `Loop.merge`."),
		planTicket("b", 2, "", "Tidy internal/dispatch/merge.go."),
		planTicket("c", 3, "", "Speed up pickNext()."),
	}
	merge := "func (o *Loop) merge() {}\n"
	sources := sourceOf(map[string]string{"internal/dispatch/merge.go": merge})
	got := linkList(PlanLinks(open, nil, trackedHere, "", sources, nil, nil))
	// merge.go doesn't mention pickNext: c works elsewhere.
	if want := []string{"a>b:internal/dispatch/merge.go"}; !slices.Equal(got, want) {
		t.Errorf("links %q, want %q", got, want)
	}

	large := sourceOf(map[string]string{"internal/dispatch/merge.go": merge + strings.Repeat("\n", SmallFileLines)})
	if got := PlanLinks(open, nil, trackedHere, "", large, nil, nil); len(got) != 0 {
		t.Errorf("a large file: links %q, want none", linkList(got))
	}
}

// Tickets that both name functions are compared on a file by function only when both name functions
// it mentions: a stray name elsewhere (a test's helper) doesn't keep them apart.
func TestPlanLinksCompareByFunctionPerFile(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("a", 1, "", "Reword README.md, as `waitSettled` in the tests expects."),
		planTicket("b", 2, "", "Describe refreshBranch() in README.md."),
	}
	readme := sourceOf(map[string]string{"README.md": "refreshBranch brings the branch up to date.\n"})
	got := linkList(PlanLinks(open, nil, trackedHere, "", readme, nil, nil))
	if want := []string{"a>b:README.md"}; !slices.Equal(got, want) {
		t.Errorf("links %q, want %q", got, want)
	}

	both := sourceOf(map[string]string{"README.md": "refreshBranch, then waitSettled.\n"})
	if got := PlanLinks(open, nil, trackedHere, "", both, nil, nil); len(got) != 0 {
		t.Errorf("both functions in the file: links %q, want none", linkList(got))
	}
}
