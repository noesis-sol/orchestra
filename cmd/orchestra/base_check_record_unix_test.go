//go:build unix

package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The overnight pre-flight's merge check of Base ends before the event stream opens: its check record,
// suite base, follows the run's start record once it does, saying what ran on which commit, for how
// long and how it ended, and where the output of a failure is.
func TestBaseCheckRecordsItsRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, check, result string
		output              bool
	}{
		{"passing", "true", "passed", false},
		{"failing", "echo '--- FAIL: TestMerge'; false", "failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := checkSession(t, nil, nil, "")
			repo, git := gitRepo(t)
			s.cfg.Repo, s.cfg.Base, s.cfg.WTRoot, s.cfg.Check = repo, "main", t.TempDir(), tc.check
			baseCheck(runCtx(t), s)
			s.log.Begin(repo, dispatch.RunStart{Started: time.Now(), Repo: repo, Branch: "main", Concurrency: 1})
			s.log.End(0)

			b := read(t, filepath.Join(repo, project.RunPath(dispatch.EventsName)))
			var kinds []string
			var rec map[string]any
			for line := range strings.Lines(b) {
				var r map[string]any
				if err := json.Unmarshal([]byte(line), &r); err != nil {
					t.Fatalf("not a JSON object (%v): %s", err, line)
				}
				kinds = append(kinds, r["kind"].(string))
				if r["kind"] == "check" {
					rec = r
				}
			}
			if strings.Join(kinds, " ") != "start check end" {
				t.Fatalf("records %q, want start check end", kinds)
			}
			head := strings.TrimSpace(git(repo, "rev-parse", "main"))
			for k, v := range map[string]any{"suite": "base", "command": tc.check, "commit": head[:10],
				"result": tc.result} {
				if rec[k] != v {
					t.Errorf("check record: %s is %#v, want %#v\n%v", k, rec[k], v, rec)
				}
			}
			if s, ok := rec["seconds"].(float64); !ok || s < 0 {
				t.Errorf("check record: seconds %#v\n%v", rec["seconds"], rec)
			}
			for _, k := range []string{"ticket", "train"} {
				if _, ok := rec[k]; ok {
					t.Errorf("check record: %s is %#v, want none", k, rec[k])
				}
			}
			out, ok := rec["output"].(string)
			if ok != tc.output || ok && !strings.HasSuffix(out, dispatch.BaseCheckLogName) {
				t.Errorf("check record: output %#v, want one: %v", rec["output"], tc.output)
			}
		})
	}
}
