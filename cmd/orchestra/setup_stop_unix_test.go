//go:build unix

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// stallGit puts first on the PATH a git that, the first time it runs in a checkout with a worker
// prompt, as Apply's first git call does once it has written it, waits until release is there. It
// returns stalled, the file it makes as it starts to wait, and release.
func stallGit(t *testing.T) (stalled, release string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	stalled, release = filepath.Join(dir, "stalled"), filepath.Join(dir, "release")
	faketool.Write(t, dir, "git", `#!/bin/sh
if [ -e .orchestra/worker-prompt.md ] && [ ! -e '`+stalled+`' ]; then
	: > '`+stalled+`'
	while [ ! -e '`+release+`' ]; do sleep 0.01; done
fi
exec '`+gitPath+`' "$@"
`)
	onPath(t, dir)
	return stalled, release
}

// stopWhileStalled waits for the git of stallGit to stall, then stops what it stalls with stop, as a
// stop signal ends the context, and lets git go on.
func stopWhileStalled(t *testing.T, stalled, release string, stop func()) {
	t.Helper()
	for deadline := time.Now().Add(patience); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(stalled); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("git never stalled in a checkout with a worker prompt")
		}
	}
	stop()
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertWholeSetUp fails the test unless the repository's setup is whole: once IsSetUp says it is, a
// run takes it as it is, and one without the settings would merge every ticket unchecked.
func assertWholeSetUp(t *testing.T, repo string) {
	t.Helper()
	if !project.IsSetUp(repo) {
		t.Fatal("not set up")
	}
	for _, p := range []string{project.FastRunner, project.FullRunner, filepath.Join(project.Dir, ".gitignore")} {
		if _, err := os.Stat(filepath.Join(repo, p)); err != nil {
			t.Errorf("set up without %s: %v", p, err)
		}
	}
	if s, _, err := project.LoadSettings(repo); err != nil || s.CheckFast != project.FastRunner {
		t.Errorf("set up with settings %+v (%v), check_fast not %s", s, err, project.FastRunner)
	}
}

// A stop while init writes the setup, once it has written the worker prompt, lets it write the rest,
// so that it isn't taken for set up half written; init then says it stopped, exit code 130.
func TestInitStoppedWhileWritingItsSetUp(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra init on real git with fake tools")
	}
	fakeBeadsTools(t, true)
	repo, _ := gitRepo(t)
	stalled, release := stallGit(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := map[string]string{"HOME": t.TempDir()}
	var out, errOut strings.Builder
	done := make(chan int, 1)
	go func() {
		done <- runInit(testCtx(ctx, t), repo, []string{"--check", "make check"}, func(k string) string { return env[k] },
			strings.NewReader(""), &out, &errOut)
	}()
	stopWhileStalled(t, stalled, release, cancel)
	var code int
	select {
	case code = <-done:
	case <-time.After(patience):
		t.Fatal("init didn't end")
	}
	if code != dispatch.ExitInterrupted {
		t.Errorf("exit %d, want %d", code, dispatch.ExitInterrupted)
	}
	if want := "orchestra init: stopped once it had written the files above\n"; errOut.String() != want {
		t.Errorf("stderr:\n%s\nwant:\n%s", errOut.String(), want)
	}
	plain := strings.Join(strings.Fields(out.String()), " ")
	for _, want := range []string{"wrote .orchestra/worker-prompt.md from the template", "wrote scripts/check-fast.sh",
		"1 at the same time"} {
		if !strings.Contains(plain, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out.String())
		}
	}
	assertWholeSetUp(t, repo)
}

// A stop while a spec run sets the repository up, once it has written the worker prompt, lets it
// write the rest and commits nothing: the run says it stopped, exit code 130.
func TestSpecRunStoppedWhileSettingTheRepositoryUp(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, _ := emptyRepoFixture(t)
	useBreakdown(t, &fakeBreakdown{repo: repo, root: "s-1"})
	stalled, release := stallGit(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out, errOut strings.Builder
	done := make(chan int, 1)
	go func() {
		err := run(testCtx(ctx, t), []string{"orchestra", "--spec", spec, "--plain"},
			testCmdEnv(t, strings.NewReader(""), &out, &errOut))
		done <- exitOf(err)
	}()
	stopWhileStalled(t, stalled, release, cancel)
	var code int
	select {
	case code = <-done:
	case <-time.After(patience):
		t.Fatal("the run didn't end")
	}
	if code != dispatch.ExitInterrupted {
		t.Errorf("exit %d, want %d; stderr:\n%s", code, dispatch.ExitInterrupted, errOut.String())
	}
	if want := "orchestra: stopped while setting the repository up for the spec; nothing was committed"; !strings.Contains(
		errOut.String(), want) {
		t.Errorf("stderr lacks %q:\n%s", want, errOut.String())
	}
	if log := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(log,
		"SPEC: stopped while setting the repository up") {
		t.Errorf("the log:\n%s", log)
	}
	if head, err := command.Output(context.Background(), 0, repo, "git", "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		t.Errorf("committed %s", head)
	}
	assertWholeSetUp(t, repo)
}
