package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The runners init writes pass the project's pinned ShellCheck (scripts/shellcheck.sh), as sh scripts:
// one that checks nothing, one with suites, one with a suite that takes turns on a lock, and check-full.sh.
// Skipped where ShellCheck can't be had, offline with nothing cached, as scripts/check.sh skips it.
// SC2251 is left out: the '! kill -0' that ends lockFunctions' stale is its status, which its callers
// test in an if, where errexit doesn't apply; rewording it would make every runner with a lock that init
// wrote before differ from what init writes now.
func TestRunnersPassShellcheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: may download ShellCheck, and runs it on each runner")
	}
	suites := []Suite{
		{Name: "unit tests", Command: "go test ./...", FoundIn: "go.mod"},
		{Name: "it's lint", Command: "npm run lint", FoundIn: "package.json:7"},
	}
	serial := append([]Suite{{Name: "e2e on port 3000", Command: "npm run e2e", Serial: true}}, suites...)
	scripts := map[string]string{
		"empty.sh":     FastScript(nil),
		"fast.sh":      FastScript(suites),
		"serial.sh":    FastScript(serial),
		"full.sh":      FullScript(serial, false),
		"full-only.sh": FullScript(suites, true),
	}
	dir := t.TempDir()
	args := []string{"-s", "sh", "-e", "SC2251", "--"}
	for name, script := range scripts {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
			t.Fatal(err)
		}
		args = append(args, path)
	}
	shellcheck, err := filepath.Abs(filepath.Join("..", "..", "scripts", "shellcheck.sh"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, shellcheck, args...).CombinedOutput()
	if strings.HasPrefix(string(out), "shellcheck skipped:") {
		t.Skip(strings.TrimSpace(string(out)))
	}
	if err != nil {
		t.Errorf("ShellCheck: %v:\n%s", err, out)
	}
}
