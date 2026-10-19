package git

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// orchestra's reads leave the index as it is: git refreshes and rewrites a stale index on a read when
// it can, under .git/index.lock, and anything writing to the repository meanwhile (the maintainer's
// own git, a worker committing on main) fails on that lock.
func TestReadsLeaveTheIndexAsItIs(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	index := filepath.Join(repo, ".git", "index")
	for i, r := range []struct {
		name string
		read func()
	}{
		{"DirtyTree", func() { _, _ = g.DirtyTree(ctx, repo) }},
		{"DirtyWorktree", func() { g.DirtyWorktree(ctx, repo) }},
		{"Changes", func() { g.Changes(ctx, repo) }},
		{"PathChanged", func() { _, _ = g.PathChanged(ctx, repo, "a.md") }},
		{"ShortStatus", func() { g.ShortStatus(ctx, repo) }},
		{"DiffStat", func() { g.DiffStat(ctx, repo) }},
		{"ConflictedFiles", func() { g.ConflictedFiles(ctx, repo) }},
	} {
		// A new modification time on the file, its content the same, leaves the index stale.
		later := time.Now().Add(time.Duration(i+1) * time.Minute)
		if err := os.Chtimes(filepath.Join(repo, "a.md"), later, later); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		r.read()
		after, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Errorf("%s rewrote the index", r.name)
		}
	}
}
