package tui

import "testing"

func TestConcurrencyOptionsHaveDistinctNotes(t *testing.T) {
	if len(concurrencyOptions()) != len(concurrencyNotes)+1 {
		t.Fatalf("%d options for %d notes and Custom…", len(concurrencyOptions()), len(concurrencyNotes))
	}
	for i, note := range concurrencyNotes {
		for j, other := range concurrencyNotes[:i] {
			if note == other {
				t.Errorf("options %d and %d share the note %q", j+1, i+1, note)
			}
		}
	}
}
