// Package gittest is for tests that run the real git: Main keeps the git they start, theirs and
// orchestra's, from working on in their repositories in the background once it has exited.
//
// After a commit, a merge or a rebase, git runs `git maintenance run --auto --detach`, which goes on
// without it. Since git 2.54 that maintenance repacks a repository once objects/17 holds two loose
// objects, which a test's few commits now and then put there, and a repack still writing in a test's
// repository as the test ends breaks TempDir's removal: "unlinkat .../.git/objects: directory not
// empty".
//
// Main also gives that git an identity, for the commits orchestra's rebases and merges make: a
// machine without one of its own, such as CI's, would otherwise stop them at "Committer identity
// unknown".
//
// On macOS, where /usr/bin/git is the shim that asks xcrun for the developer tools' git on every call, Main puts that
// git ahead of the shim on PATH, as the shim tripled the cost of each of the thousands of git commands a package's
// tests start.
package gittest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// TestingM is a package's tests, as its TestMain gets them: a *testing.M.
type TestingM interface{ Run() int }

// Main returns m set to run with git's automatic maintenance off, an identity and no shim (above). A package whose
// tests run the real git runs them through it, from its TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(gittest.Main(m).Run()) }
func Main(m TestingM) TestingM { return tests{m} }

type tests struct{ m TestingM }

func (ts tests) Run() int {
	for _, kv := range [][2]string{{"maintenance.auto", "false"}, {"user.name", "t"}, {"user.email", "t@t"}} {
		if err := setConfig(kv[0], kv[1]); err != nil {
			fmt.Fprintln(os.Stderr, "gittest:", err)
			return 1
		}
	}
	dir, err := bypassShim()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gittest:", err)
		return 1
	}
	if dir != "" {
		defer func() { _ = os.RemoveAll(dir) }() // a link in a temporary folder: a leftover is no failure
	}
	return ts.m.Run()
}

// shim is macOS's git: a stub that asks xcrun where the developer tools are, on every call, and runs
// their git. It takes about 16ms a call on an idle machine, the git it runs 5ms.
const shim = "/usr/bin/git"

// bypassShim puts the git the shim runs ahead of it on PATH, for the tests and every process they
// start: a new folder holding only a link to it, rather than its own folder, which holds the
// developer tools' other commands. It returns that folder, or "" where git on PATH isn't the shim or
// xcrun can't say which git it runs, which leaves the shim to fail as it would.
func bypassShim() (string, error) {
	if runtime.GOOS != "darwin" {
		return "", nil
	}
	if path, err := exec.LookPath("git"); err != nil || path != shim {
		return "", nil // no git, or another: nothing to bypass
	}
	out, err := command.Output(context.Background(), command.ReadLimit, "", "xcrun", "--find", "git")
	git := strings.TrimSpace(out)
	if err != nil || !filepath.IsAbs(git) || git == shim {
		return "", nil // the shim stays, and fails as it would
	}
	dir, err := os.MkdirTemp("", "gittest")
	if err != nil {
		return "", err
	}
	if err := errors.Join(os.Symlink(git, filepath.Join(dir, "git")),
		os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))); err != nil {
		_ = os.RemoveAll(dir) // as above
		return "", err
	}
	return dir, nil
}

// setConfig sets name to value for every git started from here on, through GIT_CONFIG_COUNT, after
// the entries the environment already holds. It overrides the repositories' and the user's settings.
func setConfig(name, value string) error {
	n := 0
	if s := os.Getenv("GIT_CONFIG_COUNT"); s != "" {
		var err error
		if n, err = strconv.Atoi(s); err != nil {
			return fmt.Errorf("GIT_CONFIG_COUNT=%q: %w", s, err)
		}
	}
	i := strconv.Itoa(n)
	return errors.Join(
		os.Setenv("GIT_CONFIG_KEY_"+i, name),
		os.Setenv("GIT_CONFIG_VALUE_"+i, value),
		os.Setenv("GIT_CONFIG_COUNT", strconv.Itoa(n+1)),
	)
}
