package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Two tickets that follow the worker prompt, each adding a changelog entry at the top of
// [Unreleased] and its tests in a new file, rebase onto each other without a conflict under this
// repository's .gitattributes, and conflict without it.
func TestTicketsAddingAtTheSameSpotRebaseCleanly(t *testing.T) {
	attrs, err := os.ReadFile("../../.gitattributes")
	if err != nil || !strings.Contains(string(attrs), "CHANGELOG.md merge=union") {
		t.Fatalf("this repository's .gitattributes should merge CHANGELOG.md by union: %q, %v", attrs, err)
	}
	const changelog = "# Changelog\n\n## [Unreleased]\n\n### Added\n\n- An old entry.\n"
	entry := func(ticket string) string { return "- " + ticket + ": a new entry\n  on two lines, " + ticket + ".\n" }
	for _, tc := range []struct {
		name, attrs string
		clean       bool
	}{
		{"merge=union", string(attrs), true},
		{"no attributes", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			git := func(args ...string) (string, error) {
				return command.Output(context.Background(), 0, repo, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
			}
			must := func(args ...string) {
				t.Helper()
				if out, err := git(args...); err != nil {
					t.Fatalf("git %v: %v\n%s", args, err, out)
				}
			}
			write := func(name, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			must("init", "-q", "-b", "main")
			write("CHANGELOG.md", changelog)
			if tc.attrs != "" {
				write(".gitattributes", tc.attrs)
			}
			must("add", ".")
			must("commit", "-q", "-m", "init")
			for _, ticket := range []string{"x-1", "x-2"} {
				must("checkout", "-q", "-b", "wt/"+ticket, "main")
				write("CHANGELOG.md", strings.Replace(changelog, "### Added\n\n", "### Added\n\n"+entry(ticket), 1))
				write(ticket+"_test.go", "package x\n")
				must("add", ".")
				must("commit", "-q", "-m", ticket+": Add a feature")
			}

			out, err := git("rebase", "wt/x-1") // x-2 onto x-1, as the merge queue rebases a finished ticket
			if !tc.clean {
				if err == nil {
					t.Fatalf("without merge=union the entries should conflict; the test proves nothing\n%s", out)
				}
				must("rebase", "--abort")
				return
			}
			if err != nil {
				t.Fatalf("rebase: %v\n%s", err, out)
			}
			got, _ := os.ReadFile(filepath.Join(repo, "CHANGELOG.md"))
			for _, want := range []string{entry("x-1"), entry("x-2"), "- An old entry.\n"} {
				if strings.Count(string(got), want) != 1 {
					t.Errorf("CHANGELOG.md should hold %q once:\n%s", want, got)
				}
			}
			for _, f := range []string{"x-1_test.go", "x-2_test.go"} {
				if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
					t.Error(err)
				}
			}
		})
	}
}
