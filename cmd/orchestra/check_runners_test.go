package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// settingsOf reads repo's settings.json as it is on disk.
func settingsOf(t *testing.T, repo string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(read(t, project.SettingsPath(repo))), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInitWritesTheRunnersFromTheCheckFlags(t *testing.T) {
	repo := initRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "-c", "1", "--check-fast", "make unit",
		"--check-full", "make e2e", "--check-fast-timeout", "2m", "--check-full-timeout", "90m")
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	m := settingsOf(t, repo)
	if m["check_fast"] != project.FastRunner || m["check_full"] != project.FullRunner ||
		m["check_fast_timeout"] != "2m" || m["check_full_timeout"] != "1h30m" || m["check"] != nil {
		t.Errorf("settings.json = %v", m)
	}
	if got := read(t, filepath.Join(repo, project.FastRunner)); got != project.FastScript([]project.Suite{
		{Command: "make unit", FoundIn: "--check-fast"}}) {
		t.Errorf("check-fast.sh:\n%s", got)
	}
	if got := read(t, filepath.Join(repo, project.FullRunner)); !strings.HasSuffix(got,
		"\n"+project.FastRunner+"\n\n# from --check-full\nprintf '%s\\n' 'SUITE: make e2e'\nmake e2e\n") {
		t.Errorf("check-full.sh:\n%s", got)
	}
	plain := strings.Join(strings.Fields(stdout), " ")
	for _, want := range []string{"wrote " + project.FastRunner, "check-fast: make unit, stopped after 2m",
		"check-full at the end of a run, stopped after 1h30m"} {
		if !strings.Contains(plain, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}

	// --check and --check-timeout are the fast ones' aliases.
	if _, stderr, err = runIn(t, repo, nil, "init", "--check", "make all", "--check-timeout", "3m"); err != nil {
		t.Fatalf("aliases: %v\n%s", err, stderr)
	}
	if m = settingsOf(t, repo); m["check_fast_timeout"] != "3m" || m["check_full_timeout"] != "1h30m" ||
		!strings.Contains(read(t, filepath.Join(repo, project.FastRunner)), "\nmake all\n") {
		t.Errorf("aliases: %v\n%s", m, read(t, filepath.Join(repo, project.FastRunner)))
	}

	// --check-fast "" writes a runner that checks nothing.
	stdout, _, err = runIn(t, repo, nil, "init", "--check-fast", "")
	if got := read(t, filepath.Join(repo, project.FastRunner)); err != nil || got != project.FastScript(nil) ||
		!strings.Contains(strings.Join(strings.Fields(stdout), " "), "check-fast checks nothing yet") {
		t.Errorf("none: %v\n%s\nstdout:\n%s", err, got, stdout)
	}

	for _, flag := range []string{"--check-full-timeout", "--check-fast-timeout", "--check-timeout"} {
		_, stderr, err = runIn(t, repo, nil, "init", flag, "0")
		if exitOf(err) != dispatch.ExitSetup || !strings.Contains(stderr, flag+" must be a positive duration") {
			t.Errorf("%s 0: exit = %d, stderr:\n%s", flag, exitOf(err), stderr)
		}
	}
}

func TestInitCallsTheProjectsCheckScript(t *testing.T) {
	repo := initRepo(t)
	script := "#!/bin/sh\necho the project checked\n"
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, project.CheckScript), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, err := runIn(t, repo, nil, "init"); err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	runner := read(t, filepath.Join(repo, project.FastRunner))
	if !strings.Contains(runner, "\n# the project's own check, from scripts/check.sh\n"+
		"printf '%s\\n' 'SUITE: the project'\\''s own check'\nscripts/check.sh\n") {
		t.Errorf("check-fast.sh doesn't call it:\n%s", runner)
	}
	if got := read(t, filepath.Join(repo, project.CheckScript)); got != script {
		t.Errorf("scripts/check.sh changed:\n%s", got)
	}
	if testing.Short() {
		return // running it runs a new executable, which macOS scans first
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", project.FastRunner)
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "SUITE: the project's own check\nthe project checked\n" {
		t.Errorf("check-fast.sh: %v\n%s", err, out)
	}
}

func TestInitReportsADifferingRunnerAndKeepsIt(t *testing.T) {
	repo := initRepo(t)
	if _, stderr, err := runIn(t, repo, nil, "init", "--check-fast", "make unit"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	runner := filepath.Join(repo, project.FastRunner)
	edited := read(t, runner) + "\n# ours\nmake lint\n"
	if err := os.WriteFile(runner, []byte(edited), 0o755); err != nil {
		t.Fatal(err)
	}
	// Init again, without the flag: check_fast names the runner, which is the project's now.
	stdout, _, err := runIn(t, repo, nil, "init")
	if err != nil || read(t, runner) != edited || strings.Contains(stdout, "differs") {
		t.Errorf("again: %v, runner:\n%s\nstdout:\n%s", err, read(t, runner), stdout)
	}
	// Init with another command reports the difference, as stage 2 will ask, and keeps it unless asked.
	runners, err := project.PlanRunners(repo, project.Choice{Fast: &[]project.Suite{{Command: "make all"}}})
	if err != nil || !runners[0].Differs || runners[1].Differs {
		t.Errorf("plan: %+v, %v", runners, err)
	}
	stdout, _, err = runIn(t, repo, nil, "init", "--check-fast", "make all")
	if err != nil || !strings.Contains(read(t, runner), "\nmake all\n") || strings.Contains(read(t, runner), "make lint") ||
		!strings.Contains(strings.Join(strings.Fields(stdout), " "), "replaced "+project.FastRunner) {
		t.Errorf("--check-fast replaces it: %v\n%s\nstdout:\n%s", err, read(t, runner), stdout)
	}

	// A runner the project wrote before init, with no setting to write it from: reported, and kept.
	other := initRepo(t)
	if err := os.MkdirAll(filepath.Join(other, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, project.FastRunner), []byte("#!/bin/sh\nmake test\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = runIn(t, other, nil, "init")
	if plain := strings.Join(strings.Fields(stdout), " "); err != nil ||
		!strings.Contains(plain, project.FastRunner+" differs from what init would write; kept") ||
		read(t, filepath.Join(other, project.FastRunner)) != "#!/bin/sh\nmake test\n" {
		t.Errorf("a project's own runner: %v\nstdout:\n%s", err, stdout)
	}
}

func TestInitMovesTheOldCheckSettingsToTheNewNames(t *testing.T) {
	repo := initRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, project.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project.SettingsPath(repo), []byte(`{"check": "make check", "check_timeout": "5m"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if stdout, stderr, err := runIn(t, repo, nil, "init"); err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	m := settingsOf(t, repo)
	if m["check_fast"] != project.FastRunner || m["check_fast_timeout"] != "5m" || m["check"] != nil || m["check_timeout"] != nil {
		t.Errorf("settings.json = %v", m)
	}
	if runner := read(t, filepath.Join(repo, project.FastRunner)); !strings.Contains(runner,
		"\n# from settings.json\nprintf '%s\\n' 'SUITE: make check'\nmake check\n") {
		t.Errorf("check-fast.sh:\n%s", runner)
	}
}

func TestConfigReadsTheChecksOldAndNew(t *testing.T) {
	configFixture(t, `{"concurrent": 1, "check": "make check", "check_timeout": "5m"}`)
	if c, p := loadWith(t); len(p) > 0 || c.Check != "make check" || c.CheckTimeout != 5*time.Minute ||
		c.CheckFull != "" || c.CheckFullTimeout != project.DefaultCheckFullTimeout {
		t.Errorf("old: %q %s %q %s %v", c.Check, c.CheckTimeout, c.CheckFull, c.CheckFullTimeout, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"check_fast": "scripts/check-fast.sh", `+
		`"check_full": "scripts/check-full.sh", "check_fast_timeout": "7m", "check_full_timeout": "2h"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.Check != project.FastRunner || c.CheckTimeout != 7*time.Minute ||
		c.CheckFull != project.FullRunner || c.CheckFullTimeout != 2*time.Hour {
		t.Errorf("new: %q %s %q %s %v", c.Check, c.CheckTimeout, c.CheckFull, c.CheckFullTimeout, p)
	}
	t.Setenv("ORCHESTRA_CHECK_FULL_TIMEOUT", "3h")
	if c, _ := loadWith(t); c.CheckFullTimeout != 3*time.Hour {
		t.Errorf("the environment overrides settings: %s", c.CheckFullTimeout)
	}
	if c, _ := loadWith(t, "--check-full-timeout", "45m"); c.CheckFullTimeout != 45*time.Minute {
		t.Errorf("--check-full-timeout overrides the environment: %s", c.CheckFullTimeout)
	}
	if _, p := loadWith(t, "--check-full-timeout", "0"); len(p) != 1 ||
		!strings.Contains(p[0], "--check-full-timeout must be a positive duration") {
		t.Errorf("zero: %v", p)
	}
	t.Setenv("ORCHESTRA_CHECK_FULL_TIMEOUT", "never")
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "ORCHESTRA_CHECK_FULL_TIMEOUT must be a positive duration") {
		t.Errorf("invalid variable: %v", p)
	}
}
