//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitLeavesBdInitAloneWithChangesStaged(t *testing.T) {
	t.Parallel()
	dir := fakeBeadsTools(t, true)
	repo, git := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "notes.md"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "notes.md")
	head := git(repo, "rev-parse", "HEAD")
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "make check")
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if got := toolCalls(t, dir); got != "" {
		t.Errorf("bd ran with changes staged:\n%s", got)
	}
	if got := git(repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
	plain := strings.Join(strings.Fields(stdout), " ")
	if want := "✗ Beads bd init didn't run: it would commit what you have staged (notes.md) with its own files; " +
		"commit it, or unstage it with git reset, then run orchestra init again"; !strings.Contains(plain, want) {
		t.Errorf("stdout lacks %q:\n%s", want, stdout)
	}
}
