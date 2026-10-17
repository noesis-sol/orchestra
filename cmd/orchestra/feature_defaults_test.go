package main

import (
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A feature run, like a spec run, runs unwatched once its plan is filed: its workers start in auto
// unless the project's settings.json, the flag or its variable choose another mode, and it has no
// ticket limit unless -limit or LIMIT gives one.

func TestFeatureRunsDefaultToAutoPermissionMode(t *testing.T) {
	feature := []string{"--feature", "Add a flag"}
	for _, tc := range []struct {
		name     string
		args     []string
		env      map[string]string
		settings string
		want     string
	}{
		{"a run", nil, nil, "", ""},
		{"a feature run", feature, nil, "", project.PermissionAuto},
		{"settings.json", feature, nil, `{"worker_permission_mode": "default"}`, "default"},
		{"WORKER_PERMISSION_MODE", feature, map[string]string{"WORKER_PERMISSION_MODE": "acceptEdits"},
			`{"worker_permission_mode": "default"}`, "acceptEdits"},
		{"the flag", append(slices.Clone(feature), "--worker-permission-mode", "default"), nil, "", "default"},
	} {
		c, _, problems := settle(t, tc.args, tc.env, tc.settings)
		if c.WorkerPermissionMode != tc.want || len(problems) > 0 {
			t.Errorf("%s: permission mode %q, want %q (problems %v)", tc.name, c.WorkerPermissionMode, tc.want, problems)
		}
	}
}

func TestFeatureRunHasNoTicketLimitUnlessGiven(t *testing.T) {
	feature := []string{"--feature", "Add a flag"}
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want int
	}{
		{"a feature", feature, nil, dispatch.NoLimit},
		{"with DONE_SO_FAR", feature, map[string]string{"DONE_SO_FAR": "80"}, dispatch.NoLimit},
		{"-limit", append(slices.Clone(feature), "--limit", "5"), nil, 5},
		{"LIMIT", feature, map[string]string{"LIMIT": "7"}, 7},
		{"no feature", nil, nil, 40},
	} {
		f, problems, err := readFlags(tc.args, specEnv(tc.env), io.Discard)
		if err != nil || len(problems) > 0 || f.Limit != tc.want {
			t.Errorf("%s: limit %d, want %d (%v, %q)", tc.name, f.Limit, tc.want, err, problems)
		}
	}
	// A limit given and reached is a setup problem, as a spec run's is.
	_, problems, _ := readFlags(append(slices.Clone(feature), "--limit", "3", "--done-so-far", "3"), specEnv(nil),
		io.Discard)
	if len(problems) != 1 || !strings.Contains(problems[0], "--feature would file a plan and run none of it: "+
		"-done-so-far (3) has reached -limit (3). Raise -limit [LIMIT], lower -done-so-far [DONE_SO_FAR], "+
		"or leave both out.") {
		t.Errorf("-limit reached: %q", problems)
	}
}

// A feature typed at the question runs as one given with --feature does: no ticket limit and
// workers in auto permission mode unless the flags say otherwise, and named in the run lock from
// then on.
func TestFeatureChosenRunsAsFeatureGivenDoes(t *testing.T) {
	repo := specFixture(t)
	t.Setenv("WORKER_PERMISSION_MODE", "")
	t.Setenv("LIMIT", "")
	const request = "Add a --json flag"
	for _, tc := range []struct {
		name  string
		args  []string
		limit int
		mode  string
	}{
		{"by default", nil, dispatch.NoLimit, project.PermissionAuto},
		{"with a limit and a permission mode given",
			[]string{"--limit", "5", "--worker-permission-mode", project.PermissionAcceptEdits},
			5, project.PermissionAcceptEdits},
	} {
		s, errOut := chosenSession(t, repo, tc.args...)
		s.featureChosen(tui.Work{Kind: tui.WorkFeature, Description: request})
		c := s.cfg
		if errOut.Len() > 0 {
			t.Fatalf("%s: stderr:\n%s", tc.name, errOut)
		}
		if c.Feature != request || c.Limit != tc.limit || c.WorkerPermissionMode != tc.mode {
			t.Errorf("%s: feature %q, limit %d, permission mode %q; want %q, %d, %q",
				tc.name, c.Feature, c.Limit, c.WorkerPermissionMode, request, tc.limit, tc.mode)
		}
		if h, held, err := project.RunHolder(t.Context(), repo); err != nil || !held || h.Feature != request ||
			h.PID != 1 {
			t.Errorf("%s: the run lock: %+v, held %v, %v", tc.name, h, held, err)
		}
		_ = s.lock.Close() // for the next case's
	}
}
