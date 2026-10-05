package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without a terminal there is no stage 2, so no choice files test work: init installs no skill,
// whichever agent the workers are.
func TestInitWithoutATerminalInstallsNoSkill(t *testing.T) {
	repo := initRepo(t)
	for _, args := range [][]string{
		{"init", "--check", "make check", "-c", "1"},
		{"init", "--check", "make check", "-c", "1", "--agent", "codex"},
	} {
		stdout, stderr, err := runIn(t, repo, map[string]string{"AGENT_KIND": "codex"}, args...)
		if err != nil || strings.Contains(stdout, "create-check-suite") {
			t.Fatalf("%v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
		}
		for _, d := range []string{".claude", ".agents"} {
			if _, err := os.Stat(filepath.Join(repo, d, "skills")); err == nil {
				t.Errorf("%v wrote %s/skills", args, d)
			}
		}
	}
}
