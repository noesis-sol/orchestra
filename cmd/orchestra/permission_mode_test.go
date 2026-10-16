package main

import (
	"slices"
	"testing"
)

// A spec run, which nobody watches, starts its workers in auto unless the project's settings.json,
// the flag or its variable choose another mode; any other run leaves the mode to Claude Code.
func TestSpecRunsDefaultToAutoPermissionMode(t *testing.T) {
	spec := []string{"--spec", "app.md"}
	for _, tc := range []struct {
		name     string
		args     []string
		env      map[string]string
		settings string
		want     string
	}{
		{"a run", nil, nil, "", ""},
		{"a spec run", spec, nil, "", "auto"},
		{"a spec run from ORCHESTRA_SPEC", nil, map[string]string{"ORCHESTRA_SPEC": "app.md"}, "", "auto"},
		{"settings.json", spec, nil, `{"worker_permission_mode": "default"}`, "default"},
		{"WORKER_PERMISSION_MODE", spec, map[string]string{"WORKER_PERMISSION_MODE": "acceptEdits"},
			`{"worker_permission_mode": "default"}`, "acceptEdits"},
		{"the flag", append(slices.Clone(spec), "--worker-permission-mode", "default"), nil, "", "default"},
	} {
		c, _, problems := settle(t, tc.args, tc.env, tc.settings)
		if c.WorkerPermissionMode != tc.want || len(problems) > 0 {
			t.Errorf("%s: permission mode %q, want %q (problems %v)", tc.name, c.WorkerPermissionMode, tc.want, problems)
		}
	}
}
