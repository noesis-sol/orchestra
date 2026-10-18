//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// chosenSpecRun prepares a run asked on a terminal to work on a spec: specTreeBd on PATH, a claude
// that fails, no spec run's variable or permission mode set, and the breakdown a fakeBreakdown,
// which the returned function gives once the run has asked for it, with the run's options then. It
// returns the spec, outside the repository, and that function.
func chosenSpecRun(t *testing.T) (spec string, asked func() (*fakeBreakdown, options)) {
	t.Helper()
	tools := t.TempDir()
	faketool.Write(t, tools, "bd", specTreeBd)
	faketool.Write(t, tools, "claude", "#!/bin/sh\necho 'claude: not in this test' >&2\nexit 1\n")
	onPath(t, tools)
	for _, k := range []string{"ORCHESTRA_SPEC", "ORCHESTRA_REFINE", "WORKER_PERMISSION_MODE"} {
		setenv(t, k, "")
	}
	spec = writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n\nIt lists things, sorted.\n")
	var (
		f   *fakeBreakdown
		cfg options
	)
	old := breakdownOf
	breakdownOf = func(s *session) specBreakdown { // on the run's goroutine, which asked returns after
		f, cfg = &fakeBreakdown{repo: s.cfg.Repo, root: "s-1"}, s.cfg
		return f
	}
	t.Cleanup(func() { breakdownOf = old })
	return spec, func() (*fakeBreakdown, options) { return f, cfg }
}

// In a terminal, Spec at the question runs the spec chosen as --spec runs it: committed, broken down
// and refined first as asked, the run scoped to its root epic, with no ticket limit and workers in
// auto permission mode. From the answer on, the run lock names the spec; so do the run's records.
func TestRunAsksForASpecAndRunsIt(t *testing.T) {
	spec, asked := chosenSpecRun(t)
	term, repo, exit := runOnTerminal(t)
	term.waitFor(t, "> Current tickets")
	term.waitFor(t, "orchestra --spec <file>")
	term.typeKeys(t, pickSpec, false)
	term.waitFor(t, onSpecFile)
	term.typeKeys(t, spec+keyEnter, false)
	term.waitFor(t, onRefine)
	term.typeKeys(t, keyDown+keyEnter, false)
	// stderr has the check's warnings on the tree fakeBreakdown files, which has no metadata.
	if code, stderr := exit(); code != dispatch.ExitOK {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	f, cfg := asked()
	const kept = ".orchestra/specs/app.md"
	if f == nil || f.got.Path != kept || !f.got.Refine || f.holder.Spec != kept {
		t.Fatalf("the breakdown: %+v; the terminal:\n%s", f, screenOf(term))
	}
	if cfg.specFile != spec || cfg.Limit != dispatch.NoLimit || cfg.WorkerPermissionMode != project.PermissionAuto {
		t.Errorf("the run: spec %q, limit %d, permission mode %q", cfg.specFile, cfg.Limit, cfg.WorkerPermissionMode)
	}
	out := screenOf(term)
	for _, want := range []string{"Spec: " + spec + ", refined with claude first\n",
		"committed the spec " + kept + " (sha256 ", "broke the spec down into s-1 (2 epics, 2 tickets)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, out)
		}
	}
	if subject := gitIn(t, repo, "log", "-1", "--format=%s"); subject != "orchestra: add spec app.md" {
		t.Errorf("HEAD: %q", subject)
	}
	if recs := streamRecords(t, repo); recs[0]["kind"] != "start" || recs[0]["spec"] != kept || recs[0]["scope"] != "s-1" {
		t.Errorf("start record: %v", recs[0])
	}
}

// Esc at the choice to refine ends orchestra with nothing done: nothing committed or broken down, no
// run recorded.
func TestRunStoppedAtTheChoiceToRefineDoesNothing(t *testing.T) {
	spec, asked := chosenSpecRun(t)
	term, repo, exit := runOnTerminal(t)
	head := gitIn(t, repo, "rev-parse", "HEAD")
	term.waitFor(t, "> Current tickets")
	term.typeKeys(t, pickSpec, false)
	term.waitFor(t, onSpecFile)
	term.typeKeys(t, spec+keyEnter, false)
	term.waitFor(t, onRefine)
	term.typeKeys(t, keyEsc, false)
	code, stderr := exit()
	if code != dispatch.ExitInterrupted || stderr != "orchestra: stopped before the run started; nothing was changed.\n" {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if f, _ := asked(); f != nil {
		t.Errorf("the spec was broken down: %+v", f)
	}
	if now := gitIn(t, repo, "rev-parse", "HEAD"); now != head {
		t.Errorf("committed: HEAD %s, was %s", now, head)
	}
	if b, err := os.ReadFile(filepath.Join(repo, project.RunPath(dispatch.EventsName))); err == nil && len(b) > 0 {
		t.Errorf("a run was recorded:\n%s", b)
	}
}
