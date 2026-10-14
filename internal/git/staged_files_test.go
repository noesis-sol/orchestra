package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

func TestStagedFiles(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	run := func(args ...string) {
		t.Helper()
		args = append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)
		if out, err := command.Output(ctx, 0, dir, "git", args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	for _, f := range []string{"a.go", "sub dir/b.md", "untracked.txt"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("add", "a.go", "sub dir/b.md")
	// Before the first commit, everything staged.
	if got, err := (Git{}).StagedFiles(ctx, dir); err != nil || !slices.Equal(got, []string{"a.go", "sub dir/b.md"}) {
		t.Errorf("before the first commit: %q, %v", got, err)
	}
	run("commit", "-q", "-m", "first")
	if got, err := (Git{}).StagedFiles(ctx, dir); err != nil || len(got) != 0 {
		t.Errorf("after it: %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := (Git{}).StagedFiles(ctx, dir); err != nil || len(got) != 0 {
		t.Errorf("changed, not staged: %q, %v", got, err)
	}
	run("add", "a.go")
	if got, err := (Git{}).StagedFiles(ctx, dir); err != nil || !slices.Equal(got, []string{"a.go"}) {
		t.Errorf("staged: %q, %v", got, err)
	}
	if _, err := (Git{}).StagedFiles(ctx, t.TempDir()); err == nil {
		t.Error("not a repository: no error")
	}
}
