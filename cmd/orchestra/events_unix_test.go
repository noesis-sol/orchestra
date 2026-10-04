//go:build unix

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run's event stream starts with what it runs and ends with its exit code, every record giving
// the start its lock gives; a feature run's start record follows the filing, scoped to the epic, and
// one that files nothing ends there. A run with nothing to run, as the fake bd has none ready, has
// its done record in between; one that goes to its loop, for a worker the last run left, the loop's.
func TestARunsEventStreamStartsAndEnds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools, per case")
	}
	for _, tc := range []struct {
		name, screen string
		args         []string
		left         string // the ticket a worker the last run left is on, if any
		code         int
		kinds        string // the records' kinds, info left out
		start        map[string]any
	}{
		{"scoped", featureScreenOK, []string{"--plain", "--ticket", "f-1"}, "", 0, "start done end",
			map[string]any{"scope": "f-1", "concurrency": 1.0}},
		{"scoped, a worker left", featureScreenOK, []string{"--plain", "--ticket", "f-1"}, "f-1", 0,
			"start queue done end", map[string]any{"scope": "f-1", "concurrency": 1.0}},
		{"feature", featureScreenOK, []string{"--plain", "--feature", "Add a --json flag", "--yes"}, "", 0,
			"start done end", map[string]any{"scope": "f-1", "feature": "Add a --json flag"}},
		{"feature turned down",
			`{"type":"result","is_error":false,"structured_output":{"verdict":"reject","reason":"No."}}`,
			[]string{"--plain", "--feature", "Add a --json flag", "--yes"}, "", 2, "start end",
			map[string]any{"feature": "Add a --json flag"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			featureTools(t, tc.screen, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
			setup := func(string) {}
			if tc.left != "" {
				setup = leaveWorker(t, tc.left)
			}
			repo, stdout, stderr, code := runFeatureAfter(t, setup, strings.NewReader(""), tc.args...)
			if code != tc.code {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			recs := streamRecords(t, repo)
			var events []map[string]any
			for _, r := range recs {
				if r["kind"] != "info" {
					events = append(events, r)
				}
			}
			if got := kindsOf(events); got != tc.kinds {
				t.Fatalf("kinds, info left out: %s, want %s\n%v", got, tc.kinds, recs)
			}
			var lock map[string]any
			if err := json.Unmarshal([]byte(read(t, filepath.Join(repo, ".git", project.LockName))), &lock); err != nil {
				t.Fatal(err)
			}
			for _, r := range recs {
				if r["run"] != lock["started"] {
					t.Errorf("%s record: run %v, the lock's start %v", r["kind"], r["run"], lock["started"])
				}
			}
			start, end := recs[0], recs[len(recs)-1]
			if s, _ := start["repo"].(string); !samePath(s, repo) || start["branch"] == "" || start["version"] == "" {
				t.Errorf("start record: %v", start)
			}
			for k, v := range tc.start {
				if start[k] != v {
					t.Errorf("start record: %s is %v, want %v", k, start[k], v)
				}
			}
			if _, ok := start["scope"]; ok != (tc.start["scope"] != nil) {
				t.Errorf("start record's scope: %v", start["scope"])
			}
			if end["code"] != float64(tc.code) {
				t.Errorf("end record: %v", end)
			}
			if done := events[len(events)-2]; tc.code == 0 {
				if text, _ := done["text"].(string); !strings.HasPrefix(text, "READY_EMPTY after 0 tickets") {
					t.Errorf("done record: %v", done)
				}
			}
		})
	}
}
