package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readmeList is a requires list with an install, a manual step limited to one system, a command with a
// backtick and one of two lines.
var readmeList = []Requirement{
	{Name: "jq", Check: "command -v jq", Install: "brew install jq"},
	{Name: "docker", Check: "docker info", Manual: "Install and start\nDocker Desktop", Platforms: []string{"darwin"}},
	{Name: "tick", Check: "test `uname` = Darwin", Install: "true"},
	{Name: "two", Check: "test -e a\ntest -e b", Install: "touch a b"},
}

// The README's section is the list as Markdown, between its markers: each requirement with its check
// and its install or manual step, a command in a code span that its backticks don't end, or a fenced
// block for more than one line.
func TestRequiresMarkdownIsTheList(t *testing.T) {
	t.Parallel()
	want := RequiresStart + "\n" +
		"<!-- Generated from \"requires\" in .orchestra/settings.json: change the list, then run orchestra requires " +
		"--readme. -->\n" +
		"## What to install\n\n" +
		"What this project needs of the machine, from `\"requires\"` in `.orchestra/settings.json`. " +
		"`orchestra requires` checks each one and installs what it can.\n\n" +
		"- **jq**\n  - check: `command -v jq`\n  - install: `brew install jq`\n" +
		"- **docker** (only on macOS)\n  - check: `docker info`\n  - by hand: Install and start Docker Desktop\n" +
		"- **tick**\n  - check: ``test `uname` = Darwin``\n  - install: `true`\n" +
		"- **two**\n  - check:\n\n    ```sh\n    test -e a\n    test -e b\n    ```\n\n  - install: `touch a b`\n" +
		RequiresEnd + "\n"
	if got := RequiresMarkdown(readmeList); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	empty := RequiresMarkdown(nil)
	if !strings.Contains(empty, "Nothing yet: the project's requires list") || !strings.HasPrefix(empty, RequiresStart) ||
		!strings.HasSuffix(empty, RequiresEnd+"\n") {
		t.Errorf("an empty list's section:\n%s", empty)
	}
}

// writeReadme writes text as the README at repo's top.
func writeReadme(t *testing.T, repo, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ReadmeName), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The section is added at the README's end where it has none, replaced between its markers where it
// has one, the rest of the README as it was, and written again only when the list changed.
func TestWriteReadmeRequiresAddsThenReplacesTheSection(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeReadme(t, repo, "# App\n\nIt does things.\n")
	if st, err := ReadmeRequires(repo, readmeList[:1]); st != ReadmeNoSection || err != nil {
		t.Fatalf("before: %v, %v", st, err)
	}
	if changed, err := WriteReadmeRequires(repo, readmeList[:1]); !changed || err != nil {
		t.Fatalf("first write: %v, %v", changed, err)
	}
	if got, want := readRepoFile(t, repo, ReadmeName), "# App\n\nIt does things.\n\n"+
		RequiresMarkdown(readmeList[:1]); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if st, err := ReadmeRequires(repo, readmeList[:1]); st != ReadmeInStep || err != nil {
		t.Errorf("after: %v, %v", st, err)
	}
	// Text after the section stays where it is.
	writeReadme(t, repo, readRepoFile(t, repo, ReadmeName)+"\n## License\n\nMIT\n")
	if st, _ := ReadmeRequires(repo, readmeList); st != ReadmeStale {
		t.Errorf("a longer list reads as %v, want stale", st)
	}
	if changed, err := WriteReadmeRequires(repo, readmeList); !changed || err != nil {
		t.Fatalf("second write: %v, %v", changed, err)
	}
	if got, want := readRepoFile(t, repo, ReadmeName), "# App\n\nIt does things.\n\n"+RequiresMarkdown(readmeList)+
		"\n## License\n\nMIT\n"; got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if changed, err := WriteReadmeRequires(repo, readmeList); changed || err != nil {
		t.Errorf("the same list again: changed %v, %v", changed, err)
	}
}

// A README with Windows line endings keeps them, and reads as in step once written.
func TestWriteReadmeRequiresKeepsCRLF(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	writeReadme(t, repo, "# App\r\n")
	if _, err := WriteReadmeRequires(repo, readmeList[:1]); err != nil {
		t.Fatal(err)
	}
	got := readRepoFile(t, repo, ReadmeName)
	if strings.Count(got, "\n") != strings.Count(got, "\r\n") {
		t.Errorf("mixed line endings:\n%q", got)
	}
	if st, err := ReadmeRequires(repo, readmeList[:1]); st != ReadmeInStep || err != nil {
		t.Errorf("after: %v, %v", st, err)
	}
}

