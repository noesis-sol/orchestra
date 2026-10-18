package main

import "testing"

// The scout's effort is checked as a run checks it: a value that isn't an effort, from ORGAN_EFFORT
// or settings.json, never reaches claude --effort.
func TestScoutEffortIsChecked(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ env, setting, want string }{
		{"", "", ""},
		{"high", "low", "high"},
		{"", "medium", "medium"},
		{"extreme", "low", ""},
		{"", "loud", ""},
	} {
		getenv := func(k string) string {
			if k == "ORGAN_EFFORT" {
				return c.env
			}
			return ""
		}
		if got := scoutEffort(getenv, c.setting); got != c.want {
			t.Errorf("ORGAN_EFFORT %q, organ_effort %q: effort %q, want %q", c.env, c.setting, got, c.want)
		}
	}
}
