package beads

import "testing"

// A ticket's defer_until is read from 'bd list --json' as bd gives it: absent, empty or RFC 3339.
func TestParseListReadsDeferUntil(t *testing.T) {
	raw := `[{"id":"k-1","status":"deferred"},
	         {"id":"k-2","status":"deferred","defer_until":""},
	         {"id":"k-3","status":"deferred","defer_until":"2026-11-03T22:00:00Z"}]`
	tickets, err := parseList([]byte(raw), "")
	if err != nil || len(tickets) != 3 {
		t.Fatalf("parseList: %+v, %v", tickets, err)
	}
	for i, want := range []string{"", "", "2026-11-03T22:00:00Z"} {
		if got := tickets[i].DeferUntil; got != want {
			t.Errorf("%s: defer_until %q, want %q", tickets[i].ID, got, want)
		}
	}
}
