package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// orchestra requires --markdown prints the list as the README's section, checking nothing; --readme
// writes it into README.md, and says so only once; either with another of the flags is refused.
func TestRequiresMarkdownAndReadme(t *testing.T) {
	t.Parallel()
	repo := requiresFixture(t, requiresList)
	settings, _, err := project.LoadSettings(repo)
	if err != nil {
		t.Fatal(err)
	}
	list, err := project.ResolveRequires(settings)
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runIn(t, repo, nil, "requires", "--markdown")
	if err != nil || stderr != "" || stdout != project.RequiresMarkdown(list) {
		t.Errorf("--markdown: %v, stdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(repo, "tool")); err == nil {
		t.Error("--markdown ran an install")
	}
	readme := filepath.Join(repo, project.ReadmeName)
	if err := os.WriteFile(readme, []byte("# App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = runIn(t, repo, nil, "requires", "--readme")
	if err != nil || !strings.HasPrefix(stdout, "Wrote the requires list into README.md") {
		t.Errorf("--readme: %v, %q", err, stdout)
	}
	if b, err := os.ReadFile(readme); err != nil || string(b) != "# App\n\n"+project.RequiresMarkdown(list) {
		t.Errorf("README.md:\n%s\n%v", b, err)
	}
	if stdout, _, err = runIn(t, repo, nil, "requires", "--readme"); err != nil ||
		stdout != "README.md's section is the requires list's already.\n" {
		t.Errorf("--readme again: %v, %q", err, stdout)
	}
	_, stderr, err = runIn(t, repo, nil, "requires", "--markdown", "--no-install")
	if exitOf(err) != 2 || !strings.Contains(stderr, "give one of --no-install, --markdown and --readme") {
		t.Errorf("two flags: exit %d, %q", exitOf(err), stderr)
	}
}
