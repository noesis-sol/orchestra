package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeRunner writes script as repo's scripts/check-fast.sh.
func writeRunner(t *testing.T, repo, script string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, FastRunner), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runRunner runs repo's check-fast.sh with sh (a new executable would wait on macOS's first-run
// scan), from dir, with env added and the temp directory at tmp, within limit.
func runRunner(t *testing.T, repo, dir, tmp string, limit time.Duration, env ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(repo, FastRunner))
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "TMPDIR="+tmp), env...)
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), ctx.Err()
	}
	return string(out), err
}

func TestRunnerListsEachSuiteUnderItsComment(t *testing.T) {
	script := FastScript([]Suite{
		{Name: "unit tests", Command: " npm test ", FoundIn: "package.json:7"},
		{Name: "e2e", Command: "npx playwright test", FoundIn: "playwright.config.ts:1", Serial: true},
		{Command: "make lint", FoundIn: "--check-fast"},
	})
	for _, want := range []string{
		"#!/bin/sh\n# " + FastRunner + ": orchestra's merge check",
		"orchestra init wrote it; it is the project's to edit",
		"\nset -e\ncd \"$(dirname \"$0\")/..\"\n",
		"\n# unit tests, from package.json:7\nnpm test\n",
		"\n# e2e, from playwright.config.ts:1, one worktree at a time\nlock e2e\nnpx playwright test\nunlock\n",
		"\n# from --check-fast\nmake lint\n",
		"\nlock() {", "\ntrap unlock EXIT\n",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("the runner lacks %q:\n%s", want, script)
		}
	}
	// No suite behind the lock: no lock functions.
	if script := FastScript([]Suite{{Command: "npm test"}}); strings.Contains(script, "lock") {
		t.Errorf("lock functions without a serial suite:\n%s", script)
	}
	full := FullScript([]Suite{{Name: "integration", Command: "make integration", FoundIn: "Makefile:12"}}, false)
	if !strings.Contains(full, "# "+FullRunner+": every check") ||
		!strings.Contains(full, "\n# from the merge check\n"+FastRunner+"\n\n# integration, from Makefile:12\nmake integration\n") {
		t.Errorf("check-full runs check-fast first, then its own suites:\n%s", full)
	}
	if got := lockName(Suite{Command: "docker compose run --rm e2e"}); got != "docker-compose-run-rm-e2e" {
		t.Errorf("lock name from a command: %q", got)
	}
}

func TestRunnerWithoutSuitesExitsZero(t *testing.T) {
	for name, script := range map[string]string{
		"check-fast":            FastScript(nil),
		"check-fast, blank":     FastScript([]Suite{{Command: "  "}}),
		"check-full, both none": FullScript(nil, true),
	} {
		if !strings.HasSuffix(script, "# It checks nothing yet: add a line for each suite.\nexit 0\n") ||
			strings.Contains(script, "set -e") {
			t.Errorf("%s:\n%s", name, script)
		}
		repo := t.TempDir()
		writeRunner(t, repo, script)
		if out, err := runRunner(t, repo, repo, t.TempDir(), 10*time.Second); err != nil {
			t.Errorf("%s: %v\n%s", name, err, out)
		}
	}
	// check-full with no suites of its own still runs a check-fast that has some.
	if full := FullScript(nil, false); !strings.HasSuffix(full, "\n"+FastRunner+"\n") {
		t.Errorf("check-full without its own suites:\n%s", full)
	}
}

func TestRunnerRunsFromTheRootAndStopsAtAFailingSuite(t *testing.T) {
	repo := t.TempDir()
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRunner(t, repo, FastScript([]Suite{
		{Command: "pwd -P > ran"}, {Command: "exit 3"}, {Command: "touch never"},
	}))
	_, err := runRunner(t, repo, sub, t.TempDir(), 10*time.Second)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("the failing suite's exit: %v", err)
	}
	root, _ := filepath.EvalSymlinks(repo)
	if got := strings.TrimSpace(read(t, filepath.Join(repo, "ran"))); got != root {
		t.Errorf("ran in %s, want the root %s", got, root)
	}
	if _, err := os.Stat(filepath.Join(repo, "never")); err == nil {
		t.Error("a suite ran after one failed")
	}
}

// lockedRunner writes a check-fast.sh whose one suite runs command behind the lock "it".
func lockedRunner(t *testing.T, repo, command string) {
	t.Helper()
	writeRunner(t, repo, FastScript([]Suite{{Name: "it", Command: command, Serial: true}}))
}

