package dispatch

import (
	"bytes"
	"cmp"
	"slices"
	"strings"
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
// one after the other instead of side by side: the higher-priority ticket (then the older one, then
// the lower ID by compareIDs) goes first. Two tickets are linked when they name the same function,
// or work on the same small file and aren't compared on it by function, as the loop's footprints
// are (sharedFile): a ticket works on a file it names and on one the other names that mentions a
// function it names, and two tickets that both name functions a file mentions, none the same, work
// in different places in it. read gives a repository file's contents, for its line count and the
// functions it mentions; a file it can't read (one that doesn't exist yet) is small and mentions
// none. Area labels, predicted files, and the files the check command (check) names and specs under
// .orchestra/specs/, unless a ticket's files metadata lists them, don't link tickets, and a pair
// already ordered by existing links, directly or through other tickets, gets none; each ticket is
// linked to the nearest one before it first, so tickets touching one function form a chain.
func PlanLinks(open []Ticket, existing []Link, tracked []string, check string,
	read func(file string) ([]byte, error)) []Link {
	ts := slices.Clone(open)
	slices.SortStableFunc(ts, func(a, b Ticket) int {
		return cmp.Or(
			cmp.Compare(PriorityOf(a), PriorityOf(b)),
			cmp.Compare(a.CreatedAt, b.CreatedAt),
			compareIDs(a.ID, b.ID),
		)
	})
	repo := newRepoFiles(tracked, check)
	repo.read = read
	fps := make([]Footprint, len(ts))
	for i, t := range ts {
		// A predicted footprint is a guess: good enough to keep two tickets apart for a while, not
		// to order them for good.
		if fps[i] = ticketFootprint(t, repo); fps[i].Predicted {
			fps[i] = Footprint{}
		}
	}
	small := map[string]bool{}
	isSmall := func(p string) bool {
		s, ok := small[p]
		if !ok {
			b, _ := read(p) // a file that can't be read counts as one not written yet
			s = bytes.Count(b, []byte("\n")) <= SmallFileLines
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
			what := planShared(fps[i], fps[j], repo.mentions, isSmall)
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

// compareIDs orders ticket IDs as text, except that the numbers between their dots, the subticket
// numbers bd gives an epic's children, compare as numbers: e.2 before e.10. bd's created_at counts
// whole seconds, so tickets filed together often tie on it. A number goes before text in the same
// place, and IDs that differ only in leading zeros fall back to text, so the order is total.
func compareIDs(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(as), len(bs)) {
		x, xNum := idNumber(as[i])
		y, yNum := idNumber(bs[i])
		var c int
		switch {
		case xNum && yNum:
			c = cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y))
		case xNum:
			c = -1
		case yNum:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Or(cmp.Compare(len(as), len(bs)), strings.Compare(a, b))
}

// idNumber returns a part of an ID without its leading zeros and true when it is a number, or the
// part unchanged and false.
func idNumber(part string) (string, bool) {
	if part == "" || strings.Trim(part, "0123456789") != "" {
		return part, false
	}
	return strings.TrimLeft(part, "0"), true
}

// planShared returns what two tickets' footprints share closely enough to order them, or "": a
// function both name, else a small file both work on that they aren't compared on by function (in
// reports which functions a file mentions).
func planShared(f, g Footprint, in func(file, fn string) bool, small func(string) bool) string {
	if x := sharedFunc(f, g); x != "" {
		return x
	}
	return sharedFile(f, g, nil, in, small)
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
