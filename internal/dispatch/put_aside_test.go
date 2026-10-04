package dispatch

import (
	"context"
	"path/filepath"
	"testing"
)

// asideLoop is a loop with notes as its tracker, for putAside alone.
func asideLoop(t *testing.T, notes Notes) (*Loop, *runSink) {
	t.Helper()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &runSink{held: make(chan struct{})}
	o := New(Config{Base: "main"}, log, "", Deps{Notes: notes})
	o.SetSink(sink)
	return o, sink
}

var asideA = asideTexts{note: "the note", reason: "the reason", detail: "the detail", deferred: "  DEFERRED_LINE",
	failed: "A went wrong, and bd could not defer it", then: "see to it"}

func TestPutAsideDefersAndReportsIt(t *testing.T) {
	t.Parallel()
	b := newFakeBeads()
	b.add("A", "a", 2)
	o, sink := asideLoop(t, b)
	if !o.putAside(context.Background(), Ticket{ID: "A", Title: "a"}, asideA) {
		t.Error("putAside reported A not deferred")
	}
	if got := b.notes["A"]; !equal(got, []string{"the note", "deferred: the reason"}) {
		t.Errorf("A's notes: %q", got)
	}
	want := Event{Kind: EvDeferred, Ticket: "A", Title: "a", Detail: "the detail", Text: "  DEFERRED_LINE"}
	if len(sink.events) != 1 || !sameEvent(sink.events[0], want) {
		t.Errorf("events %+v, want only %+v", sink.events, want)
	}
	if got := o.setAside(); !equal(got, []string{"A"}) {
		t.Errorf("set aside: %v", got)
	}
}

func TestPutAsideWarnsWhenBdCannotDefer(t *testing.T) {
	t.Parallel()
	// bd still lists the ticket as ready: it is kept out of the run all the same, and the warning
	// leaves it set aside for review, as the dashboard and the run's count of those set aside see it.
	b := newFakeBeads()
	b.add("A", "a", 2)
	o, sink := asideLoop(t, deferFails{b})
	if o.putAside(context.Background(), Ticket{ID: "A", Title: "a"}, asideA) {
		t.Error("putAside reported A deferred")
	}
	if got := b.notes["A"]; !equal(got, []string{"the note"}) {
		t.Errorf("A's notes: %q", got)
	}
	want := Event{Kind: EvWarn, Ticket: "A", Aside: true, Detail: "the detail", Text: "  DEFER_FAILED: " +
		"A went wrong, and bd could not defer it: database is locked; kept out of this run, see to it"}
	if len(sink.events) != 1 || !sameEvent(sink.events[0], want) {
		t.Errorf("events %+v, want only %+v", sink.events, want)
	}
	if got := o.setAside(); !equal(got, []string{"A"}) {
		t.Errorf("set aside: %v", got)
	}
}

// sameEvent compares the fields putAside sets, leaving out the time.
func sameEvent(got, want Event) bool {
	return got.Kind == want.Kind && got.Ticket == want.Ticket && got.Title == want.Title &&
		got.Detail == want.Detail && got.Text == want.Text && got.Aside == want.Aside
}
