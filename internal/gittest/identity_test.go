package gittest

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A commit under Main needs no identity of the machine's, which CI's runners don't have.
func TestMainGivesGitAnIdentity(t *testing.T) {
	// No identity of the user's, and none guessed from the machine's names, as git does on macOS.
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
		"EMAIL"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	repo := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := command.Output(ctx, command.WriteLimit, repo, "git", args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
