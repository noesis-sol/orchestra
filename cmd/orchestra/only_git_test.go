package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The tests' PATH holds one folder, of links to git and the POSIX utilities (faketool.OnlyTools):
// wherever claude, bd, brew, tmux, herdr or cmux is installed, a test finds only the one it put on
// its own PATH.
func TestTheTestsFindOnlyGitOfTheMachinesTools(t *testing.T) {
	t.Parallel()
	path := filepath.SplitList(os.Getenv("PATH")) //nolint:forbidigo // the process's PATH is this test's subject
	if len(path) != 1 {
		t.Fatalf("the process's PATH is %q, not one folder", path)
	}
	for _, name := range []string{"git", "sh"} {
		if _, err := os.Stat(filepath.Join(path[0], name)); err != nil {
			t.Errorf("no %s: %v", name, err)
		}
	}
	for _, name := range []string{"claude", "bd", "brew", "tmux", "herdr", "cmux"} {
		if _, err := os.Lstat(filepath.Join(path[0], name)); err == nil {
			t.Errorf("the PATH has the machine's %s", name)
		}
	}
}
