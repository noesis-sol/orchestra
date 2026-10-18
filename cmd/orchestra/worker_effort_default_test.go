package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// workerEffortCases are the ways a run gets its workers' effort, and the effort each gives: high with
// none, else the setting, under WORKER_EFFORT, under --worker-effort.
var workerEffortCases = []struct {
	name     string
	args     []string
	env      map[string]string
	settings string
	want     string
}{
	{"by default", nil, nil, `{"mcp_servers": []}`, "high"},
	{"from settings.json", nil, nil, `{"mcp_servers": [], "worker_effort": "medium"}`, "medium"},
	{"from WORKER_EFFORT", nil, map[string]string{"WORKER_EFFORT": "low"}, `{"mcp_servers": [], "worker_effort": "medium"}`,
		"low"},
	{"from --worker-effort", []string{"--worker-effort", "max"}, map[string]string{"WORKER_EFFORT": "low"},
		`{"mcp_servers": [], "worker_effort": "medium"}`, "max"},
}

// Claude workers start at high effort unless the project, WORKER_EFFORT or --worker-effort chooses
// another; the organs and the headless breakdown keep their own efforts whatever the workers'.
func TestClaudeWorkersStartAtHighEffortByDefault(t *testing.T) {
	for _, tc := range workerEffortCases {
		c, _, p := settle(t, tc.args, tc.env, tc.settings)
		if len(p) > 0 || c.WorkerEffort != tc.want ||
			strings.Join(c.WorkerArgs, " ") != "--no-chrome --effort "+tc.want {
			t.Errorf("%s: effort %q, worker arguments %q, want --effort %s (problems %v)", tc.name, c.WorkerEffort,
				c.WorkerArgs, tc.want, p)
		}
		if c.OrganEffort != "" || c.SpecEffort != project.DefaultSpecEffort {
			t.Errorf("%s: the organs' effort is %q and the breakdown's %q, want their own", tc.name, c.OrganEffort,
				c.SpecEffort)
		}
	}
}

// The session that refines a spec or talks a feature through starts at the workers' effort, resolved
// as theirs is, in a pane as on orchestra's terminal (see TestInterviewFilesTheFeatureAndRunsItsEpic).
func TestRefineSessionStartsAtTheWorkersEffort(t *testing.T) {
	tools := t.TempDir()
	faketool.Write(t, tools, "claude", "#!/bin/sh\necho 'claude: not in this test' >&2\nexit 1\n")
	onPath(t, tools)
	for _, tc := range workerEffortCases {
		c, _, _ := settle(t, tc.args, tc.env, tc.settings)
		var out strings.Builder
		b := newRefineBreakdown(&session{cfg: c, e: cmdEnv{getenv: func(string) string { return "" },
			stdin: strings.NewReader(""), stdout: &out, stderr: &out}})
		if b.effort != tc.want {
			t.Errorf("%s: the session's effort is %q, want %q", tc.name, b.effort, tc.want)
		}
		b.repo, b.pane, b.epics, b.tracker = t.TempDir(), "w1:p1", filedEpic{}, &filedTree{}
		b.terminal = func(context.Context, string, project.RefineMode) error {
			t.Errorf("%s: the session fell back to the terminal", tc.name)
			return nil
		}
		for _, mode := range []string{"spec", "feature"} {
			synctest.Test(t, func(t *testing.T) {
				// claude starts, then leaves without handing anything back
				panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateIdle, dispatch.StateGone}}
				b.panes = panes
				if mode == "spec" {
					_, _ = b.BreakDown(t.Context(), breakdownRequest{Spec: project.Spec{Path: "app.md"}, Refine: true})
				} else {
					_, _ = b.talkFeature(t.Context(), "Add a --json flag", paneRun)
				}
				end := slices.Index(panes.launched, "--")
				if i := slices.Index(panes.launched, "--effort"); i < 0 || i > end || panes.launched[i+1] != tc.want {
					t.Errorf("%s: the %s's claude started with %q, want --effort %s", tc.name, mode, panes.launched,
						tc.want)
				}
			})
		}
	}
}
