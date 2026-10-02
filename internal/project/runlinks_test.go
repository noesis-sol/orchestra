package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// .orchestra and .orchestra/run must be real folders: git's ignore rules for the run files match a
// folder only, so a symlink in place of either, wherever it points, is an *EscapeError naming it,
// with ErrRunLink, and nothing is written where it points. Through a link that stays inside the
// worktree, the MCP servers' secrets would be untracked files there, for a worker to commit.
func TestRunFoldersThatAreSymlinksAreRefused(t *testing.T) {
	for name, write := range runWrites {
		for _, link := range []string{Dir, filepath.Join(Dir, RunName)} {
			for _, to := range []string{"inside", "outside", "nowhere"} {
				t.Run(name+" through "+link+" "+to, func(t *testing.T) {
					wt := t.TempDir()
					dir := map[string]string{ // what the link points to: a folder with only "kept" in it, or nothing
						"inside":  filepath.Join(wt, "docs"),
						"outside": t.TempDir(),
						"nowhere": filepath.Join(wt, "missing"),
					}[to]
					if to != "nowhere" {
						if err := os.MkdirAll(dir, 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(dir, "kept"), []byte("kept"), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					target := dir
					if to != "outside" { // as a worker would make it: relative to the link's folder
						rel, err := filepath.Rel(filepath.Dir(filepath.Join(wt, link)), dir)
						if err != nil {
							t.Fatal(err)
						}
						target = rel
					}
					plant(t, wt, link, target)

					err := write(wt)
					var e *EscapeError
					if !errors.As(err, &e) || e.Path != link || e.Dir != wt || !errors.Is(err, ErrRunLink) {
						t.Fatalf("err = %v, want an *EscapeError with ErrRunLink for %s in %s", err, link, wt)
					}
					if fi, err := os.Lstat(filepath.Join(wt, link)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
						t.Errorf("the link %s was removed or replaced (%v)", link, err)
					}
					if to == "nowhere" {
						if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
							t.Errorf("%s was made where %s points (%v)", dir, link, err)
						}
						return
					}
					if got := listing(t, dir); len(got) != 1 || got[filepath.Join(dir, "kept")] != "kept" {
						t.Errorf("where %s points: %v, want only kept, as it was", link, got)
					}
				})
			}
		}
	}
}

// A real .orchestra/run holds the run files as before, whether an earlier run made it or not.
func TestRunFilesInARealFolder(t *testing.T) {
	for _, made := range []bool{false, true} {
		wt := t.TempDir()
		if made {
			if err := os.MkdirAll(filepath.Join(wt, Dir, RunName), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for name, write := range runWrites {
			if err := write(wt); err != nil {
				t.Fatalf("%s (folder made before: %t): %v", name, made, err)
			}
			if fi, err := os.Lstat(filepath.Join(wt, RunPath(name))); err != nil || !fi.Mode().IsRegular() {
				t.Errorf("%s (folder made before: %t): %v %v, want a file", name, made, fi, err)
			}
		}
		if fi, err := os.Stat(filepath.Join(wt, RunPath(MCPConfigName))); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v, want mode 0600", MCPConfigName, fi, err)
		}
	}
}
