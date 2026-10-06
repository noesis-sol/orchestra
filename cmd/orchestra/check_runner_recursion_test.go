package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A --check-fast that runs a runner is left out with a note, and one that is just the runner, run by
// bash or by an absolute path, keeps it as it is: init never writes a runner that runs itself.
func TestInitLeavesOutACheckThatRunsARunner(t *testing.T) {
	repo := initRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "-c", "1", "--check-fast", "make unit && "+project.FullRunner)
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if got := project.RunnerCommands(read(t, filepath.Join(repo, project.FastRunner))); !slices.Equal(got,
		[]string{"exit 0"}) {
		t.Errorf("check-fast.sh runs %q, want nothing", got)
	}
	plain := strings.Join(strings.Fields(stdout), " ")
	if !strings.Contains(plain, "left out 'make unit && "+project.FullRunner+"' (from --check-fast)") {
		t.Errorf("stdout doesn't say the check was left out:\n%s", stdout)
	}

	if _, _, err = runIn(t, repo, nil, "init", "--check-fast", "make unit"); err != nil {
		t.Fatal(err)
	}
	written := read(t, filepath.Join(repo, project.FastRunner))
	for _, check := range []string{"bash " + project.FastRunner, filepath.Join(repo, project.FastRunner)} {
		if _, stderr, err = runIn(t, repo, nil, "init", "--check-fast", check); err != nil {
			t.Fatalf("%s: %v\n%s", check, err, stderr)
		}
		if got := read(t, filepath.Join(repo, project.FastRunner)); got != written {
			t.Errorf("--check-fast %q changed check-fast.sh:\n%s", check, got)
		}
	}
}
