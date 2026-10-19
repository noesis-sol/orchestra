//go:build unix

package beads

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra-ale2: Create's parent and the tickets it waits for, and Describe, are confirmed as Show
// confirms an ID: bd would link the new ticket to k-1.1 for a deleted k-1, and show k-1.1 for it.
func TestCreateAndDescribeByALongerID(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	faketool.Write(t, dir, "bd", "#!/bin/sh\necho \"$@\" >> '"+calls+"'\n"+
		"case \"$1 $3\" in\n"+
		"'show --json') printf '[{\"id\": \"k-1.1\", \"status\": \"open\"}]\\n' ;;\n"+
		"'show ') echo 'k-1.1 · the follow-up' ;;\n"+
		"create*) printf '{\"id\": \"k-2\"}\\n' ;;\n"+
		"esac\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b := Tracker{Repo: t.TempDir()}
	ctx := context.Background()

	for what, nt := range map[string]NewTicket{
		"parent":     {Title: "x", Type: "task", Parent: "k-1"},
		"blocked by": {Title: "x", Type: "task", BlockedBy: []string{"k-1.1", "k-1"}},
	} {
		if id, err := b.Create(ctx, nt); !errors.Is(err, dispatch.ErrNoTicket) || id != "" {
			t.Errorf("Create with %s k-1 = %q, %v; want not found", what, id, err)
		}
	}
	if got := b.Describe(ctx, "k-1"); strings.Contains(got, "follow-up") || !strings.Contains(got, "k-1.1") {
		t.Errorf("Describe(k-1) = %q; want bd's failure, naming the ticket it gave", got)
	}
	got, _ := os.ReadFile(calls)
	for line := range strings.Lines(string(got)) {
		if !strings.HasPrefix(line, "show ") || !strings.HasSuffix(line, " --json\n") {
			t.Errorf("bd %s; want only shows --json", strings.TrimSpace(line))
		}
	}

	if id, err := b.Create(ctx, NewTicket{Title: "x", Type: "task", Parent: "k-1.1",
		BlockedBy: []string{"k-1.1"}}); err != nil || id != "k-2" {
		t.Errorf("Create under k-1.1 = %q, %v; want k-2", id, err)
	}
	if got := b.Describe(ctx, "k-1.1"); got != "k-1.1 · the follow-up\n" {
		t.Errorf("Describe(k-1.1) = %q; want bd's show", got)
	}
}
