package project

import (
	"context"
	"strings"
	"testing"
)

func TestResolveEffort(t *testing.T) {
	cases := []struct {
		flag, setting, want, problem string
	}{
		{"", "", "", ""},
		{"", "medium", "medium", ""},
		{"high", "medium", "high", ""},
		{"high", "bogus", "high", ""}, // the flag overrides the setting
		{"", "bogus", "", "worker_effort must be one of low, medium, high, xhigh, max (got 'bogus')"},
		{"HIGH", "", "", "--worker-effort must be one of"},
	}
	for _, c := range cases {
		got, err := ResolveEffort(c.flag, "worker_effort", c.setting)
		if c.problem != "" {
			if err == nil || !strings.Contains(err.Error(), c.problem) {
				t.Errorf("%q, %q: want the problem %q, got %q, %v", c.flag, c.setting, c.problem, got, err)
			}
		} else if err != nil || got != c.want {
			t.Errorf("%q, %q: got %q, %v, want %q", c.flag, c.setting, got, err, c.want)
		}
	}
}

// orchestra init says workers run at high effort until the project chooses one.
func TestNextStepsMentionWorkerEffortUntilSet(t *testing.T) {
	repo, _ := gitRepo(t)
	steps, _ := Init(context.Background(), repo, PromptChecks{Check: "make check"}, false)
	if next := strings.Join(NextSteps(context.Background(), repo, steps, nil, Choice{}, "", onPath), "\n"); !strings.Contains(next, `"worker_effort"`) ||
		!strings.Contains(next, "Workers run at high effort.") {
		t.Errorf("unset: %q", next)
	}
	if next := strings.Join(NextSteps(context.Background(), repo, steps, nil, Choice{}, "medium", onPath), "\n"); strings.Contains(next, "worker_effort") {
		t.Errorf("set: %q", next)
	}
}
