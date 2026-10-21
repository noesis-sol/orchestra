package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run's start warns README_STALE when the README's section on what to install differs from the
// requires list, and goes on, leaving the README as it is; a section in step says nothing.
func TestARunStartSaysWhenTheReadmeIsStale(t *testing.T) {
	t.Parallel()
	list := []project.Requirement{{Name: "sh", Check: "command -v sh", Manual: "install a shell"}}
	for _, inStep := range []bool{false, true} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.cfg.Requires = list
			readme := "# App\n\n" + project.RequiresMarkdown(nil)
			if inStep {
				readme = "# App\n\n" + project.RequiresMarkdown(list)
			}
			path := filepath.Join(h.cfg.Repo, project.ReadmeName)
			if err := os.WriteFile(path, []byte(readme), 0o644); err != nil {
				t.Fatal(err)
			}
			h.beads.add("A", "works", 1)
			h.worker("A", finishes("a.txt"))
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			ev := h.sink.text()
			warned := strings.Contains(ev, "  README_STALE: README.md's section on what to install differs from the "+
				"requires list in .orchestra/settings.json: orchestra requires --readme writes it, to commit\n")
			if warned == inStep {
				t.Errorf("in step %v: warned %v\n%s", inStep, warned, ev)
			}
			if got := read(t, path); got != readme {
				t.Errorf("the run changed the README:\n%s", got)
			}
		})
	}
}

// A worker in a project with a README is asked to write its section again with orchestra requires
// --readme when it changes the list, in the same commit; one without a README isn't.
func TestRequiresNoteAsksForTheReadmeSection(t *testing.T) {
	t.Parallel()
	o := &Loop{prompt: "Work on TICKET_ID: " + project.RequiresRule + "."}
	o.cfg.Repo = t.TempDir()
	if got := o.requiresNote(); strings.Contains(got, "--readme") {
		t.Errorf("no README, yet: %q", got)
	}
	if err := os.WriteFile(filepath.Join(o.cfg.Repo, project.ReadmeName), []byte("# App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := " A ticket that changes the list runs orchestra requires --readme, which writes README.md's section " +
		"on what to install from it, and commits that with the list.\n"
	if got := o.requiresNote(); !strings.HasSuffix(got, "which has no entries yet."+want) {
		t.Errorf("a prompt with the rule: %q", got)
	}
	o.prompt = "Work on TICKET_ID."
	if got := o.requiresNote(); !strings.HasSuffix(got, "as a question."+want) {
		t.Errorf("a prompt without the rule: %q", got)
	}
}
