package git

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A detached HEAD is no branch; a folder git can't read is an error, not a detached HEAD or a
// dirty tree.
func TestCheckoutStateTellsGitFailingFromTheAnswer(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := command.Output(context.Background(), 0, dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "init")
	if br, err := (Git{}).CurrentBranch(context.Background(), dir); br != "main" || err != nil {
		t.Errorf("on main: got %q, %v", br, err)
	}
	git("switch", "-q", "--detach")
	if br, err := (Git{}).CurrentBranch(context.Background(), dir); br != "" || err != nil {
		t.Errorf("detached: got %q, %v", br, err)
	}

	gone := filepath.Join(dir, "gone")
	if br, err := (Git{}).CurrentBranch(context.Background(), gone); err == nil {
		t.Errorf("a missing folder: got branch %q and no error", br)
	}
	if d, err := (Git{}).DirtyTree(context.Background(), gone); err == nil || d != "" {
		t.Errorf("a missing folder: got %q, %v; want only an error", d, err)
	}
}
