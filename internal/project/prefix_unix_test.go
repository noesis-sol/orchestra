//go:build unix

package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

func TestPrefixProblemTakesWhatBdTakes(t *testing.T) {
	for p, want := range map[string]string{
		"orc":       "",
		"orc-2":     "",
		"a":         "",
		"":          "is empty",
		"Orc":       "must start with a lowercase letter (got 'Orc')",
		"2orc":      "must start with a lowercase letter (got '2orc')",
		"orc_x":     "must hold only lowercase letters, digits and hyphens (got 'orc_x')",
		"orc x":     "must hold only lowercase letters, digits and hyphens (got 'orc x')",
		"orc-":      "must not end in a hyphen (got 'orc-')",
		"orchéstra": "must hold only lowercase letters, digits and hyphens (got 'orchéstra')",
	} {
		if got := PrefixProblem(p); got != want {
			t.Errorf("PrefixProblem(%q) = %q, want %q", p, got, want)
		}
	}
	if got := NormalPrefix(" orc- "); got != "orc" {
		t.Errorf("NormalPrefix = %q, want orc", got)
	}
	if got := DefaultPrefix("/home/me/My_Proj.X"); got != "My_Proj_X" {
		t.Errorf("DefaultPrefix = %q, want bd init's My_Proj_X", got)
	}
}

func TestRenameQuestionShowsAnIDRenamed(t *testing.T) {
	c := Choice{Prefix: "orc", PrefixWas: "orchestra", PrefixTickets: 12, PrefixExample: "orchestra-74b2.31"}
	if got := RenameQuestion(c); got != "Rename 12 tickets, orchestra-74b2.31 → orc-74b2.31?" {
		t.Errorf("RenameQuestion = %q", got)
	}
	c.PrefixTickets, c.PrefixExample = 1, ""
	if got := RenameQuestion(c); got != "Rename the ticket, orchestra-… → orc-…?" {
		t.Errorf("RenameQuestion = %q", got)
	}
}

// fakePrefixBd puts a bd first on PATH whose Beads has the prefix orchestra and three tickets, in
// bd's JSON envelope, and returns its folder. It logs its calls to calls, and fails rename-prefix
// when the folder holds rename-fails.
func fakePrefixBd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
echo "bd $*" >> "$d/calls"
case "$1" in
config) echo '{"schema_version":1,"data":{"key":"issue_prefix","value":"orchestra"}}' ;;
count) echo '{"schema_version":1,"data":{"count":3}}' ;;
list) echo '{"schema_version":1,"data":[{"id":"orchestra-74b2.31","title":"a"}]}' ;;
rename-prefix) [ -f "$d/rename-fails" ] && { echo 'Error: database is locked' >&2; exit 1; }; echo renamed ;;
esac
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestBdSaysThePrefixAndTheTickets(t *testing.T) {
	dir := fakePrefixBd(t)
	repo := t.TempDir()
	p, err := BeadsPrefix(context.Background(), repo)
	if err != nil || p != "orchestra" {
		t.Errorf("BeadsPrefix = %q, %v", p, err)
	}
	n, example, err := PrefixTickets(context.Background(), repo)
	if err != nil || n != 3 || example != "orchestra-74b2.31" {
		t.Errorf("PrefixTickets = %d, %q, %v", n, example, err)
	}
	if calls := readCalls(t, dir); calls != "bd config get issue_prefix --json\nbd count --json\n"+
		"bd list --all --limit 1 --json\n" {
		t.Errorf("calls:\n%s", calls)
	}
}

func TestApplyPrefixRenamesOnlyAConfirmedChange(t *testing.T) {
	base := Choice{Prefix: "orc", PrefixWas: "orchestra", PrefixTickets: 3, RenamePrefix: true}
	for _, tc := range []struct {
		name    string
		change  func(*Choice)
		refusal string
		fails   bool
		want    Step
		renamed bool
	}{
		{name: "renamed", want: Step{Kind: StepDone, Detail: "orc (was orchestra), 3 tickets renamed"}, renamed: true},
		{name: "kept", change: func(c *Choice) { c.Prefix = "orchestra" },
			want: Step{Kind: StepKept, Detail: "orchestra, kept"}},
		{name: "declined", change: func(c *Choice) { c.RenamePrefix = false },
			want: Step{Kind: StepKept, Detail: "orchestra, kept: renaming the tickets to orc was declined"}},
		{name: "refused", refusal: "tickets are left unmerged",
			want: Step{Kind: StepMissing, Detail: "orchestra, kept: the tickets can't be renamed to orc while " +
				"tickets are left unmerged"}},
		{name: "failed", fails: true, renamed: true,
			want: Step{Kind: StepMissing, Detail: "orchestra, kept: bd rename-prefix orc- failed (exit status 1): " +
				"Error: database is locked"}},
	} {
		dir := fakePrefixBd(t)
		if tc.fails {
			if err := os.WriteFile(filepath.Join(dir, "rename-fails"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		c := base
		if tc.change != nil {
			tc.change(&c)
		}
		s, ok := ApplyPrefix(context.Background(), t.TempDir(), c, tc.refusal)
		tc.want.Label = prefixLabel
		if !ok || s.Kind != tc.want.Kind || s.Label != tc.want.Label || s.Detail != tc.want.Detail {
			t.Errorf("%s: step %+v, %v; want %+v", tc.name, s, ok, tc.want)
		}
		if renamed := strings.Contains(readCalls(t, dir), "bd rename-prefix orc-\n"); renamed != tc.renamed {
			t.Errorf("%s: renamed %v, calls:\n%s", tc.name, renamed, readCalls(t, dir))
		}
	}
	// Not asked, or a new Beads, which bd init gives its prefix: no step.
	for _, c := range []Choice{{}, {Prefix: "orc", PrefixWas: "orchestra", PrefixNew: true}} {
		if s, ok := ApplyPrefix(context.Background(), t.TempDir(), c, ""); ok {
			t.Errorf("%+v: step %+v", c, s)
		}
	}
}

// readCalls is what the fake bd in dir was called with, one call a line.
func readCalls(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}
