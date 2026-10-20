package project

import (
	"testing"
	"time"
)

// Each default the help writes as text is the duration a run takes.
func TestDefaultTimeoutTexts(t *testing.T) {
	for _, tc := range []struct {
		text string
		want time.Duration
	}{
		{DefaultCheckTimeoutText, DefaultCheckTimeout},
		{DefaultCheckFullTimeoutText, DefaultCheckFullTimeout},
		{DefaultSpecTimeoutText, DefaultSpecTimeout},
	} {
		if d, err := ParseCheckTimeout(tc.text); err != nil || d != tc.want {
			t.Errorf("%q reads as %s, %v; want %s", tc.text, d, err, tc.want)
		}
	}
}

// A given value's problem names what gave it, the flag or its variable, as the run's flags say.
func TestGivenProblemsNameWhatGaveTheValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"the variable", second(ResolveConcurrency(Given[int]{Given: true, Name: "ORCHESTRA_CONCURRENT"}, Settings{})),
			"ORCHESTRA_CONCURRENT must be between 1 and 16 (got 0)"},
		{"the flag", second(ResolveConcurrency(Given[int]{Value: 17, Given: true, Name: "--concurrent"}, Settings{})),
			"--concurrent must be between 1 and 16 (got 17)"},
		{"a time limit", second(ResolveSpecTimeout(Given[time.Duration]{Given: true, Name: "--spec-timeout"}, Settings{})),
			"--spec-timeout must be a positive duration such as 5m (got 0s)"},
		{"an effort", second(ResolveSpecEffort(Given[string]{Value: "most", Name: "ORCHESTRA_SPEC_EFFORT"}, Settings{})),
			"ORCHESTRA_SPEC_EFFORT must be one of low, medium, high, xhigh, max (got 'most')"},
		{"a permission mode", second(ResolvePermissionMode(Given[string]{Value: "plan", Name: "--worker-permission-mode"},
			Settings{}, false)), "--worker-permission-mode can't be plan: a worker in it only plans, and never changes " +
			"a file. Use one of default, manual, acceptEdits, auto"},
	} {
		if errText(tc.err) != tc.want {
			t.Errorf("%s: %v\nwant %s", tc.name, tc.err, tc.want)
		}
	}
}

// The check commands a footprint leaves out are the merge check's, the full check's and a worker's own.
func TestCheckCommands(t *testing.T) {
	s := Settings{CheckFast: FastRunner, CheckFull: FullRunner, CheckOwn: "scripts/check-own.sh"}
	if got, want := s.CheckCommands(), FastRunner+"\n"+FullRunner+"\nscripts/check-own.sh"; got != want {
		t.Errorf("CheckCommands() = %q, want %q", got, want)
	}
}
