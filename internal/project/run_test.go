package project

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// plant makes the path link in the worktree wt a symlink to target.
func plant(t *testing.T, wt, link, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, link)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(wt, link)); err != nil {
		t.Fatal(err)
	}
}

// runWrites writes each run file orchestra writes in this package, by its name in .orchestra/run/,
// in the worktree wt.
var runWrites = map[string]func(wt string) error{
	MCPConfigName: func(wt string) error { _, err := WriteMCPConfig(wt, slices.Clip(testServers[:2])); return err },
	"prompt.md":   func(wt string) error { _, err := WriteLaunchPrompt(wt, "x-1", "Work on x-1."); return err },
}

// listing is every file under dir, with its content.
func listing(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		files[p] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// The run files are written only inside the worktree: a symlink that leads out of it, in place of
// .orchestra, .orchestra/run or the file, is an *EscapeError naming it, and nothing outside is
// written or removed.
func TestRunFilesStayInsideTheWorktree(t *testing.T) {
	for name, write := range runWrites {
		for _, link := range []string{Dir, filepath.Join(Dir, RunName), RunPath(name)} {
			t.Run(name+" through "+link, func(t *testing.T) {
				wt, outside := t.TempDir(), t.TempDir()
				kept := filepath.Join(outside, "kept")
				if err := os.WriteFile(kept, []byte("kept"), 0o644); err != nil {
					t.Fatal(err)
				}
				target := outside
				if link == RunPath(name) {
					target = kept // a file the write would replace
				}
				plant(t, wt, link, target)

				err := write(wt)
				var e *EscapeError
				if !errors.As(err, &e) || e.Path != link || e.Dir != wt {
					t.Fatalf("err = %v, want an *EscapeError for %s in %s", err, link, wt)
				}
				if got := listing(t, outside); len(got) != 1 || got[kept] != "kept" {
					t.Errorf("outside the worktree: %v, want only %s, as it was", got, kept)
				}
			})
		}
	}
}
