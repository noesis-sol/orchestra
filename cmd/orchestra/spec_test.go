package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// specEnv is an environment of these variables alone.
func specEnv(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// writeSpec writes a spec with text at path, and returns path.
func writeSpec(t *testing.T, path, text string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// specFixture is configFixture with neither of a spec run's variables set.
func specFixture(t *testing.T) string {
	t.Helper()
	repo := configFixture(t, `{"concurrent": 1}`)
	t.Setenv("ORCHESTRA_SPEC", "")
	t.Setenv("ORCHESTRA_REFINE", "")
	return repo
}

// --spec and ORCHESTRA_SPEC give the spec, the flag over the variable; --refine and ORCHESTRA_REFINE
// say to refine it, read like the other on-or-off variables.
func TestSpecFlagsAndTheirVariables(t *testing.T) {
	outside := t.TempDir()
	spec := writeSpec(t, filepath.Join(outside, "app.md"), "# An app\n")
	other := writeSpec(t, filepath.Join(outside, "other.md"), "# Another\n")
	for _, tc := range []struct {
		name   string
		args   []string
		env    map[string]string
		file   string
		refine bool
	}{
		{"none", nil, nil, "", false},
		{"--spec", []string{"--spec", spec}, nil, spec, false},
		{"ORCHESTRA_SPEC", nil, map[string]string{"ORCHESTRA_SPEC": spec}, spec, false},
		{"--spec over the variable", []string{"--spec", spec}, map[string]string{"ORCHESTRA_SPEC": other}, spec, false},
		{"--refine", []string{"--spec", spec, "--refine"}, nil, spec, true},
		{"ORCHESTRA_REFINE=yes", []string{"--spec", spec}, map[string]string{"ORCHESTRA_REFINE": "yes"}, spec, true},
		{"ORCHESTRA_REFINE=0", []string{"--spec", spec}, map[string]string{"ORCHESTRA_REFINE": "0"}, spec, false},
		{"--refine=false over the variable", []string{"--spec", spec, "--refine=false"},
			map[string]string{"ORCHESTRA_REFINE": "1"}, spec, false},
		// A standing choice, like ORCHESTRA_TICKETS: a run without a spec leaves it unused.
		{"ORCHESTRA_REFINE=1 alone", nil, map[string]string{"ORCHESTRA_REFINE": "1"}, "", true},
	} {
		f, problems, err := readFlags(tc.args, specEnv(tc.env), io.Discard)
		if err != nil || len(problems) > 0 || f.specFile != tc.file || f.Refine != tc.refine {
			t.Errorf("%s: spec %q, refine %v; want %q, %v (%v, %q)", tc.name, f.specFile, f.Refine, tc.file,
				tc.refine, err, problems)
		}
	}
	_, problems, _ := readFlags([]string{"--spec", spec}, specEnv(map[string]string{"ORCHESTRA_REFINE": "maybe"}),
		io.Discard)
	if len(problems) != 1 || !strings.Contains(problems[0], "ORCHESTRA_REFINE must be 1 or 0") {
		t.Errorf("ORCHESTRA_REFINE=maybe: %q", problems)
	}
}

// A spec given relative to the working directory is found from there, and is kept in the repository
// where it is, if it is inside, else in .orchestra/specs/.
func TestSpecPathInTheRepository(t *testing.T) {
	repo := specFixture(t)
	writeSpec(t, filepath.Join(repo, "docs", "app.md"), "# An app\n")
	c, problems := loadWith(t, "--spec", "docs/app.md")
	if len(problems) > 0 || !samePath(c.specFile, filepath.Join(repo, "docs", "app.md")) || c.Spec != "docs/app.md" {
		t.Errorf("inside: file %q, kept at %q (%q)", c.specFile, c.Spec, problems)
	}
	outside := writeSpec(t, filepath.Join(t.TempDir(), "app spec.md"), "# An app\n")
	t.Setenv("ORCHESTRA_SPEC", outside)
	if c, problems := loadWith(t); len(problems) > 0 || c.specFile != outside || c.Spec != ".orchestra/specs/app spec.md" {
		t.Errorf("outside: file %q, kept at %q (%q)", c.specFile, c.Spec, problems)
	}
}

// --spec doesn't go with --feature, --ticket or --tickets, nor --refine without it; each is a setup
// problem.
func TestSpecFlagCombinations(t *testing.T) {
	spec := "/specs/app.md"
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"no file", []string{"--spec", ""}, nil, "--spec needs the spec's file: orchestra --spec <file>"},
		{"--refine without --spec", []string{"--refine"}, nil, "--refine only applies with --spec or --feature."},
		{"--feature", []string{"--spec", spec, "--feature", "Add a flag"}, nil,
			"--spec can't be combined with --feature: one runs a spec, the other a feature request."},
		{"ORCHESTRA_SPEC with --feature", []string{"--feature", "Add a flag"}, map[string]string{"ORCHESTRA_SPEC": spec},
			"ORCHESTRA_SPEC can't be combined with --feature"},
		{"--ticket", []string{"--spec", spec, "--ticket", "x-1"}, nil, "--spec can't be combined with --ticket " +
			"(or ORCHESTRA_TICKET): a spec run is scoped to the epic its breakdown files."},
		{"ORCHESTRA_TICKET", []string{"--spec", spec}, map[string]string{"ORCHESTRA_TICKET": "x-1"},
			"--spec can't be combined with --ticket (or ORCHESTRA_TICKET)"},
		{"--tickets", []string{"--spec", spec, "--tickets"}, nil,
			"--spec can't be combined with --tickets: one runs a spec, the other the current tickets."},
		// ORCHESTRA_TICKETS=1 is a default, which the spec overrides, as --feature does.
		{"ORCHESTRA_TICKETS=1", []string{"--spec", spec}, map[string]string{"ORCHESTRA_TICKETS": "1"}, ""},
	} {
		_, problems, err := readFlags(tc.args, specEnv(tc.env), io.Discard)
		if err != nil || tc.want == "" && len(problems) > 0 ||
			tc.want != "" && (len(problems) != 1 || !strings.Contains(problems[0], tc.want)) {
			t.Errorf("%s: problems %q, want one with %q (%v)", tc.name, problems, tc.want, err)
		}
	}
}

