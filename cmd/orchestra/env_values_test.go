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

// TRIAGE=false, NOTIFY=off and the like turn their feature off, where only 0 did; ORCHESTRA_TICKETS
// takes true as it took 1. An unknown value is a setup problem unless the flag overrides it.
func TestConfigBooleanVariables(t *testing.T) {
	configFixture(t, `{"concurrent": 1}`)
	t.Setenv(noUpdateCheck, "")
	t.Setenv("NOTIFY", "off")
	t.Setenv("TRIAGE", "false")
	t.Setenv("REVIEW", "no")
	t.Setenv("PROMPT_AT_LAUNCH", "FALSE")
	t.Setenv("ORCHESTRA_TICKETS", "true")
	c, p := loadWith(t)
	if len(p) > 0 || c.Notify || c.Triage || c.Review || c.LaunchPrompt || !c.Tickets {
		t.Errorf("notify %v, triage %v, review %v, prompt at launch %v, tickets %v, problems %v",
			c.Notify, c.Triage, c.Review, c.LaunchPrompt, c.Tickets, p)
	}

	t.Setenv("TRIAGE", "nope")
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "TRIAGE must be 1 or 0") ||
		!strings.Contains(p[0], "'nope'") {
		t.Errorf("unknown value: %v", p)
	}
	if c, p := loadWith(t, "-triage=true"); len(p) > 0 || !c.Triage {
		t.Errorf("the flag over an unknown value: triage %v, problems %v", c.Triage, p)
	}
	t.Setenv("TRIAGE", "")

	t.Setenv(noUpdateCheck, "sometimes")
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], noUpdateCheck+" must be 1 or 0") {
		t.Errorf("unknown %s: %v", noUpdateCheck, p)
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

// --concurrent 0, -c 0 and ORCHESTRA_CONCURRENT=0 are out of range, where they fell back to the
// settings as if not given.
func TestConfigConcurrencyZero(t *testing.T) {
	configFixture(t, `{"concurrent": 3}`)
	for _, args := range [][]string{{"--concurrent", "0"}, {"-c", "0"}} {
		if _, p := loadWith(t, args...); len(p) != 1 || !strings.Contains(p[0], "between 1 and 16 (got 0)") {
			t.Errorf("%v: %v", args, p)
		}
	}
	t.Setenv("ORCHESTRA_CONCURRENT", "0")
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "ORCHESTRA_CONCURRENT") ||
		!strings.Contains(p[0], "between 1 and 16 (got 0)") {
		t.Errorf("ORCHESTRA_CONCURRENT=0: %v", p)
	}
	if c, p := loadWith(t, "-c", "2"); len(p) > 0 || c.Concurrency != 2 {
		t.Errorf("the flag over a variable of 0: %d %v", c.Concurrency, p)
	}
	t.Setenv("ORCHESTRA_CONCURRENT", "")
	if c, p := loadWith(t); len(p) > 0 || c.Concurrency != 3 {
		t.Errorf("unset: %d %v", c.Concurrency, p)
	}
}
