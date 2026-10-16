package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A run file that is a symlink to a file inside the worktree is replaced by the file however the link
// gets there: an absolute target, as ln -s "$PWD/README.md" makes, or a relative one that climbs out
// of the worktree and back in. An os.Root follows neither, and refusing them halted the whole run for
// the MCP config, and dropped the worker's launch prompt, though the link leads nowhere outside.
func TestRunFilesReplaceLinksInsideTheOSRootWontFollow(t *testing.T) {
	for name, write := range runWrites {
		for _, how := range []string{"absolute", "climbing"} {
			t.Run(name+" "+how, func(t *testing.T) {
				wt := t.TempDir()
				docs := filepath.Join(wt, "docs")
				if err := os.MkdirAll(docs, 0o755); err != nil {
					t.Fatal(err)
				}
				kept := filepath.Join(docs, name)
				if err := os.WriteFile(kept, []byte("kept"), 0o644); err != nil {
					t.Fatal(err)
				}
				target := kept
				if how == "climbing" { // .orchestra/run -> the worktree's parent, then back into it
					target = filepath.Join("..", "..", "..", filepath.Base(wt), "docs", name)
				}
				plant(t, wt, RunPath(name), target)

				if err := write(wt); err != nil {
					t.Fatalf("err = %v, want the file written in place of the link", err)
				}
				if fi, err := os.Lstat(filepath.Join(wt, RunPath(name))); err != nil || !fi.Mode().IsRegular() {
					t.Errorf("%s: %v %v, want a file in place of the link", RunPath(name), fi, err)
				}
				if got := listing(t, docs); len(got) != 1 || got[kept] != "kept" {
					t.Errorf("where the link pointed: %v, want only %s, as it was", got, kept)
				}
			})
		}
	}
}

// An absolute link to a file outside the worktree is still refused as an *EscapeError naming it, and
// left where it is: removing it would hide that a worker tried to send orchestra's file elsewhere.
func TestRunFilesRefuseAbsoluteLinksOutside(t *testing.T) {
	for name, write := range runWrites {
		wt, outside := t.TempDir(), t.TempDir()
		kept := filepath.Join(outside, name)
		if err := os.WriteFile(kept, []byte("kept"), 0o644); err != nil {
			t.Fatal(err)
		}
		plant(t, wt, RunPath(name), kept)

		err := write(wt)
		var e *EscapeError
		if !errors.As(err, &e) || e.Path != RunPath(name) || e.Dir != wt {
			t.Fatalf("%s: err = %v, want an *EscapeError for %s in %s", name, err, RunPath(name), wt)
		}
		if fi, err := os.Lstat(filepath.Join(wt, RunPath(name))); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s: the link was removed or replaced (%v)", name, err)
		}
		if got := listing(t, outside); len(got) != 1 || got[kept] != "kept" {
			t.Errorf("%s: outside: %v, want only %s, as it was", name, got, kept)
		}
	}
}
