package dispatch

import (
	"testing"
	"time"
)

// Running lists the tickets started at the same time by ticket, on every call: map order would
// reorder them from one call to the next. An older ticket still comes first.
func TestRunningOrdersTicketsStartedTogetherByID(t *testing.T) {
	t.Parallel()
	var o Loop
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"e", "b", "d", "a", "c", "f", "h", "g"} {
		o.setActive(Status{Ticket: id, Started: at})
	}
	o.setActive(Status{Ticket: "z", Started: at.Add(-time.Minute)})
	want := []string{"z", "a", "b", "c", "d", "e", "f", "g", "h"}
	for range 50 {
		var got []string
		for _, st := range o.Running() {
			got = append(got, st.Ticket)
		}
		if !equal(got, want) {
			t.Fatalf("Running: %v, want %v", got, want)
		}
	}
}
