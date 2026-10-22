package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// changedFilesSay is a History whose ChangedFiles says files, or fails with err.
type changedFilesSay struct {
	History
	files []string
	err   error
}

func (c changedFilesSay) ChangedFiles(context.Context, string, string, string) ([]string, error) {
	return c.files, c.err
}

// depsLoop is a Loop with the setup cmd, reading package-lock.json, whose git says h of what changed,
// and ticket A's worktree, which it returns.
func depsLoop(t *testing.T, cmd string, h History) (*Loop, string) {
	t.Helper()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := &Loop{cfg: Config{Setup: cmd, SetupFiles: []string{"package-lock.json"}, CheckTimeout: time.Minute}, log: log}
	o.history = h
	return o, t.TempDir()
}

// runFile writes worktree wt's run file name, as a setup there leaves it.
func runFile(t *testing.T, wt, name string) {
	t.Helper()
	dir := filepath.Join(wt, ".orchestra", "run")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// A worktree's setup is due after one there that failed or was cut short, in this run or an earlier
// one, whatever changed since, as it may have left the dependencies missing or half installed; else
// when a file the setup reads changed since they were last set up, or git can't say.
func TestSetupDue(t *testing.T) {
	t.Parallel()
	lock := changedFilesSay{files: []string{"package-lock.json", "src/a.ts"}}
	none := changedFilesSay{files: []string{"src/a.ts"}}
	for _, tc := range []struct {
		name    string
		cmd     string
		h       History
		prepare func(o *Loop, wt string)
		want    setupCause
		changed []string
		why     string
	}{
		{name: "no setup", h: lock, want: dueNone},
		{name: "set up, nothing it reads changed", cmd: "npm ci", h: none, want: dueNone},
		{name: "its lockfile changed", cmd: "npm ci", h: lock, want: dueChanged, changed: []string{"package-lock.json"},
			why: "package-lock.json changed since its dependencies were last set up"},
		{name: "git can't say", cmd: "npm ci", h: changedFilesSay{err: errors.New("bad object")}, want: dueUnknown,
			why: "git could not say what changed since its dependencies were last set up"},
		{name: "the last setup failed", cmd: "npm ci", h: none, want: dueFailed,
			prepare: func(_ *Loop, wt string) { runFile(t, wt, setupLogName); runFile(t, wt, setupMarkName) },
			why:     "the last setup in its worktree failed"},
		{name: "the last setup was cut short", cmd: "npm ci", h: none, want: dueCutShort,
			prepare: func(_ *Loop, wt string) { runFile(t, wt, setupMarkName) },
			why:     "the last setup in its worktree didn't finish"},
		{name: "a setup in this run broke, leaving no file", cmd: "npm ci", h: none, want: dueBroken,
			prepare: func(o *Loop, _ string) { o.book.setupBroke("A") },
			why:     "the last setup in its worktree failed or didn't finish"},
		{name: "set up again since it broke", cmd: "npm ci", h: none, want: dueNone,
			prepare: func(o *Loop, _ string) { o.book.setupBroke("A"); o.book.setUp("A", "c1") }},
		{name: "a new worktree since it broke", cmd: "npm ci", h: none, want: dueNone,
			prepare: func(o *Loop, _ string) { o.book.setupBroke("A"); o.book.freshWorktree("A") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o, wt := depsLoop(t, tc.cmd, tc.h)
			if tc.prepare != nil {
				tc.prepare(o, wt)
			}
			due := o.depsOf("A", wt).due(t.Context(), "c0", "c2")
			if due.cause != tc.want || !slices.Equal(due.changed, tc.changed) {
				t.Errorf("due = %+v, want %v with %q", due, tc.want, tc.changed)
			}
			if got := due.why(); got != tc.why {
				t.Errorf("why = %q, want %q", got, tc.why)
			}
		})
	}
}

// A setup that fails leaves its mark and has the next due run it again, with nothing it reads changed;
// one that works records what the dependencies are set up for and removes the mark and the output of
// the one that failed.
func TestSetupRunKeepsItsState(t *testing.T) {
	t.Parallel()
	o, wt := depsLoop(t, "exit 1", changedFilesSay{files: []string{"src/a.ts"}})
	run, _ := o.depsOf("A", wt).run(t.Context(), "c1")
	run.out.close()
	if run.err == nil {
		t.Fatal("'exit 1' worked")
	}
	if !setupCutShortIn(wt) {
		t.Errorf("a failed setup left no %s", setupMarkName)
	}
	runFile(t, wt, setupLogName) // as its caller keeps its output
	if due := o.depsOf("A", wt).due(t.Context(), "c0", "c1"); due.cause != dueFailed {
		t.Errorf("after a failed setup, due = %+v, want it run again as it failed", due)
	}

	o.setupSet = &setupConf{cmd: "true", files: []string{"package-lock.json"}}
	run, _ = o.depsOf("A", wt).run(t.Context(), "c2")
	run.out.close()
	if run.err != nil {
		t.Fatal(run.err)
	}
	if setupFailedIn(wt) || setupCutShortIn(wt) {
		t.Errorf("a setup that worked left %s or %s", setupLogName, setupMarkName)
	}
	if o.book.brokenSetup("A") {
		t.Error("a setup that worked left the dependencies marked broken")
	}
	if from := o.book.setUpSince("A", "c0"); from != "c2" {
		t.Errorf("set up since %q, want c2, the commit it ran for", from)
	}
	if due := o.depsOf("A", wt).due(t.Context(), "c0", "c3"); due.cause != dueNone {
		t.Errorf("after it worked, with nothing it reads changed, due = %+v, want none", due)
	}
}
