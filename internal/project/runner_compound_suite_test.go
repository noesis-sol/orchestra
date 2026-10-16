package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// compoundRepo is a repository whose scripts/check.sh fails, with fake make, go and npm on the PATH
// it returns: each writes its name and the directory it ran in to the file ran, at the root, and npm
// fails where npmFails is set.
func compoundRepo(t *testing.T, npmFails bool) (repo, path string) {
	t.Helper()
	repo, bin := t.TempDir(), t.TempDir()
	for _, dir := range []string{filepath.Join(repo, "scripts"), filepath.Join(repo, "web")} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	faketool.Write(t, filepath.Join(repo, "scripts"), "check.sh", "#!/bin/sh\nexit 1\n")
	ran := filepath.Join(repo, "ran")
	for _, name := range []string{"make", "go", "npm"} {
		exit := "0"
		if name == "npm" && npmFails {
			exit = "1"
		}
		faketool.Write(t, bin, name, "#!/bin/sh\necho \""+name+" in $(pwd -P)\" >> '"+ran+"'\nexit "+exit+"\n")
	}
	return repo, "PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")
}

// ranLines are the fakes compoundRepo's runner ran, one line each.
func ranLines(t *testing.T, repo string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, "ran"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// A suite that is an AND-list fails the runner when a part of it before the last fails, as the scout's
// "scripts/check.sh && make lint" does with a broken check.sh: under set -e alone, sh would skip make
// lint, go on to go vet and pass.
func TestRunnerFailsWhereACompoundSuiteFails(t *testing.T) {
	repo, path := compoundRepo(t, false)
	writeRunner(t, repo, FastScript([]Suite{
		{Name: "check", Command: "scripts/check.sh && make lint", FoundIn: "scripts/check.sh"},
		{Command: "go vet ./..."},
	}))
	out, err := runRunner(t, repo, repo, t.TempDir(), 10*time.Second, path)
	if err == nil {
		t.Errorf("the runner passed with check.sh failing:\n%s", out)
	}
	if got := ranLines(t, repo); len(got) != 0 {
		t.Errorf("ran %q after check.sh failed", got)
	}
}

// A compound suite behind the lock, the runner's last, fails the runner too: the unlock after it
// doesn't make the list's status its own.
func TestRunnerFailsWhereASerialCompoundSuiteFails(t *testing.T) {
	repo, path := compoundRepo(t, true)
	writeRunner(t, repo, FastScript([]Suite{{Name: "web", Command: "npm test && make build", Serial: true}}))
	if out, err := runRunner(t, repo, repo, t.TempDir(), 10*time.Second, path); err == nil {
		t.Errorf("the runner passed with npm test failing:\n%s", out)
	}
	if got := ranLines(t, repo); len(got) != 1 || !strings.HasPrefix(got[0], "npm in ") {
		t.Errorf("ran %q, want npm alone", got)
	}
}

// A suite's cd stays in it: the next suite runs from the repository's root.
func TestRunnerRunsTheSuiteAfterACdFromTheRoot(t *testing.T) {
	repo, path := compoundRepo(t, false)
	writeRunner(t, repo, FastScript([]Suite{{Command: "cd web && npm test"}, {Command: "make lint"}}))
	if out, err := runRunner(t, repo, repo, t.TempDir(), 10*time.Second, path); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"npm in " + filepath.Join(root, "web"), "make in " + root}
	if got := ranLines(t, repo); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ran %q, want %q", got, want)
	}
}

// A suite whose command ends in a comment still runs, and fails the runner where it fails: the
// subshell's ) is on a line of its own.
func TestRunnerRunsASuiteEndingInAComment(t *testing.T) {
	repo, path := compoundRepo(t, true)
	writeRunner(t, repo, FastScript([]Suite{{Command: "npm test # the unit tests"}, {Command: "make lint"}}))
	if out, err := runRunner(t, repo, repo, t.TempDir(), 10*time.Second, path); err == nil {
		t.Errorf("the runner passed with npm test failing:\n%s", out)
	}
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got := ranLines(t, repo); strings.Join(got, "\n") != "npm in "+root {
		t.Errorf("ran %q, want npm alone", got)
	}
}
