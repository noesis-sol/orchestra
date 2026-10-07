package beads

import "testing"

// A closed ticket's close reason is read from 'bd show --json', for the run report's evidence.
func TestParseTicketReadsTheCloseReason(t *testing.T) {
	raw := `{"schema_version":1,"data":[{"id":"k-1","title":"Watch for it","status":"closed",
	         "close_reason":"Measured it; no change needed."}]}`
	tk, ok := parseTicket([]byte(raw))
	if !ok || tk.CloseReason != "Measured it; no change needed." {
		t.Errorf("parseTicket: %v %+v", ok, tk)
	}
}
