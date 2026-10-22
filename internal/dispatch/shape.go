package dispatch

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// HubHolds is how many open tickets must wait directly on a ticket for its shape to name it a hub.
const HubHolds = 3

// LongChain is the longest chain of tickets, one waiting on the next, that a breakdown's tree may have
// before its check warns.
const LongChain = 3

// Shape is how the blocks links among an epic's open tickets let them run: its tickets, its longest
// chain of tickets each waiting on the one before, and its hubs. Tickets over chain length is the most
// workers its links let run on average, the tickets taken as equal (Width).
type Shape struct {
	Epic    string
	Tickets int
	Chain   []string // the longest chain, its first blocker first; empty when there are no tickets
	Hubs    []Hub    // the tickets HubHolds or more of the epic's open tickets wait on, the most held first
}

// Hub is a ticket of an epic that other open tickets of it wait on directly: Holds of them.
type Hub struct {
	ID    string
	Holds int
}

// ShapeOf is the shape of epic, whose tickets are tickets: the epics and closed tickets among them are
// left out, as are links from or to a ticket that isn't one of the rest. A blocks cycle is followed
// once round, not looped.
func ShapeOf(epic string, tickets []Ticket, links []Link) Shape {
	in := map[string]bool{}
	var ids []string
	for _, t := range tickets {
		if t.IssueType == "epic" || t.Status == StatusClosed || in[t.ID] {
			continue
		}
		in[t.ID] = true
		ids = append(ids, t.ID)
	}
	slices.SortFunc(ids, compareIDs)
	after := map[string][]string{} // blocker → the tickets that wait on it, each once
	for _, l := range links {
		if in[l.Blocker] && in[l.Blocked] && l.Blocker != l.Blocked && !slices.Contains(after[l.Blocker], l.Blocked) {
			after[l.Blocker] = append(after[l.Blocker], l.Blocked)
		}
	}
	s := Shape{Epic: epic, Tickets: len(ids)}
	for _, id := range ids {
		slices.SortFunc(after[id], compareIDs)
		if n := len(after[id]); n >= HubHolds {
			s.Hubs = append(s.Hubs, Hub{ID: id, Holds: n})
		}
	}
	slices.SortStableFunc(s.Hubs, func(a, b Hub) int { return cmp.Compare(b.Holds, a.Holds) })
	s.Chain = longestChain(ids, after)
	return s
}

// longestChain is the longest path through the tickets ids along after, the first in ids' order of
// those as long. A link back to a ticket already on the path is not followed, so a cycle ends it.
func longestChain(ids []string, after map[string][]string) []string {
	depth := map[string]int{} // the tickets on the longest path from a ticket, itself included
	next := map[string]string{}
	onPath := map[string]bool{}
	var walk func(id string) int
	walk = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		onPath[id] = true
		best := 0
		for _, n := range after[id] {
			if onPath[n] {
				continue
			}
			if d := walk(n); d > best {
				best, next[id] = d, n
			}
		}
		onPath[id] = false
		depth[id] = best + 1
		return depth[id]
	}
	start, most := "", 0
	for _, id := range ids {
		if d := walk(id); d > most {
			start, most = id, d
		}
	}
	var chain []string
	for id := start; id != ""; id = next[id] {
		chain = append(chain, id)
	}
	return chain
}

// EpicShapes is the shape of each epic that open tickets name as their parent, by its open tickets
// (those that name it) and the blocks links among them, in the order of the epics' IDs.
func EpicShapes(open []Ticket, links []Link) []Shape {
	byEpic := map[string][]Ticket{}
	for _, t := range open {
		if t.Parent != "" {
			byEpic[t.Parent] = append(byEpic[t.Parent], t)
		}
	}
	var shapes []Shape
	for epic, tickets := range byEpic {
		if s := ShapeOf(epic, tickets, links); s.Tickets > 0 {
			shapes = append(shapes, s)
		}
	}
	slices.SortFunc(shapes, func(a, b Shape) int { return compareIDs(a.Epic, b.Epic) })
	return shapes
}

// Width is the most tickets the shape's links let run at a time on average: its tickets over its
// longest chain; 0 for no tickets.
func (s Shape) Width() float64 {
	if len(s.Chain) == 0 {
		return 0
	}
	return float64(s.Tickets) / float64(len(s.Chain))
}

// String is the shape in a line: "orchestra-aq0o: 7 tickets, longest chain 2 (aq0o.1 > aq0o.3), at
// most 3.5 at a time; orchestra-aq0o.1 holds 6". The chain's tickets go without the project's prefix
// the epic's ID has.
func (s Shape) String() string {
	tickets := strconv.Itoa(s.Tickets) + " tickets"
	if s.Tickets == 1 {
		tickets = "1 ticket"
	}
	prefix := ""
	if i := strings.Index(s.Epic, "-"); i >= 0 {
		prefix = s.Epic[:i+1]
	}
	chain := make([]string, len(s.Chain))
	for i, id := range s.Chain {
		chain[i] = strings.TrimPrefix(id, prefix)
	}
	line := fmt.Sprintf("%s: %s, longest chain %d (%s), at most %s at a time", s.Epic, tickets, len(s.Chain),
		strings.Join(chain, " > "), FormatWidth(s.Width()))
	if len(s.Hubs) == 0 {
		return line
	}
	hubs := make([]string, len(s.Hubs))
	for i, h := range s.Hubs {
		hubs[i] = fmt.Sprintf("%s holds %d", h.ID, h.Holds)
	}
	return line + "; " + strings.Join(hubs, ", ")
}

// FormatWidth gives a shape's width to one decimal, without a trailing ".0": "3.5", "7".
func FormatWidth(w float64) string {
	return strconv.FormatFloat(math.Round(w*10)/10, 'f', -1, 64)
}
