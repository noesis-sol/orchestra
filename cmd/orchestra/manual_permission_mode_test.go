package main

import "testing"

// manual, the name newer Claude Code gives the default mode, is a permission mode workers can be
// started in, from settings.json, the flag or its variable alike.
func TestManualPermissionMode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		args     []string
		env      map[string]string
		settings string
	}{
		{"settings.json", nil, nil, `{"worker_permission_mode": "manual"}`},
		{"the flag", []string{"--worker-permission-mode", "manual"}, nil, ""},
		{"WORKER_PERMISSION_MODE", nil, map[string]string{"WORKER_PERMISSION_MODE": "manual"}, ""},
	} {
		c, _, problems := settle(t, tc.args, tc.env, tc.settings)
		if c.WorkerPermissionMode != "manual" || len(problems) > 0 {
			t.Errorf("%s: permission mode %q, want manual (problems %v)", tc.name, c.WorkerPermissionMode, problems)
		}
	}
}
