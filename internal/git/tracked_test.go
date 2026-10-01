package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

func TestTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a.go", "sub dir/b.md", "untracked.txt"} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "a.go", "sub dir/b.md"}} {
		if out, err := command.Output(context.Background(), 0, dir, "git", args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if got := (Git{}).TrackedFiles(context.Background(), dir); !slices.Equal(got, []string{"a.go", "sub dir/b.md"}) {
		t.Errorf("got %q", got)
	}
	if got := (Git{}).TrackedFiles(context.Background(), t.TempDir()); got != nil {
		t.Errorf("not a repository: %q", got)
	}
}
