package dispatch

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// The dashboard's why for a ticket set aside by its check says whether the check or its setup
// failed or timed out.
func TestChecksFailedDetail(t *testing.T) {
	cases := []struct {
		setup, timedOut bool
		want            string
	}{
		{false, false, "checks failed"},
		{false, true, "checks timed out"},
		{true, false, "setup failed"},
		{true, true, "setup timed out"},
	}
	for _, tc := range cases {
		if got := checksFailedDetail(tc.setup, tc.timedOut); got != tc.want {
			t.Errorf("checksFailedDetail(%v, %v) = %q, want %q", tc.setup, tc.timedOut, got, tc.want)
		}
	}
}

// failedHow names what failed, the check or its setup, and how: it fails, or did not finish.
func TestFailedHow(t *testing.T) {
	o := &Loop{cfg: Config{Check: "make check", Setup: "npm ci", CheckTimeout: 5 * time.Minute}}
	cases := []struct {
		setup bool
		err   error
		want  string
	}{
		{false, errors.New("exit status 1"), "'make check' fails"},
		{false, fmt.Errorf("after 5m: %w", errCheckTimedOut), "'make check' did not finish within 5m"},
		{true, errors.New("exit status 1"), "the setup 'npm ci', run before the check, fails"},
		{true, errCheckTimedOut, "the setup 'npm ci', run before the check, did not finish within 5m"},
	}
	for _, tc := range cases {
		if got := o.failedHow(tc.setup, tc.err); got != tc.want {
			t.Errorf("failedHow(%v, %v) = %q, want %q", tc.setup, tc.err, got, tc.want)
		}
	}
}
