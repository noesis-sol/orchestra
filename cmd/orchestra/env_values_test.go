package main

import (
	"context"
	"strings"
	"testing"
)

// envBool takes 1/0, true/false, yes/no and on/off in any case, def when unset, and reports
// anything else.
func TestEnvBool(t *testing.T) {
	for _, tc := range []struct {
		value     string
		def, want bool
		problem   bool
	}{
		{"", true, true, false}, {"", false, false, false},
		{"1", false, true, false}, {"true", false, true, false}, {"YES", false, true, false}, {"On", false, true, false},
		{"0", true, false, false}, {"false", true, false, false}, {"no", true, false, false}, {"OFF", true, false, false},
		{"2", true, true, true}, {"enabled", false, false, true}, {" 1", false, false, true},
	} {
		got, problem := envBool(func(string) string { return tc.value }, "NOTIFY", tc.def)
		if got != tc.want || (problem != "") != tc.problem {
			t.Errorf("NOTIFY=%q (default %v) = %v, %q", tc.value, tc.def, got, problem)
		}
		if tc.problem && !strings.Contains(problem, "NOTIFY must be 1 or 0, true or false") {
			t.Errorf("NOTIFY=%q: problem %q", tc.value, problem)
		}
	}
}

// ORCHESTRA_NO_UPDATE_CHECK takes the same values: on turns the check off, off leaves it on, and an
// unknown value, reported as a setup problem, turns it off too.
func TestUpdateCheckTurnedOffByAnyOn(t *testing.T) {
	latestRelease(t, "v0.5.1", "v0.6.0")
	cacheIn(t)
	for value, off := range map[string]bool{"true": true, "yes": true, "ON": true, "false": false, "off": false,
		"sometimes": true} {
		check := startUpdateCheck(context.Background(), func(k string) string {
			if k == noUpdateCheck {
				return value
			}
			return ""
		})
		if (check == nil) != off {
			t.Errorf("%s=%q: check %v, want off %v", noUpdateCheck, value, check, off)
		}
		check.Newer()
	}
}
