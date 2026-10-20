package dispatch

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func planTicket(id string, prio int, created, text string) Ticket {
	return Ticket{ID: id, Title: id, Status: "open", Priority: &prio, CreatedAt: created, Description: text}
}

// linesOf gives every file this many empty lines, except those listed.
func linesOf(def int, files map[string]int) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		n, ok := files[p]
		if !ok {
			n = def
		}
		return []byte(strings.Repeat("\n", n)), nil
	}
}

// sourceOf gives the files listed these contents, and every other one 50 empty lines.
func sourceOf(files map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if c, ok := files[p]; ok {
			return []byte(c), nil
		}
		return []byte(strings.Repeat("\n", 50)), nil
	}
}

func linkList(links []Link) []string {
	var l []string
	for _, k := range links {
		l = append(l, fmt.Sprintf("%s>%s:%s", k.Blocker, k.Blocked, k.Why))
	}
	return l
}

func TestPlanLinksTheHigherPriorityTicketFirst(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("low", 3, "2026-09-01T00:00:00Z", "Retry in `Loop.merge` (internal/dispatch/merge.go)."),
		planTicket("high", 1, "2026-09-30T00:00:00Z", "Log each rebase in Loop.merge()."),
		planTicket("other", 0, "2026-09-01T00:00:00Z", "Change refreshBranch() in internal/dispatch/merge.go."),
	}
	// low and other work in merge.go on different functions it mentions.
	merge := sourceOf(map[string]string{"internal/dispatch/merge.go": "func (o *Loop) merge() { o.refreshBranch() }\n"})
	got := linkList(PlanLinks(open, nil, trackedHere, "", merge, nil))
	if want := []string{"high>low:Loop.merge"}; !slices.Equal(got, want) {
		t.Errorf("links %q, want %q", got, want)
	}

	// At the same priority, the older ticket goes first.
	open[0].Priority, open[1].Priority = open[1].Priority, open[0].Priority
	*open[0].Priority = 2
	*open[1].Priority = 2
	if got := linkList(PlanLinks(open, nil, trackedHere, "", merge, nil)); !slices.Equal(got, []string{"low>high:Loop.merge"}) {
		t.Errorf("same priority: links %q, want the older first", got)
	}
}

func TestPlanLinksSmallFilesOnly(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("a", 1, "", "Reword the intro in README.md."),
		planTicket("b", 2, "", "Add a section to README.md and internal/dispatch/loop.go."),
		planTicket("c", 2, "", "Tidy internal/dispatch/loop.go."),
		planTicket("d", 2, "", "Add internal/dispatch/plan.go."),
		planTicket("e", 3, "", "Test internal/dispatch/plan.go."),
	}
	got := linkList(PlanLinks(open, nil, trackedHere, "", linesOf(50, map[string]int{"internal/dispatch/loop.go": 900, "internal/dispatch/plan.go": 0}), nil))
	// loop.go is large: b and c may work in different places. plan.go is new, so small.
	if want := []string{"a>b:README.md", "d>e:internal/dispatch/plan.go"}; !slices.Equal(got, want) {
		t.Errorf("links %q, want %q", got, want)
	}
}

func TestPlanLinksByFunctionWhenBothNameOne(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("a", 1, "", "Change `waitSettled` in README.md."),
		planTicket("b", 2, "", "Change refreshBranch() in README.md."),
		planTicket("c", 2, "", "Reword README.md."),
	}
	readme := sourceOf(map[string]string{"README.md": "Call waitSettled, then refreshBranch.\n"})
	got := linkList(PlanLinks(open, nil, trackedHere, "", readme, nil))
	// a and b name different functions of one file, so they aren't linked. c names none, so the file
	// decides for it: it waits for b, the nearest ticket before it, and for a, which b doesn't.
	if want := []string{"b>c:README.md", "a>c:README.md"}; !slices.Equal(got, want) {
		t.Errorf("links %q, want %q", got, want)
	}
}

func TestPlanLinksChainAndSkipOrderedPairs(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("a", 1, "", "Change `Loop.merge`."),
		planTicket("b", 2, "", "Change `Loop.merge`."),
		planTicket("c", 3, "", "Change `Loop.merge`."),
	}
	got := linkList(PlanLinks(open, nil, trackedHere, "", linesOf(50, nil), nil))
	if want := []string{"a>b:Loop.merge", "b>c:Loop.merge"}; !slices.Equal(got, want) {
		t.Errorf("three on one function: links %q, want a chain %q", got, want)
	}

	// Already ordered, either way round or through another ticket: nothing to add.
	existing := []Link{{Blocker: "c", Blocked: "x"}, {Blocker: "x", Blocked: "b"}, {Blocker: "a", Blocked: "c"}}
	if got := PlanLinks(open, existing, trackedHere, "", linesOf(50, nil), nil); len(got) != 0 {
		t.Errorf("already ordered: links %q, want none", linkList(got))
	}
}

func TestPlanLinksIgnoresAreasAndTicketsNamingNothing(t *testing.T) {
	t.Parallel()
	a := planTicket("a", 1, "", "Make it faster.")
	b := planTicket("b", 2, "", "Make it nicer.")
	a.Labels, b.Labels = []string{"area:tui"}, []string{"area:tui"}
	if got := PlanLinks([]Ticket{a, b}, nil, trackedHere, "", linesOf(50, nil), nil); len(got) != 0 {
		t.Errorf("links %q, want none", linkList(got))
	}
}

// A predicted footprint is a guess: it keeps tickets apart while they run, but doesn't order them.
func TestPlanLinksIgnoresPredictedFiles(t *testing.T) {
	t.Parallel()
	a := planTicket("a", 1, "", "Make it faster.")
	b := planTicket("b", 2, "", "Change internal/dispatch/run.go.")
	a.Metadata = []byte(`{"predicted_files": "internal/dispatch/run.go"}`)
	if got := PlanLinks([]Ticket{a, b}, nil, trackedHere, "", linesOf(50, nil), nil); len(got) != 0 {
		t.Errorf("links %q, want none", linkList(got))
	}
}
