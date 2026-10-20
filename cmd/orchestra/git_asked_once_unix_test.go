//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// loggingGit puts a git on PATH that writes each call's arguments as a line of the file it returns,
// and passes them on to the real git.
func loggingGit(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "git-calls")
	faketool.Write(t, dir, "git", `#!/bin/sh
echo "$*" >> '`+calls+`'
exec '`+gitPath+`' "$@"
`)
	onPath(t, dir)
	return calls
}

// A run asks git where the repository is once, however many of its steps need to know: its top
// folder, whether it is the main checkout, and the git directory the run lock and the exclusion of
// .orchestra/run/ are in.
func TestARunAsksGitWhereTheRepositoryIsOnce(t *testing.T) {
	t.Parallel()
	featureTools(t)
	setenv(t, "HERDR_PANE_ID", "w2B:p61")
	calls := loggingGit(t)
	_, stdout, stderr, code := runFeatureIn(t, "--plain", "--ticket", "f-1")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	b, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	var where []string
	for l := range strings.Lines(string(b)) {
		for _, flag := range []string{"--show-toplevel", "--absolute-git-dir", "--git-common-dir"} {
			if strings.HasPrefix(l, "rev-parse ") && strings.Contains(l, flag) {
				where = append(where, strings.TrimSpace(l))
				break
			}
		}
	}
	if len(where) != 1 {
		t.Errorf("git was asked where the repository is %d times, want once:\n%s", len(where), strings.Join(where, "\n"))
	}
}
