//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// --prefix sets the Beads ticket prefix without a terminal: bd init takes it for a new Beads, and an
// existing one's tickets are renamed to it, unless something still names their IDs.

// fakePrefixBd puts a bd first on PATH and returns its folder. Its Beads has the prefix orchestra and
// three tickets, one of them orchestra-a1; it lists the tickets not closed from the folder's unclosed
// and those left unmerged from its unmerged ([] when missing), and logs its calls to calls. Its init
// adds .beads/ and commits it.
func fakePrefixBd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
echo "bd $*" >> "$d/calls"
list() { if [ -f "$d/$1" ]; then cat "$d/$1"; else echo '[]'; fi; }
case "$1" in
init) mkdir -p .beads && echo 'issue-prefix: t' > .beads/config.yaml && git add .beads &&
	git -c user.name=t -c user.email=t@t commit -q -m 'bd init: initialize beads issue tracking' ;;
config) echo '{"schema_version":1,"data":{"key":"issue_prefix","value":"orchestra"}}' ;;
count) echo '{"schema_version":1,"data":{"count":3}}' ;;
list) case "$*" in
	*"--label unmerged"*) list unmerged ;;
	*--brief*) list unclosed ;;
	*) echo '{"schema_version":1,"data":[{"id":"orchestra-a1","title":"a"}]}' ;;
	esac ;;
rename-prefix) echo renamed ;;
esac
`)
	onPath(t, dir)
	return dir
}

// beadsRepo is a repository whose Beads is set up.
func beadsRepo(t *testing.T) (string, func(dir string, args ...string) string) {
	t.Helper()
	repo, git := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo, git
}

func TestInitGivesANewBeadsThePrefix(t *testing.T) {
	dir := fakePrefixBd(t)
	repo, _ := gitRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "true", "--mcp", "", "--prefix", "orc-")
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if calls := toolCalls(t, dir); calls != "bd init --non-interactive --role maintainer --init-if-missing "+
		"--prefix orc\n" {
		t.Errorf("calls:\n%s", calls)
	}
	if plain := strings.Join(strings.Fields(stdout), " "); !strings.Contains(plain, "✓ ticket prefix orc, for the new Beads") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestInitKeepsTheFolderNameForANewBeadsWhenGivenIt(t *testing.T) {
	dir := fakePrefixBd(t)
	repo, _ := gitRepo(t)
	name := project.DefaultPrefix(repo)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "true", "--mcp", "", "--prefix", name)
	if err != nil || toolCalls(t, dir) != bdInitCall {
		t.Fatalf("init: %v, calls:\n%s\nstdout:\n%s\nstderr:\n%s", err, toolCalls(t, dir), stdout, stderr)
	}
}

func TestInitRenamesTheTicketsToTheGivenPrefix(t *testing.T) {
	dir := fakePrefixBd(t)
	repo, _ := beadsRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "true", "--mcp", "", "--prefix", "orc")
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if calls := toolCalls(t, dir); !strings.HasSuffix(calls, "bd rename-prefix orc-\n") {
		t.Errorf("calls:\n%s", calls)
	}
	plain := strings.Join(strings.Fields(stdout), " ")
	if !strings.Contains(plain, "✓ ticket prefix orc (was orchestra), 3 tickets renamed") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestInitKeepsTheGivenPrefixWhereBeadsHasIt(t *testing.T) {
	dir := fakePrefixBd(t)
	repo, _ := beadsRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "true", "--mcp", "", "--prefix", "orchestra")
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if calls := toolCalls(t, dir); strings.Contains(calls, "rename-prefix") {
		t.Errorf("calls:\n%s", calls)
	}
	if plain := strings.Join(strings.Fields(stdout), " "); !strings.Contains(plain, "• ticket prefix orchestra, kept") {
		t.Errorf("stdout:\n%s", stdout)
	}
}

func TestInitRefusesToRenameTicketsSomethingStillNames(t *testing.T) {
	if testing.Short() {
		t.Skip("runs init three times, over a second")
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, repo, dir string, git func(string, ...string) string)
		want  string
	}{
		{"an open ticket's branch", func(t *testing.T, repo, dir string, git func(string, ...string) string) {
			git(repo, "branch", "wt/orchestra-a1")
			git(repo, "branch", "wt/orchestra-gone") // a closed ticket's: no matter
			writeFile(t, filepath.Join(dir, "unclosed"), `[{"id":"orchestra-a1","status":"open"}]`)
		}, "tickets not closed have branches, and their worktrees: wt/orchestra-a1. Nothing was changed."},
		{"a carried worker", func(t *testing.T, repo, _ string, _ func(string, ...string) string) {
			if err := project.SaveState(repo, project.RunState{Workers: []project.LeftWorker{
				{Ticket: "orchestra-a1", Agent: "w", Tab: "1", Worktree: "/w"}}}); err != nil {
				t.Fatal(err)
			}
		}, "the last run left workers on orchestra-a1 (in .orchestra/run/state.json; run orchestra to finish them)"},
		{"an unmerged ticket", func(t *testing.T, _, dir string, _ func(string, ...string) string) {
			writeFile(t, filepath.Join(dir, "unmerged"), `[{"id":"orchestra-b2","status":"closed","labels":["unmerged"]}]`)
		}, "tickets are left unmerged (labelled unmerged; their unmerged_fork names their branch): orchestra-b2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fakePrefixBd(t)
			repo, git := beadsRepo(t)
			tc.setup(t, repo, dir, git)
			stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "true", "--mcp", "", "--prefix", "orc")
			if exitOf(err) != dispatch.ExitSetup {
				t.Errorf("exit %d (%v), want %d", exitOf(err), err, dispatch.ExitSetup)
			}
			plain := strings.Join(strings.Fields(stderr), " ")
			if !strings.Contains(plain, "orchestra init: --prefix: the tickets can't be renamed from orchestra to orc "+
				"while "+tc.want) {
				t.Errorf("stderr:\n%s", stderr)
			}
			if strings.Contains(toolCalls(t, dir), "rename-prefix") || fileThere(filepath.Join(repo, ".orchestra", "settings.json")) {
				t.Errorf("init changed something; calls:\n%s\nstdout:\n%s", toolCalls(t, dir), stdout)
			}
		})
	}
}

func TestInitRefusesAnInvalidPrefix(t *testing.T) {
	dir := fakePrefixBd(t)
	repo, _ := gitRepo(t)
	for value, want := range map[string]string{
		"Orc":   "orchestra init: --prefix must start with a lowercase letter (got 'Orc')",
		"o_r":   "orchestra init: --prefix must hold only lowercase letters, digits and hyphens (got 'o_r')",
		"":      "orchestra init: --prefix is empty",
		"orc--": "orchestra init: --prefix must not end in a hyphen (got 'orc-')",
	} {
		_, stderr, err := runIn(t, repo, nil, "init", "--check", "true", "--prefix", value)
		if exitOf(err) != dispatch.ExitSetup || !strings.Contains(stderr, want) {
			t.Errorf("--prefix %q: exit %d, stderr:\n%s", value, exitOf(err), stderr)
		}
	}
	if calls := toolCalls(t, dir); calls != "" {
		t.Errorf("calls:\n%s", calls)
	}
}

// writeFile writes text to p.
func writeFile(t *testing.T, p, text string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fileThere reports whether p exists.
func fileThere(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
