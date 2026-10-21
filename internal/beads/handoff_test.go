package beads

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/tracker"
)

// The close reason of each ticket a ticket depends on is read from 'bd show --json', as bd 1.3 gives it
// among its dependencies, for the hand-offs a new worker is given.
func TestParseTicketReadsADependencysCloseReason(t *testing.T) {
	raw := `{"schema_version":1,"data":[{"id":"k-2","title":"Build on it","status":"open","dependencies":[
	         {"id":"k-1","title":"Rename it","status":"closed","dependency_type":"blocks",
	          "close_reason":"Renamed Loop.run to Loop.Run."},
	         {"id":"k-0","title":"The epic","status":"open","dependency_type":"parent-child"}]}]}`
	tk, ok := parseTicket([]byte(raw))
	if !ok || len(tk.Dependencies) != 2 {
		t.Fatalf("parseTicket: %v %+v", ok, tk)
	}
	if d := tk.Dependencies[0]; d.ID != "k-1" || d.Status != tracker.StatusClosed || d.DependencyType != "blocks" ||
		d.CloseReason != "Renamed Loop.run to Loop.Run." {
		t.Errorf("the closed blocker read as %+v", d)
	}
	if d := tk.Dependencies[1]; d.CloseReason != "" {
		t.Errorf("the open parent read with a close reason: %+v", d)
	}
}