// Markers out of pair leave the README as it is: an error to write it, a caution in init.
func TestReadmeMarkersOutOfPair(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"# App\n" + RequiresStart + "\n",
		"# App\n" + RequiresEnd + "\n" + RequiresStart + "\n",
		RequiresStart + "\n" + RequiresEnd + "\n" + RequiresStart + "\n" + RequiresEnd + "\n",
	} {
		repo := t.TempDir()
		writeReadme(t, repo, text)
		if _, err := WriteReadmeRequires(repo, readmeList); !errors.Is(err, ErrReadmeMarkers) {
			t.Errorf("%q: %v, want ErrReadmeMarkers", text, err)
		}
		if got := readRepoFile(t, repo, ReadmeName); got != text {
			t.Errorf("%q was changed to %q", text, got)
		}
		if s, ok, err := oneStep(t, ApplyReadme, repo, Choice{}); err != nil || !ok || s.Kind != StepCaution {
			t.Errorf("%q: init's step %+v, %v", text, s, err)
		}
	}
	// A marker inside a line, as a README documenting it has, isn't one.
	repo := t.TempDir()
	writeReadme(t, repo, "Put it between `"+RequiresStart+"` and `"+RequiresEnd+"`.\n")
	if st, err := ReadmeRequires(repo, nil); st != ReadmeNoSection || err != nil {
		t.Errorf("markers in a line: %v, %v", st, err)
	}
}

// readmeRepo is a repository with settings whose requires list is list, and README text, none for "".
func readmeRepo(t *testing.T, list []Requirement, readme string) string {
	t.Helper()
	repo, _ := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveSettings(repo, Settings{CheckFast: "make check", Requires: list}); err != nil {
		t.Fatal(err)
	}
	if readme != "" {
		writeReadme(t, repo, readme)
	}
	return repo
}

// orchestra init writes the section into the README where it has none, for the user to commit, updates
// one that differs, and keeps one in step; it writes no README where there is none, and no section for
// an empty list.
func TestInitWritesTheReadmeSection(t *testing.T) {
	repo := readmeRepo(t, readmeList, "# App\n")
	c := DefaultChoice(Settings{CheckFast: "make check"}, "")
	steps, err := Apply(context.Background(), repo, c, false)
	if err != nil {
		t.Fatal(err)
	}
	var readme *Step
	for i := range steps {
		if steps[i].Label == "README" {
			readme = &steps[i]
		}
	}
	if readme == nil || readme.Kind != StepDone || !strings.HasPrefix(readme.Detail, "wrote README.md's section") ||
		strings.Join(readme.Commit, ",") != ReadmeName {
		t.Fatalf("init's README step: %+v\nall: %+v", readme, steps)
	}
	if got, want := readRepoFile(t, repo, ReadmeName), "# App\n\n"+RequiresMarkdown(readmeList); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if s, ok, err := oneStep(t, ApplyReadme, repo, Choice{}); err != nil || !ok || s.Kind != StepKept {
		t.Errorf("in step: %+v, %v", s, err)
	}
	if err := SaveSettings(repo, Settings{CheckFast: "make check", Requires: readmeList[:2]}); err != nil {
		t.Fatal(err)
	}
	s, ok, err := oneStep(t, ApplyReadme, repo, Choice{})
	if err != nil || !ok || s.Kind != StepDone || !strings.HasPrefix(s.Detail, "updated") {
		t.Errorf("a changed list: %+v, %v", s, err)
	}
	if got, want := readRepoFile(t, repo, ReadmeName), "# App\n\n"+RequiresMarkdown(readmeList[:2]); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}

	for name, repo := range map[string]string{
		"no README":                       readmeRepo(t, readmeList, ""),
		"an empty list and no section":    readmeRepo(t, nil, "# App\n"),
		"a list a run would refuse it in": readmeRepo(t, []Requirement{{Name: "x"}}, "# App\n"),
	} {
		if s, ok, err := oneStep(t, ApplyReadme, repo, Choice{}); ok || err != nil {
			t.Errorf("%s: step %+v, %v", name, s, err)
		}
		if _, err := os.Stat(filepath.Join(repo, ReadmeName)); name == "no README" && err == nil {
			t.Errorf("init wrote a README where there was none")
		}
	}
}

// A run's start is told when the README's section differs from the list, or the README has none though
// the list has entries; nothing where it is in step or there is no README.
func TestReadmeStaleLine(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	if got := ReadmeStaleLine(repo, readmeList); got != "" {
		t.Errorf("no README: %q", got)
	}
	writeReadme(t, repo, "# App\n")
	if got := ReadmeStaleLine(repo, nil); got != "" {
		t.Errorf("an empty list, no section: %q", got)
	}
	if got := ReadmeStaleLine(repo, readmeList); !strings.HasPrefix(got, "README.md has no section on what to install") ||
		!strings.HasSuffix(got, "orchestra requires --readme writes it, to commit") {
		t.Errorf("no section: %q", got)
	}
	if _, err := WriteReadmeRequires(repo, readmeList); err != nil {
		t.Fatal(err)
	}
	if got := ReadmeStaleLine(repo, readmeList); got != "" {
		t.Errorf("in step: %q", got)
	}
	if got := ReadmeStaleLine(repo, readmeList[:1]); !strings.HasPrefix(got,
		"README.md's section on what to install differs from the requires list in .orchestra/settings.json") {
		t.Errorf("stale: %q", got)
	}
}
