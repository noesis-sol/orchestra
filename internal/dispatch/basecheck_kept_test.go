package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A worktree CheckBase can't remove, as one whose setup left a folder it can't write to (Go's module
// cache is read-only), is left where it is, and Kept says where for the caller to say.
func TestCheckBaseSaysTheWorktreeItCouldntRemove(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root removes a read-only folder's files")
	}
	c := baseConfig(t, "true")
	c.Setup = "mkdir cache && touch cache/mod && chmod a-w cache"
	t.Cleanup(func() { writable(t, c.WTRoot) }) // before t.TempDir's removal of it
	b, err := CheckBase(t.Context(), c, newFakeGit("main"))
	if err != nil || b.Failed != "" {
		t.Fatalf("CheckBase: %+v, %v", b, err)
	}
	if filepath.Dir(b.Kept) != c.WTRoot || !strings.HasPrefix(filepath.Base(b.Kept), "check-base-") {
		t.Fatalf("Kept is %q, want the check-base- worktree in %s", b.Kept, c.WTRoot)
	}
	if _, err := os.Stat(filepath.Join(b.Kept, "cache", "mod")); err != nil {
		t.Errorf("the worktree kept isn't there: %v", err)
	}
}

// writable makes every folder under dir writable again, so that it can be removed.
func writable(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		return os.Chmod(path, 0o755)
	})
	if err != nil {
		t.Error(err)
	}
}