// A spec file that is missing, a folder, unreadable, empty or not text is a setup problem, naming
// the file as given and where it came from.
func TestSpecFileProblems(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, tc := range []struct {
		name, file, from, want string
	}{
		{"missing", "none.md", "--spec", "Spec not found: none.md (--spec)."},
		{"missing, from the variable", "none.md", "ORCHESTRA_SPEC", "Spec not found: none.md (ORCHESTRA_SPEC)."},
		{"a folder", dir, "--spec", "Cannot read the spec " + dir + " (--spec): is a directory."},
		{"empty", filepath.Base(writeSpec(t, filepath.Join(dir, "empty.md"), "\n \n")), "--spec",
			"The spec empty.md (--spec) is empty: write what to build in it first."},
		{"not text", filepath.Base(writeSpec(t, filepath.Join(dir, "app.png"), "\x89PNG\r\n\x1a\n\x00")), "--spec",
			"The spec app.png (--spec) isn't text: give a Markdown or plain-text file, in UTF-8."},
	} {
		c := options{specFile: tc.file}
		if p := checkSpecFile(&c, tc.from); len(p) != 1 || p[0] != tc.want {
			t.Errorf("%s: problems %q, want %q", tc.name, p, tc.want)
		}
	}
	c := options{specFile: filepath.Base(writeSpec(t, filepath.Join(dir, "app.md"), "# An app\n"))}
	if p := checkSpecFile(&c, "--spec"); len(p) > 0 || !samePath(c.specFile, filepath.Join(dir, "app.md")) {
		t.Errorf("a spec: problems %q, file %q", p, c.specFile)
	}
	if os.Getuid() != 0 { // root reads it all the same
		locked := writeSpec(t, filepath.Join(dir, "locked.md"), "# Locked\n")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		c := options{specFile: locked}
		if p := checkSpecFile(&c, "--spec"); len(p) != 1 ||
			p[0] != "Cannot read the spec "+locked+" (--spec): permission denied." {
			t.Errorf("unreadable: problems %q", p)
		}
	}
}

// A spec where git wouldn't commit it, ignored or in the .git folder, is a setup problem, listed
// with the run's others.
func TestSpecPathProblems(t *testing.T) {
	repo := specFixture(t)
	writeSpec(t, filepath.Join(repo, ".gitignore"), "private/\n")
	ignored := writeSpec(t, filepath.Join(repo, "private", "app.md"), "# Mine\n")
	t.Setenv("LIMIT", "lots")
	_, p := loadWith(t, "--spec", ignored)
	if len(p) != 2 || !strings.Contains(p[0], "LIMIT must be a whole number") ||
		!strings.HasPrefix(p[1], "git ignores private/app.md, where the spec (--spec) is kept, "+
			"and workers only see committed files: move the spec, or stop ignoring that path") {
		t.Errorf("ignored: problems %q", p)
	}
	t.Setenv("LIMIT", "")
	inGit := writeSpec(t, filepath.Join(repo, ".git", "app.md"), "# Mine\n")
	if _, p := loadWith(t, "--spec", inGit); len(p) != 1 ||
		!strings.HasPrefix(p[0], "The spec .git/app.md (--spec) is in the repository's .git folder") {
		t.Errorf("in .git: problems %q", p)
	}
}

// A spec run has no ticket limit unless -limit or LIMIT gives one; one given is held to, and one
// already reached is a setup problem, as a feature run's is.
func TestSpecRunHasNoTicketLimitUnlessGiven(t *testing.T) {
	spec := writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n")
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want int
	}{
		{"a spec", []string{"--spec", spec}, nil, dispatch.NoLimit},
		{"with DONE_SO_FAR", []string{"--spec", spec}, map[string]string{"DONE_SO_FAR": "80"}, dispatch.NoLimit},
		{"-limit", []string{"--spec", spec, "--limit", "5"}, nil, 5},
		{"LIMIT", []string{"--spec", spec}, map[string]string{"LIMIT": "7"}, 7},
		{"no spec", nil, nil, 40},
	} {
		f, problems, err := readFlags(tc.args, specEnv(tc.env), io.Discard)
		if err != nil || len(problems) > 0 || f.Limit != tc.want {
			t.Errorf("%s: limit %d, want %d (%v, %q)", tc.name, f.Limit, tc.want, err, problems)
		}
	}
	_, problems, _ := readFlags([]string{"--spec", spec, "--limit", "3", "--done-so-far", "3"}, specEnv(nil), io.Discard)
	if len(problems) != 1 || !strings.Contains(problems[0], "--spec would break the spec down and run none of it: "+
		"-done-so-far (3) has reached -limit (3).") {
		t.Errorf("-limit reached: %q", problems)
	}
	if got := dispatch.LimitLabel(dispatch.NoLimit) + " " + dispatch.Progress(3, dispatch.NoLimit); got != "none 3" {
		t.Errorf("no limit reads %q", got)
	}
}

// A spec says what the run works on: it doesn't ask, in a terminal or not.
func TestSpecRunDoesntAsk(t *testing.T) {
	c := options{specFile: "/tmp/app.md"}
	c.Limit = dispatch.NoLimit
	if asksWork(c, true) {
		t.Error("a spec run asks what to work on")
	}
}
