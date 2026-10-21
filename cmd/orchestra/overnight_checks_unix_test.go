//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's checks that refuse to start the run, each failing and passing, and its
// fix made where it offers one.

// checkSession is the session of an overnight run given args, env and the project's settings, for a
// pre-flight check to look at.
func checkSession(t *testing.T, args []string, env map[string]string, settings string) *session {
	t.Helper()
	c, _, problems := settle(t, append([]string{"--overnight"}, args...), env, settings)
	if len(problems) > 0 {
		t.Fatalf("problems: %q", problems)
	}
	var out strings.Builder
	return &session{cfg: c, e: cmdEnv{getenv: func(k string) string { return env[k] },
		stdin: strings.NewReader(""), stdout: &out, stderr: &out}}
}

// wantResult fails t unless r has verdict v and text that has each of texts.
func wantResult(t *testing.T, r preflightResult, v tui.CheckVerdict, texts ...string) {
	t.Helper()
	if r.verdict != v {
		t.Errorf("the check %s, want %s: %s", r.verdict, v, r.text)
	}
	for _, text := range texts {
		if !strings.Contains(r.text, text) {
			t.Errorf("the check says %q, want it to say %q", r.text, text)
		}
	}
}

// fixWith makes r's fix in s, failing t where r offers none, or where it asks question, when given, not.
func fixWith(t *testing.T, s *session, r preflightResult, question string) {
	t.Helper()
	if r.fix == nil {
		t.Fatalf("the check offers no fix: %s", r.text)
	}
	if question != "" && r.fix.question != question {
		t.Errorf("the fix asks %q, want %q", r.fix.question, question)
	}
	if err := r.fix.apply(s); err != nil {
		t.Fatal(err)
	}
}

func TestLimitCheck(t *testing.T) {
	t.Parallel()
	if r := limitCheck(runCtx(t), checkSession(t, nil, nil, "")); r.verdict != tui.CheckPassed || r.fix != nil {
		t.Errorf("a run with no limit: %s, %s", r.verdict, r.text)
	}
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		says string
	}{
		{"flag", []string{"--limit", "10"}, nil, "--limit 10 is set: the run would stop after 10 tickets"},
		{"variable", nil, map[string]string{"LIMIT": "1"}, "LIMIT=1 is set: the run would stop after 1 ticket"},
		{"done so far", []string{"--limit", "10", "--done-so-far", "3"}, nil,
			"--limit 10 is set, with DONE_SO_FAR=3: the run would stop after 7 more"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := checkSession(t, tc.args, tc.env, "")
			r := limitCheck(runCtx(t), s)
			wantResult(t, r, tui.CheckFailed, tc.says)
			fixWith(t, s, r, "Run without a limit?")
			if s.cfg.Limit != dispatch.NoLimit {
				t.Errorf("the fix left the limit at %d", s.cfg.Limit)
			}
		})
	}
}

func TestConcurrencyCheck(t *testing.T) {
	t.Parallel()
	wantResult(t, concurrencyCheck(runCtx(t), checkSession(t, []string{"--concurrent", "3"}, nil, "")),
		tui.CheckPassed, "3 tickets at a time")
	s := checkSession(t, nil, nil, `{"concurrent": 1}`)
	r := concurrencyCheck(runCtx(t), s)
	wantResult(t, r, tui.CheckFailed, "1 ticket at a time", "one slow ticket would hold up the night")
	n := offeredConcurrency(runtime.NumCPU(), 0)
	fixWith(t, s, r, "Run "+ticketCount(n)+" at a time?")
	if s.cfg.Concurrency != n {
		t.Errorf("the fix set concurrency %d, want %d", s.cfg.Concurrency, n)
	}
}

func TestOfferedConcurrency(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ cores, machineWorkers, want int }{
		{1, 0, 2}, {4, 0, 2}, {12, 0, 3}, {16, 0, 4}, {64, 0, 4},
		{64, 3, 3}, {64, 1, 2}, // the machine's cap, but never fewer than 2
	} {
		if got := offeredConcurrency(tc.cores, tc.machineWorkers); got != tc.want {
			t.Errorf("offeredConcurrency(%d, %d) = %d, want %d", tc.cores, tc.machineWorkers, got, tc.want)
		}
	}
}

func TestPermissionCheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		args     []string
		settings string
		verdict  tui.CheckVerdict
		says     string
	}{
		{"Claude Code's default", nil, "", tui.CheckFailed, "workers start in Claude Code's default permission mode"},
		{"default", nil, `{"worker_permission_mode": "default"}`, tui.CheckFailed,
			"workers start in default permission mode (worker_permission_mode"},
		{"manual", []string{"--worker-permission-mode", "manual"}, "", tui.CheckFailed,
			"workers start in manual permission mode"},
		{"auto", nil, `{"worker_permission_mode": "auto"}`, tui.CheckPassed, "auto permission mode"},
		{"acceptEdits", []string{"--worker-permission-mode", "acceptEdits"}, "", tui.CheckPassed,
			"acceptEdits permission mode"},
		{"another agent", []string{"--agent", "codex"}, "", tui.CheckPassed, "workers are codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := checkSession(t, tc.args, nil, tc.settings)
			r := permissionCheck(runCtx(t), s)
			wantResult(t, r, tc.verdict, tc.says)
			if tc.verdict == tui.CheckPassed {
				return
			}
			fixWith(t, s, r, "Start workers in auto permission mode?")
			if s.cfg.settleWork(); s.cfg.WorkerPermissionMode != project.PermissionAuto {
				t.Errorf("after the fix workers start in %q", s.cfg.WorkerPermissionMode)
			}
		})
	}
}

func TestClaudeCheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, script string
		verdict      tui.CheckVerdict
		says         string
	}{
		{"logged in", `echo '{"loggedIn": true, "authMethod": "claude.ai"}'`, tui.CheckPassed,
			"claude starts, logged in (claude.ai)"},
		{"logged out", `echo '{"loggedIn": false}'; exit 1`, tui.CheckFailed,
			"claude isn't logged in: no worker could start. Log in with 'claude auth login'"},
		{"failing", `echo 'error: unknown command auth' >&2; exit 1`, tui.CheckFailed,
			"claude couldn't say whether it is logged in"},
		{"saying nothing", `echo '{}'`, tui.CheckFailed, "its status said nothing of a login"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			bin := faketool.Write(t, dir, "claude", "#!/bin/sh\necho \"$*\" > '"+dir+"/args'\n"+tc.script+"\n")
			r := claudeCheck(bin)(runCtx(t), checkSession(t, nil, nil, ""))
			wantResult(t, r, tc.verdict, tc.says)
			if args := read(t, filepath.Join(dir, "args")); args != "auth status --json\n" {
				t.Errorf("claude was run with %q", args)
			}
		})
	}
	r := claudeCheck(filepath.Join(t.TempDir(), "claude"))(runCtx(t), checkSession(t, nil, nil, ""))
	wantResult(t, r, tui.CheckFailed, "claude isn't on the PATH")
}

func TestDiskCheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, settings string
		free           uint64
		err            error
		verdict        tui.CheckVerdict
		says           string
	}{
		{"enough", "", 50 * gb, nil, tui.CheckPassed, "50 GB free where the worktrees go, 20 GB wanted"},
		{"short", "", 12*gb + gb/2, nil, tui.CheckFailed, "12 GB free in "},
		{"the setting's", `{"overnight_free_disk_gb": 10}`, 12 * gb, nil, tui.CheckPassed, "10 GB wanted"},
		{"short of the setting's", `{"overnight_free_disk_gb": 100}`, 50 * gb, nil, tui.CheckFailed,
			"under the 100 GB an overnight run wants (overnight_free_disk_gb in settings.json)"},
		{"not checked", `{"overnight_free_disk_gb": 0}`, 0, nil, tui.CheckPassed, "free disk isn't checked"},
		{"can't tell", "", 0, errors.ErrUnsupported, tui.CheckWarned, "orchestra couldn't tell how much disk is free"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := checkSession(t, nil, nil, tc.settings)
			s.cfg.WTRoot = filepath.Join(t.TempDir(), "worktrees", "repo") // made as the first ticket starts
			var asked string
			r := diskCheck(func(path string) (uint64, error) {
				asked = path
				return tc.free, tc.err
			})(runCtx(t), s)
			wantResult(t, r, tc.verdict, tc.says)
			if _, err := os.Stat(asked); tc.settings != `{"overnight_free_disk_gb": 0}` && err != nil {
				t.Errorf("the free disk was asked of %q: %v", asked, err)
			}
		})
	}
}

// The free disk is read from the file system itself.
func TestFreeDisk(t *testing.T) {
	t.Parallel()
	if n, err := freeDisk(t.TempDir()); err != nil || n == 0 {
		t.Errorf("freeDisk: %d, %v", n, err)
	}
}

// The merge check runs on the base branch's head in a worktree of its own, and fails the run where it fails.
func TestBaseCheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, check string
		verdict     tui.CheckVerdict
		says        []string
	}{
		{"passing", "true", tui.CheckPassed, []string{"the merge check 'true' passes on main at "}},
		{"failing", "echo '--- FAIL: TestMerge'; false", tui.CheckFailed, []string{
			"the merge check 'echo '--- FAIL: TestMerge'; false' fails on main at ",
			"so every ticket's merge would fail: --- FAIL: TestMerge (the whole output: ",
			dispatch.BaseCheckLogName + "). Fix it on main first"}},
		{"none", "", tui.CheckPassed, []string{"no merge check (check_fast) to run on main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := checkSession(t, nil, nil, "")
			s.cfg.Repo, _ = gitRepo(t)
			s.cfg.Base, s.cfg.WTRoot, s.cfg.Check = "main", t.TempDir(), tc.check
			wantResult(t, baseCheck(runCtx(t), s), tc.verdict, tc.says...)
			if left, err := os.ReadDir(s.cfg.WTRoot); err != nil || len(left) > 0 {
				t.Errorf("the merge check's worktree was left: %v (%v)", left, err)
			}
		})
	}
}
