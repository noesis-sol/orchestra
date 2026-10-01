//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// showBd puts a bd on PATH whose 'bd show' knows k-open, k-closed and the question k-q.
func showBd(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$2" in
k-open) echo '[{"id":"k-open","status":"open"}]' ;;
k-closed) echo '[{"id":"k-closed","status":"closed"}]' ;;
k-q) echo '[{"id":"k-q","status":"open","labels":["human"]}]' ;;
*) echo "Issue $2 not found" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// --ticket (or ORCHESTRA_TICKET) scopes the run; an unknown, closed or question ticket is a setup
// problem, listed with the others, as is an ID that isn't a plain name.
func TestConfigTicketScope(t *testing.T) {
	configFixture(t, `{"concurrent": 1}`)
	showBd(t)
	if c, p := loadWith(t, "--ticket", "k-open"); len(p) > 0 || c.Ticket != "k-open" {
		t.Errorf("open ticket: %q %v", c.Ticket, p)
	}
	t.Setenv("ORCHESTRA_TICKET", "k-open")
	if c, p := loadWith(t); len(p) > 0 || c.Ticket != "k-open" {
		t.Errorf("from the environment: %q %v", c.Ticket, p)
	}
	t.Setenv("ORCHESTRA_TICKET", "")
	if c, p := loadWith(t); len(p) > 0 || c.Ticket != "" {
		t.Errorf("unscoped: %q %v", c.Ticket, p)
	}
	for id, want := range map[string]string{
		"k-none":   "Cannot read ticket k-none (--ticket): ",
		"k-closed": "Ticket k-closed (--ticket) is closed: nothing to run. Reopen it with: bd update k-closed --status open",
		"k-q":      "Ticket k-q (--ticket) is a question for you (label human), not work. Answer it with: bd human respond k-q",
		"k-a/b": "Ticket k-a/b (--ticket): IDs with path characters can't be run: a ticket's ID names its worktree " +
			"folder and its branch wt/<id>. Give it a plain ID with: bd rename k-a/b <new-id>",
		"k-../../y": "Ticket k-../../y (--ticket): IDs with path characters can't be run",
		"k-a.lock":  "Ticket k-a.lock (--ticket): IDs that can't name a git branch can't be run",
	} {
		_, p := loadWith(t, "--ticket", id, "--limit", "-1")
		if len(p) != 2 || !strings.Contains(strings.Join(p, "\n"), want) {
			t.Errorf("%s: problems %q, want %q listed with the others", id, p, want)
		}
	}
	if _, p := loadWith(t, "--ticket", "k-none"); len(p) != 1 || !strings.Contains(p[0], "Issue k-none not found") {
		t.Errorf("unknown ticket: %q, want bd's reason", p)
	}
}
