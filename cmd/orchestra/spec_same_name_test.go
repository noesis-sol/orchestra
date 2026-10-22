package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A spec from outside the repository with the file name of another's copy, from another folder,
// doesn't start: the run would update the other spec's tree for the whole of this one. The problem
// names both and the way out; the other's copy is left as it was.
func TestSpecWithAnothersNameDoesntStart(t *testing.T) {
	t.Parallel()
	repo := specFixture(t)
	first := writeSpec(t, filepath.Join(t.TempDir(), "a", "PRD.md"), "# A\n")
	if _, _, err := project.CommitSpec(runCtx(t), repo, first); err != nil {
		t.Fatal(err)
	}
	second := writeSpec(t, filepath.Join(t.TempDir(), "b", "PRD.md"), "# B\n")
	_, problems := loadWith(t, "--spec", second)
	want := "The spec " + second + " (--spec) has the name of another: .orchestra/specs/PRD.md holds the copy of " +
		"another spec, " + project.SpecSource(first) + ", and a spec from outside the repository is kept under its " +
		"file name: give " + second + " another file name"
	if len(problems) != 1 || !strings.HasPrefix(problems[0], want) {
		t.Errorf("problems %q, want one starting with:\n%s", problems, want)
	}
	if got := read(t, filepath.Join(repo, ".orchestra", "specs", "PRD.md")); got != "# A\n" {
		t.Errorf("the first spec's copy holds %q", got)
	}
	if _, problems := loadWith(t, "--spec", first); len(problems) > 0 {
		t.Errorf("the first spec again: %q", problems)
	}
}
