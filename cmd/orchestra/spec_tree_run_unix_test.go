//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// An answer whose tree can't be filed as given, with a blocks cycle or a key that names no issue,
// ends the run with exit 2 before its loop, as a breakdown that stopped part way does: orchestra
// files none of it, lists what the session filed for the spec, and says that running it again
// carries on.
func TestSpecBreakdownWhoseAnswerCantBeFiled(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools, per case")
	}
	root := filing(t, "toolu_1", "bd create --type epic --priority 1 --title 'An app' --metadata "+specMeta+" --silent")
	task := func(key string, waits ...string) specIssue {
		return specIssue{Key: key, Type: "task", Priority: 2, Title: "Do " + key, Description: "Does it.",
			Acceptance: "Done", Files: []string{key + ".go"}, WaitsFor: waits}
	}
	for _, tc := range []struct {
		name   string
		issues []specIssue
		want   string
	}{
		{"a cycle", []specIssue{task("a", "b"), task("b", "a")}, "its blocks links make a cycle: a → b → a"},
		{"an unknown key", []specIssue{task("a"), task("b", "a", "c")},
			"b waits for c, which is neither a key of its own nor an issue bd has"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, spec, tools := breakdownFixture(t, "", root+answering(t, specAnswer{Root: "s-1", Issues: tc.issues}))
			stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
			if code != 2 || strings.Contains(stdout, "START") {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			for _, want := range []string{
				"orchestra couldn't break the spec down: its answer can't be filed: " + tc.want + "\n",
				"It filed 1 epic and 0 tickets for .orchestra/specs/app.md, under s-1:\n  s-1  epic  An app\n",
				"Run it again to carry on: orchestra --spec " + spec + "\n",
			} {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if calls := read(t, filepath.Join(tools, "bd-calls")); strings.Contains(calls, "create|--json|") ||
				strings.Contains(calls, "update|") {
				t.Errorf("orchestra filed some of the answer:\n%s", calls)
			}
			if recs := streamRecords(t, repo); kindsOf(recs) != "start end" || recs[1]["code"] != 2.0 {
				t.Errorf("records: %v", recs)
			}
		})
	}
}

// An update of the tree answers specSchema, and files what it changes through bd itself: orchestra
// files nothing from its answer.
func TestSpecUpdateFilesNothingFromItsAnswer(t *testing.T) {
	t.Parallel()
	repo, tools := breakdownRepo(t), t.TempDir()
	faketool.Write(t, tools, "bd", filingBd)
	fakeSession(t, tools, "", answering(t, specAnswer{Root: "s-1", Issues: []specIssue{{Key: "a", Type: "task",
		Title: "Do a"}}}))
	onPath(t, tools)
	if err := os.WriteFile(filepath.Join(tools, "filed"), []byte(keptRoot("open", "0ld", "c0de")), 0o644); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	got, err := specBreakdownIn(repo, &out).BreakDown(testCtx(context.Background(), t), breakdownRequest{Spec: testSpec,
		Update: &specUpdate{Root: "s-1", File: ".orchestra/run/spec-update.md"}})
	if err != nil || got.Root != "s-1" {
		t.Fatalf("%+v, %v", got, err)
	}
	if args := strings.Split(read(t, filepath.Join(tools, "claude-args")), "\n"); !slices.Contains(args, specSchema) {
		t.Errorf("claude ran with\n%q\nwant the update's schema", args)
	}
	if calls, err := os.ReadFile(filepath.Join(tools, "bd-calls")); err == nil &&
		(strings.Contains(string(calls), "create|") || strings.Contains(string(calls), "update|")) {
		t.Errorf("orchestra filed from an update's answer:\n%s", calls)
	}
}
