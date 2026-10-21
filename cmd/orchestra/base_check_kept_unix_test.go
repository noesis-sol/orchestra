//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A merge check's worktree the pre-flight can't remove, its setup having left a folder it can't write to,
// is said, with the command that removes it.
func TestBaseCheckSaysTheWorktreeLeft(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root removes a read-only folder's files")
	}
	s := checkSession(t, nil, nil, "")
	s.cfg.Repo, _ = gitRepo(t)
	s.cfg.Base, s.cfg.WTRoot, s.cfg.Check = "main", t.TempDir(), "true"
	s.cfg.Setup = "mkdir cache && touch cache/mod && chmod a-w cache"
	t.Cleanup(func() { // before t.TempDir's removal of it
		_ = filepath.WalkDir(s.cfg.WTRoot, func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				err = os.Chmod(path, 0o755)
			}
			return err
		})
	})
	wantResult(t, baseCheck(runCtx(t), s), tui.CheckPassed, "the merge check 'true' passes on main at ")
	left, err := filepath.Glob(filepath.Join(s.cfg.WTRoot, "check-base-*"))
	if err != nil || len(left) != 1 {
		t.Fatalf("worktrees left: %q (%v)", left, err)
	}
	out := s.e.stdout.(*strings.Builder).String()
	if want := "the merge check's worktree " + left[0] + " is left (git worktree remove --force " +
		command.ShellWord(left[0]) + " removes it)"; !strings.Contains(out, want) {
		t.Errorf("the pre-flight says:\n%s\nwant it to say %q", out, want)
	}
}
