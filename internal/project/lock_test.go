package project

import (
	"testing"
	"time"
)

// A holder is described by what it has: its start time by the clock today, with the date before.
func TestHolderDescription(t *testing.T) {
	now := time.Date(2026, 10, 2, 15, 0, 0, 0, time.Local)
	for _, tc := range []struct {
		h    Holder
		want string
	}{
		{Holder{PID: 44497, Started: time.Date(2026, 10, 2, 8, 31, 0, 0, time.Local), Branch: "main", Pane: "w2B:p60"},
			"pid 44497, since 08:31, main, pane w2B:p60"},
		{Holder{PID: 7, Started: time.Date(2026, 10, 1, 23, 5, 0, 0, time.Local), Branch: "batch/x", Ticket: "x-1"},
			"pid 7, since Oct 1 23:05, batch/x, --ticket x-1"},
		{Holder{PID: 7, Feature: "Add a --json flag\nto  every command that lists things"},
			`pid 7, --feature "Add a --json flag to every command that…"`},
		{Holder{}, RunPath(LockName) + " doesn't say which"},
	} {
		if got := tc.h.describe(now); got != tc.want {
			t.Errorf("%+v:\n got %s\nwant %s", tc.h, got, tc.want)
		}
	}
}
