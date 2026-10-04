package dispatch

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestPlanLinksSameSecondTicketsByIDNumber(t *testing.T) {
	// Filed in one burst: same priority, same created_at second. Text order would put e.10 first.
	open := []Ticket{
		planTicket("e.10", 2, "2026-10-04T09:00:00Z", "Retry in `Loop.merge` (internal/dispatch/merge.go)."),
		planTicket("e.2", 2, "2026-10-04T09:00:00Z", "Log each rebase in Loop.merge()."),
	}
	got := linkList(PlanLinks(open, nil, trackedHere, "", linesOf(50, nil)))
	if want := []string{"e.2>e.10:Loop.merge"}; !slices.Equal(got, want) {
		t.Errorf("links %q, want %q", got, want)
	}
}

func TestCompareIDsOrdersSubticketNumbersAsNumbers(t *testing.T) {
	want := []string{
		"e", "e.1", "e.1.3", "e.1.20", "e.2", "e.9", "e.10", "e.x",
		"orchestra-4wb", "orchestra-4wb.2", "orchestra-4wb.12", "orchestra-zdl",
	}
	for range 20 {
		got := slices.Clone(want)
		rand.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		slices.SortFunc(got, compareIDs)
		if !slices.Equal(got, want) {
			t.Fatalf("sorted %q, want %q", got, want)
		}
	}
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"e.2", "e.10", -1},
		{"e.10", "e.2", 1},
		{"e.2", "e.2", 0},
		{"e.02", "e.2", -1}, // equal as numbers: text breaks the tie
		{"e.2", "e.02", 1},
		{"e.9", "e.1a", -1}, // a number before text
		{"e.1a", "e.10", 1},
		{"a10", "a9", -1}, // hash IDs stay text
	} {
		if got := compareIDs(c.a, c.b); got != c.want {
			t.Errorf("compareIDs(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
