//go:build unix

package beads

import (
	"context"
	"os"
	"testing"
)

// Close closes the one ticket by its ID, with the reason, as orchestra closes an epic whose
// subtickets have all merged.
func TestCloseClosesTheTicketWithTheReason(t *testing.T) {
	record := createBd(t, "")
	reason := "All 3 subtickets merged; closed by orchestra after k-1.3"
	if err := (Tracker{Repo: t.TempDir()}).Close(context.Background(), "k-1", reason); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(record)
	if want := "close\nk-1\n--reason=" + reason + "\n"; string(b) != want {
		t.Errorf("bd ran with\n%s\nwant\n%s", b, want)
	}
}
