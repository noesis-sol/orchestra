package beads

import (
	"errors"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A read whose output parses has no error; one that doesn't has bd's own error when bd failed too,
// as it carries bd's stderr, and else the parse error, wrapped.
func TestReadFailed(t *testing.T) {
	parseErr, runErr := errors.New("unexpected end of JSON input"), errors.New("bd: database locked")
	cases := []struct {
		name       string
		parse, run error
		want       string
		wraps      error
	}{
		{"parsed", nil, nil, "", nil},
		{"parsed though bd failed", nil, runErr, "", nil},
		{"bd failed", parseErr, runErr, "bd: database locked", runErr},
		{"unreadable", parseErr, nil, "could not parse 'bd list --json': unexpected end of JSON input", parseErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := readFailed("bd list --json", tc.parse, tc.run)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("readFailed = %v, want nil", err)
			case tc.want != "" && (err == nil || err.Error() != tc.want):
				t.Errorf("readFailed = %v, want %q", err, tc.want)
			case tc.wraps != nil && !errors.Is(err, tc.wraps):
				t.Errorf("readFailed = %v, which doesn't wrap %v", err, tc.wraps)
			}
		})
	}
}

// Only an open ticket, not a question for the maintainer nor of an excluded type, is dispatched.
func TestDispatchable(t *testing.T) {
	cases := []struct {
		name string
		t    dispatch.Ticket
		want bool
	}{
		{"open", dispatch.Ticket{Status: dispatch.StatusOpen, IssueType: "task"}, true},
		{"in progress", dispatch.Ticket{Status: dispatch.StatusInProgress, IssueType: "task"}, false},
		{"closed", dispatch.Ticket{Status: dispatch.StatusClosed, IssueType: "task"}, false},
		{"an epic", dispatch.Ticket{Status: dispatch.StatusOpen, IssueType: "epic"}, false},
		{"a question", dispatch.Ticket{Status: dispatch.StatusOpen, IssueType: "task",
			Labels: []string{dispatch.HumanLabel}}, false},
	}
	for _, tc := range cases {
		if got := dispatchable(tc.t, []string{"epic"}); got != tc.want {
			t.Errorf("%s: dispatchable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
