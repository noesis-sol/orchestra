package dispatch

import (
	"cmp"
	"slices"
)

// Link is a blocks link between two tickets: Blocked waits for Blocker (bd dep add <Blocked>
// <Blocker>). Why is what both touch, for a proposed link.
type Link struct {
	Blocker, Blocked string
	Why              string
}

// SmallFileLines is the most lines a file can have for two tickets naming it, and no function in
// common, to be ordered: in a larger file they likely work in different places.
const SmallFileLines = 200

// PlanLinks proposes blocks links that order the open tickets touching the same code, so they run
// one after the other instead of side by side: the higher-priority ticket (then the older one)
// goes first. Two tickets are linked when both name functions and one of them is the same, or,
// when either names none, they name the same small file (lines gives a repository file's line
// count, 0 for one that doesn't exist yet). Area labels alone don't link tickets, and a pair
// already ordered by existing links, directly or through other tickets, gets none; each ticket is
// linked to the nearest one before it first, so tickets touching one function form a chain.
func PlanLinks(open []Ticket, existing []Link, tracked []string, lines func(path string) int) []Link {
	ts := slices.Clone(open)
	slices.SortStableFunc(ts, func(a, b Ticket) int {
		return cmp.Or(cmp.Compare(PriorityOf(a), PriorityOf(b)), cmp.Compare(a.CreatedAt, b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	repo := newRepoFiles(tracked)
	fps := make([]Footprint, len(ts))
	for i, t := range ts {
		fps[i] = ticketFootprint(t, repo)
	}
	small := map[string]bool{}
	isSmall := func(p string) bool {
		s, ok := small[p]
		if !ok {
			s = lines(p) <= SmallFileLines
			small[p] = s
		}
		return s
	}
	after := map[string][]string{} // blocker → the tickets it blocks
	for _, l := range existing {
		after[l.Blocker] = append(after[l.Blocker], l.Blocked)
	}
	var links []Link
	for j := range ts {
		for i := j - 1; i >= 0; i-- {
			a, b := ts[i].ID, ts[j].ID
			what := planShared(fps[i], fps[j], isSmall)
			if what == "" || reaches(after, a, b) || reaches(after, b, a) {
				continue
			}
			after[a] = append(after[a], b)
			links = append(links, Link{Blocker: a, Blocked: b, Why: what})
		}
	}
	return links
}

// PriorityOf is the ticket's priority, 0 the highest; 9 when it has none.
func PriorityOf(t Ticket) int {
	if t.Priority == nil {
		return 9
	}
	return *t.Priority
}

// planShared returns what two tickets' footprints share closely enough to order them, or "": a
// function when both name functions, else a small file both name.
func planShared(f, g Footprint, small func(string) bool) string {
	if len(f.Funcs) > 0 && len(g.Funcs) > 0 {
		for _, x := range f.Funcs {
			for _, y := range g.Funcs {
				if funcsMeet(x, y) {
					return x
				}
			}
		}
		return ""
	}
	for _, p := range f.Files {
		if slices.Contains(g.Files, p) && small(p) {
			return p
		}
	}
	return ""
}

// reaches reports whether ticket to already waits for from, through the links in after.
func reaches(after map[string][]string, from, to string) bool {
	seen := map[string]bool{from: true}
	for next := []string{from}; len(next) > 0; {
		id := next[len(next)-1]
		next = next[:len(next)-1]
		for _, n := range after[id] {
			if n == to {
				return true
			}
			if !seen[n] {
				seen[n] = true
				next = append(next, n)
			}
		}
	}
	return false
}