// lockPath finds the lock's path in tmp, from inside a suite holding it.
func lockPath(t *testing.T, repo, tmp string) string {
	t.Helper()
	lockedRunner(t, repo, `ls "$TMPDIR" > held`)
	if out, err := runRunner(t, repo, repo, tmp, 10*time.Second); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	name := strings.TrimSpace(read(t, filepath.Join(repo, "held")))
	if !strings.HasSuffix(name, "-it.lock") || strings.Contains(name, "\n") {
		t.Fatalf("the lock held: %q", name)
	}
	if _, err := os.Stat(filepath.Join(tmp, name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the lock outlived the runner: %v", err)
	}
	return filepath.Join(tmp, name)
}

func TestLockIsReleasedWhenTheSuiteFails(t *testing.T) {
	repo, tmp := t.TempDir(), t.TempDir()
	lock := lockPath(t, repo, tmp)
	lockedRunner(t, repo, "exit 4")
	_, err := runRunner(t, repo, repo, tmp, 10*time.Second)
	if exit := (*exec.ExitError)(nil); !errors.As(err, &exit) || exit.ExitCode() != 4 {
		t.Errorf("the suite's exit: %v", err)
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock is still held: %v", err)
	}
}

func TestLockOfADeadHolderIsTakenOver(t *testing.T) {
	repo, tmp := t.TempDir(), t.TempDir()
	lock := lockPath(t, repo, tmp)
	dead := exec.Command("sh", "-c", "exit 0")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(dead.Process.Pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockedRunner(t, repo, "touch ran")
	if out, err := runRunner(t, repo, repo, tmp, 10*time.Second); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(repo, "ran")); err != nil {
		t.Error("the suite didn't run")
	}
	if _, err := os.Stat(lock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the lock is still held: %v", err)
	}
}

func TestWorktreesTakeTurnsOnALockedSuite(t *testing.T) {
	if testing.Short() {
		t.Skip("waits on the lock for seconds")
	}
	repo, git := gitRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	git(repo, "worktree", "add", "-q", "-b", "other", other)
	tmp, mark := t.TempDir(), filepath.Join(t.TempDir(), "in")
	// Two copies at once would both find the mark there, and one would fail.
	for _, wt := range []string{repo, other} {
		lockedRunner(t, wt, `mkdir "$MARK" && sleep 1 && rmdir "$MARK"`)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	outs := make([]string, 2)
	start := time.Now()
	for i, wt := range []string{repo, other} {
		wg.Go(func() { outs[i], errs[i] = runRunner(t, wt, wt, tmp, 30*time.Second, "MARK="+mark) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("worktree %d: %v\n%s", i, err, outs[i])
		}
	}
	if took := time.Since(start); took < 2*time.Second {
		t.Errorf("both ran in %s: they didn't take turns", took)
	}

	// A live holder keeps it: a runner waits on it.
	lock := lockPath(t, repo, tmp)
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lock, "pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	lockedRunner(t, other, "touch ran")
	if _, err := runRunner(t, other, other, tmp, 2*time.Second); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ran while another held the lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other, "ran")); err == nil {
		t.Error("the suite ran while another held the lock")
	}
}

func TestApplyRunnersWritesKeepsAndReplaces(t *testing.T) {
	repo := t.TempDir()
	suites := &[]Suite{{Name: "unit tests", Command: "npm test", FoundIn: "package.json:7"}}
	c := Choice{Fast: suites, Full: &[]Suite{}}
	steps, err := ApplyRunners(repo, c)
	if err != nil {
		t.Fatal(err)
	}
	for i, path := range []string{FastRunner, FullRunner} {
		fi, err := os.Stat(filepath.Join(repo, path))
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v, %v", path, fi, err)
		}
		if steps[i].Kind != StepDone || steps[i].Detail != "wrote "+path || len(steps[i].Commit) != 1 {
			t.Errorf("%s: %+v", path, steps[i])
		}
	}
	if got := read(t, filepath.Join(repo, FastRunner)); got != FastScript(*suites) {
		t.Errorf("check-fast.sh:\n%s", got)
	}
	// Again, the same: kept, nothing to commit.
	steps, _ = ApplyRunners(repo, c)
	if steps[0].Kind != StepKept || steps[1].Kind != StepKept || len(steps[0].Commit) != 0 {
		t.Errorf("again: %+v", steps)
	}

	// The project edited check-fast.sh: init reports it differs and keeps it.
	edited := FastScript(*suites) + "\nmake e2e\n"
	if err := os.WriteFile(filepath.Join(repo, FastRunner), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	runners, _ := PlanRunners(repo, c)
	if !runners[0].Exists || !runners[0].Differs || runners[1].Differs {
		t.Errorf("plan: %+v", runners)
	}
	steps, _ = ApplyRunners(repo, c)
	if steps[0].Kind != StepCaution || !strings.Contains(steps[0].Detail, "differs from what init would write; kept") ||
		read(t, filepath.Join(repo, FastRunner)) != edited {
		t.Errorf("a differing runner: %+v", steps[0])
	}
	// Fast nil keeps it as it is, without a word about differing.
	if steps, _ = ApplyRunners(repo, Choice{Full: &[]Suite{}}); steps[0].Kind != StepKept {
		t.Errorf("nil keeps it: %+v", steps[0])
	}
	// Replacing it writes it, mode 755 again.
	c.ReplaceFast = true
	steps, _ = ApplyRunners(repo, c)
	fi, _ := os.Stat(filepath.Join(repo, FastRunner))
	if steps[0].Kind != StepDone || steps[0].Detail != "replaced "+FastRunner || fi.Mode().Perm() != 0o755 ||
		read(t, filepath.Join(repo, FastRunner)) != FastScript(*suites) {
		t.Errorf("replaced: %+v, %v", steps[0], fi.Mode())
	}

	// Nothing there and nothing to write it from: a runner that checks nothing.
	empty := t.TempDir()
	steps, _ = ApplyRunners(empty, Choice{})
	if steps[0].Kind != StepDone || !strings.Contains(steps[0].Detail, "checks nothing yet") ||
		read(t, filepath.Join(empty, FastRunner)) != FastScript(nil) {
		t.Errorf("none: %+v", steps)
	}
	// check-full without suites, when check-fast's are unknown, still runs check-fast.
	if got := read(t, filepath.Join(empty, FullRunner)); got != FullScript(nil, false) {
		t.Errorf("check-full:\n%s", got)
	}
}

func TestOldCheckSettingsAreReadAsCheckFast(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(SettingsPath(repo), []byte(`{"check": "make check", "check_timeout": "5m"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, err := LoadSettings(repo)
	if err != nil || s.CheckFast != "make check" || s.CheckFastTimeout != "5m" {
		t.Fatalf("%+v, %v", s, err)
	}
	if d, err := ResolveCheckTimeout(0, false, s); err != nil || d != 5*time.Minute {
		t.Errorf("a run's check time limit: %s, %v", d, err)
	}
	c := DefaultChoice(s, "")
	if c.FastCommand() != "make check" || c.CheckFastTimeout != "5m" {
		t.Errorf("init starts from it: %+v", c)
	}
	// Saved, the settings take the new names.
	if err := SaveSettings(repo, s); err != nil {
		t.Fatal(err)
	}
	if raw := read(t, SettingsPath(repo)); !strings.Contains(raw, `"check_fast": "make check"`) ||
		!strings.Contains(raw, `"check_fast_timeout": "5m"`) || strings.Contains(raw, `"check"`) ||
		strings.Contains(raw, `"check_timeout"`) {
		t.Errorf("settings.json:\n%s", raw)
	}
	// An invalid old time limit is named as the settings name it.
	if _, err := ResolveCheckTimeout(0, false, Settings{CheckTimeout: "soon", CheckFastTimeout: "soon"}); err == nil ||
		!strings.Contains(err.Error(), "check_timeout must be") {
		t.Errorf("the old name: %v", err)
	}
	// The new names win over the old.
	both := `{"check": "make check", "check_fast": "` + FastRunner + `", "check_timeout": "5m", "check_fast_timeout": "7m"}`
	if err := os.WriteFile(SettingsPath(repo), []byte(both), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, _, _ = LoadSettings(repo); s.CheckFast != FastRunner || s.CheckFastTimeout != "7m" {
		t.Errorf("both: %+v", s)
	}
	if c := DefaultChoice(s, ""); c.Fast != nil {
		t.Errorf("check_fast naming the runner leaves it as it is: %+v", c.Fast)
	}
	if d, err := ResolveCheckFullTimeout(0, false, Settings{}); err != nil || d != DefaultCheckFullTimeout {
		t.Errorf("check-full's default: %s, %v", d, err)
	}
	if d, err := ResolveCheckFullTimeout(0, false, Settings{CheckFullTimeout: "90m"}); err != nil || d != 90*time.Minute {
		t.Errorf("check_full_timeout: %s, %v", d, err)
	}
	if _, err := ResolveCheckFullTimeout(0, true, Settings{}); err == nil ||
		!strings.Contains(err.Error(), "--check-full-timeout must be") {
		t.Errorf("--check-full-timeout 0: %v", err)
	}
}
