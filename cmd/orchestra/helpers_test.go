package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// gitRepo makes a repository with one commit on main, whatever git's default branch on the machine,
// and returns its path and a git runner.
func gitRepo(t *testing.T) (string, func(dir string, args ...string) string) {
	t.Helper()
	repo := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := command.Output(context.Background(), 0, dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git(repo, "init", "-q", "-b", "main")
	git(repo, "commit", "-q", "--allow-empty", "-m", "init")
	return repo, git
}

// hostTools puts a bd and a herdr at the end of PATH, for a run's check that they're installed
// (checkHost) on a machine without them, such as CI's. They fail if run: a test that runs one puts
// its own first on PATH.
func hostTools(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"bd", "herdr"} {
		faketool.Write(t, dir, name, "#!/bin/sh\necho '"+name+": not in this test' >&2\nexit 1\n")
	}
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+dir)
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// initRepo is gitRepo with a .beads/ folder, so orchestra init in it runs no bd init: a bd on the
// machine's PATH would otherwise set Beads up in the test's repository.
func initRepo(t *testing.T) string {
	t.Helper()
	repo, _ := gitRepo(t)
	if err := os.Mkdir(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}
