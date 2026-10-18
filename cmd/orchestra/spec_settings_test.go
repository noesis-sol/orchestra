package main

import (
	"slices"
	"testing"
	"time"
)

// The spec breakdown's time limit and effort are taken from their flags over their variables, over
// the project's settings.json, over the defaults: an hour, at effort high.
func TestSpecBreakdownSettings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		layer    string
		args     []string
		env      map[string]string
		settings string
		limit    time.Duration
		effort   string
	}{
		{"by default", nil, nil, "", time.Hour, "high"},
		{"settings.json", nil, nil, `{"spec_timeout": "90m", "spec_effort": "xhigh"}`, 90 * time.Minute, "xhigh"},
		{"the variables over settings.json", nil, map[string]string{"ORCHESTRA_SPEC_TIMEOUT": "2h",
			"ORCHESTRA_SPEC_EFFORT": "max"}, `{"spec_timeout": "90m", "spec_effort": "xhigh"}`, 2 * time.Hour, "max"},
		{"the flags over all", []string{"--spec-timeout", "45m", "--spec-effort", "medium"},
			map[string]string{"ORCHESTRA_SPEC_TIMEOUT": "2h", "ORCHESTRA_SPEC_EFFORT": "max"},
			`{"spec_timeout": "90m", "spec_effort": "xhigh"}`, 45 * time.Minute, "medium"},
	} {
		c, _, problems := settle(t, tc.args, tc.env, tc.settings)
		if c.SpecTimeout != tc.limit || c.SpecEffort != tc.effort || len(problems) > 0 {
			t.Errorf("%s: %v at %q, want %v at %q (problems %v)", tc.layer, c.SpecTimeout, c.SpecEffort, tc.limit,
				tc.effort, problems)
		}
	}
}

// The breakdown's settings, flags and variables are checked as a run's others are.
func TestSpecBreakdownSettingProblems(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		args     []string
		env      map[string]string
		settings string
		want     []string
	}{
		{"variables", nil, map[string]string{"ORCHESTRA_SPEC_TIMEOUT": "never"}, "",
			[]string{"ORCHESTRA_SPEC_TIMEOUT must be a positive duration such as 5m (got 'never')."}},
		{"--spec-timeout 0", []string{"--spec-timeout", "0"}, nil, "",
			[]string{"--spec-timeout must be a positive duration such as 5m (got 0s)."}},
		{"--spec-effort", []string{"--spec-effort", "huge"}, nil, "",
			[]string{"--spec-effort must be one of low, medium, high, xhigh, max (got 'huge')."}},
		{"settings.json", nil, nil, `{"spec_timeout": "1 hour", "spec_effort": "hi"}`, []string{
			".orchestra/settings.json: spec_effort must be one of low, medium, high, xhigh, max (got 'hi').",
			".orchestra/settings.json: spec_timeout must be a positive duration such as 90m (got '1 hour').",
		}},
		{"flags over invalid variables", []string{"--spec-timeout", "2h", "--spec-effort", "low"},
			map[string]string{"ORCHESTRA_SPEC_TIMEOUT": "0", "ORCHESTRA_SPEC_EFFORT": "huge"}, "", nil},
	} {
		if _, _, got := settle(t, tc.args, tc.env, tc.settings); !slices.Equal(got, tc.want) {
			t.Errorf("%s: problems\n%q\nwant\n%q", tc.name, got, tc.want)
		}
	}
}
