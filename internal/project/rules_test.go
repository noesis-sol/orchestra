package project

import (
	"context"
	"path/filepath"
	"testing"
)

// A worker's standing rules go in .orchestra/run/rules.md of its worktree, by an absolute path for
// --append-system-prompt-file, and stay out of git like the rest of .orchestra/run/.
func TestWriteRulesKeepsThemOutOfGit(t *testing.T) {
	repo, git := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", "-b", "wt/k-1", wt)
	if err := EnsureRunExcluded(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	const rules = "You are responsible for k-1.\n- Never push.\n"
	path, err := WriteRules(wt, rules)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wt, ".orchestra", "run", "rules.md"); path != want {
		t.Errorf("path %q, want %q", path, want)
	}
	if got := read(t, path); got != rules {
		t.Errorf("rules = %q", got)
	}
	if s := git(wt, "status", "--porcelain", "--untracked-files=all"); s != "" {
		t.Errorf("the rules show in git status: %q", s)
	}
}
