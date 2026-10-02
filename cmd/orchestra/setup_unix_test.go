//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// failingRevParse puts a git on PATH that fails 'git rev-parse' given flag and passes everything
// else to the real git.
func failingRevParse(t *testing.T, flag string) {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := `#!/bin/sh
for a in "$@"; do
	[ "$a" = "` + flag + `" ] && { echo "fatal: cannot read the git directory" >&2; exit 128; }
done
exec "` + gitPath + `" "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// A git that can't say where the repository's git directories are is a setup problem, not a
// main checkout: two failed rev-parses would otherwise compare equal.
func TestConfigSetupProblemWhenGitDirsAreUnreadable(t *testing.T) {
	for _, flag := range []string{"--absolute-git-dir", "--git-common-dir"} {
		t.Run(flag, func(t *testing.T) {
			configFixture(t, "")
			failingRevParse(t, flag)
			_, p := loadWith(t)
			const want = " is the main checkout: "
			if joined := strings.Join(p, "\n"); !strings.Contains(joined, want) || !strings.Contains(joined, "cannot read the git directory") {
				t.Errorf("problems lack %q with git's reason:\n%s", want, joined)
			}
		})
	}
}
