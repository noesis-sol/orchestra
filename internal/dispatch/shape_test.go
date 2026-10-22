package dispatch

import (
	"reflect"
	"testing"
)

// shapeTicket is an open task of epic.
func shapeTicket(id, epic string) Ticket {
	return Ticket{ID: id, IssueType: "task", Status: StatusOpen, Parent: epic}
}

// waitsOn links each pair of IDs, the blocked ticket first: waitsOn("e-1.2", "e-1.1") has e-1.2 wait on e-1.1.
func waitsOn(pairs ...string) []Link {
	var links []Link
	for i := 0; i+1 < len(pairs); i += 2 {
		links = append(links, Link{Blocked: pairs[i], Blocker: pairs[i+1]})
	}
	return links
}

// A chain's tickets each wait on the one before: one worker at a time.
func TestShapeOfAChain(t *testing.T) {
	t.Parallel()
	tickets := []Ticket{shapeTicket("p-e.3", "p-e"), shapeTicket("p-e.1", "p-e"), shapeTicket("p-e.2", "p-e"),
		shapeTicket("p-e.4", "p-e")}
	s := ShapeOf("p-e", tickets, waitsOn("p-e.2", "p-e.1", "p-e.3", "p-e.2", "p-e.4", "p-e.3"))
	want := Shape{Epic: "p-e", Tickets: 4, Chain: []string{"p-e.1", "p-e.2", "p-e.3", "p-e.4"}}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("shape %+v, want %+v", s, want)
	}
	if got, want := s.String(), "p-e: 4 tickets, longest chain 4 (e.1 > e.2 > e.3 > e.4), at most 1 at a time"; got != want {
		t.Errorf("line %q, want %q", got, want)
	}
}

// A hub is a ticket three or more others wait on directly; the shape names it with how many.
func TestShapeOfAHub(t *testing.T) {
	t.Parallel()
	var tickets []Ticket
	for _, id := range []string{"o-aq0o.1", "o-aq0o.2", "o-aq0o.3", "o-aq0o.4", "o-aq0o.5", "o-aq0o.6", "o-aq0o.7"} {
		tickets = append(tickets, shapeTicket(id, "o-aq0o"))
	}
	links := waitsOn("o-aq0o.3", "o-aq0o.1", "o-aq0o.2", "o-aq0o.1", "o-aq0o.4", "o-aq0o.1", "o-aq0o.5", "o-aq0o.1",
		"o-aq0o.6", "o-aq0o.1", "o-aq0o.7", "o-aq0o.1", "o-aq0o.7", "o-aq0o.1") // the last one twice
	s := ShapeOf("o-aq0o", tickets, links)
	if want := []Hub{{ID: "o-aq0o.1", Holds: 6}}; !reflect.DeepEqual(s.Hubs, want) {
		t.Errorf("hubs %+v, want %+v", s.Hubs, want)
	}
	want := "o-aq0o: 7 tickets, longest chain 2 (aq0o.1 > aq0o.2), at most 3.5 at a time; o-aq0o.1 holds 6"
	if got := s.String(); got != want {
		t.Errorf("line %q, want %q", got, want)
	}

	// Two tickets each wait on by two aren't hubs.
	s = ShapeOf("o-aq0o", tickets, waitsOn("o-aq0o.3", "o-aq0o.1", "o-aq0o.2", "o-aq0o.1", "o-aq0o.5", "o-aq0o.4",
		"o-aq0o.6", "o-aq0o.4"))
	if len(s.Hubs) != 0 {
		t.Errorf("hubs %+v, want none", s.Hubs)
	}
}

// An epic whose tickets wait on none of each other runs them all at once.
func TestShapeOfAWideEpic(t *testing.T) {
	t.Parallel()
	tickets := []Ticket{shapeTicket("p-w.1", "p-w"), shapeTicket("p-w.2", "p-w"), shapeTicket("p-w.3", "p-w")}
	s := ShapeOf("p-w", tickets, nil)
	if s.Tickets != 3 || len(s.Chain) != 1 || s.Width() != 3 {
		t.Errorf("shape %+v, width %v, want 3 tickets, a chain of 1, width 3", s, s.Width())
	}
	if got, want := s.String(), "p-w: 3 tickets, longest chain 1 (w.1), at most 3 at a time"; got != want {
		t.Errorf("line %q, want %q", got, want)
	}
	if got := ShapeOf("p-x", nil, nil); got.Tickets != 0 || len(got.Chain) != 0 || got.Width() != 0 {
		t.Errorf("no tickets: %+v", got)
	}
}

// Closed tickets, epics and tickets outside the epic are left out, and so are the links to them: they
// neither count nor lengthen a chain.
func TestShapeLeavesOutClosedAndOutsideTickets(t *testing.T) {
	t.Parallel()
	closed := shapeTicket("p-e.3", "p-e")
	closed.Status = StatusClosed
	child := shapeTicket("p-e.4", "p-e")
	child.IssueType = "epic"
	open := []Ticket{shapeTicket("p-e.1", "p-e"), shapeTicket("p-e.2", "p-e"), closed, child,
		shapeTicket("p-f.1", "p-f"), shapeTicket("p-f.2", "p-f"), shapeTicket("p-loose", "")}
	links := waitsOn("p-e.2", "p-e.3", "p-e.3", "p-e.1", "p-e.1", "p-f.1", "p-f.2", "p-f.1", "p-e.2", "p-loose")
	shapes := EpicShapes(open, links)
	want := []Shape{
		{Epic: "p-e", Tickets: 2, Chain: []string{"p-e.1"}},
		{Epic: "p-f", Tickets: 2, Chain: []string{"p-f.1", "p-f.2"}},
	}
	if !reflect.DeepEqual(shapes, want) {
		t.Errorf("shapes %+v, want %+v", shapes, want)
	}
}

// A blocks cycle is followed once round: the chain ends where it would come back.
func TestShapeOfACycleDoesntLoop(t *testing.T) {
	t.Parallel()
	tickets := []Ticket{shapeTicket("p-c.1", "p-c"), shapeTicket("p-c.2", "p-c"), shapeTicket("p-c.3", "p-c")}
	s := ShapeOf("p-c", tickets, waitsOn("p-c.2", "p-c.1", "p-c.3", "p-c.2", "p-c.1", "p-c.3", "p-c.1", "p-c.1"))
	if want := []string{"p-c.1", "p-c.2", "p-c.3"}; !reflect.DeepEqual(s.Chain, want) {
		t.Errorf("chain %q, want %q", s.Chain, want)
	}
}

// The width goes to one decimal.
func TestFormatWidth(t *testing.T) {
	t.Parallel()
	for w, want := range map[float64]string{3.5: "3.5", 7: "7", 10.0 / 3: "3.3", 1.25: "1.3", 0: "0"} {
		if got := FormatWidth(w); got != want {
			t.Errorf("FormatWidth(%v) = %q, want %q", w, got, want)
		}
	}
}
