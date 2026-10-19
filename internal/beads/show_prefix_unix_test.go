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

// orchestra-c1em: bd answers an ID it has no ticket for with one whose ID starts with it, as k-1.1
// for a deleted k-1, and acts on that one when told to write. Show takes it for not found, and the
// writes leave bd alone; the ticket by that ID is still read and written.
func TestATicketBdAnswersForALongerID(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	faketool.Write(t, dir, "bd", "#!/bin/sh\necho \"$@\" >> '"+calls+"'\n"+
		"case \"$1\" in show) printf '[{\"id\": \"k-1.1\", \"status\": \"open\"}]\\n' ;; esac\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	b := Tracker{Repo: t.TempDir()}
	ctx := context.Background()

	tk, err := b.Show(ctx, "k-1")
	if !errors.Is(err, dispatch.ErrNoTicket) || !strings.Contains(err.Error(), "k-1.1") {
		t.Errorf("Show error %v; want not found, naming the ticket bd gave", err)
	}
	if tk.ID != "k-1" || tk.Status != dispatch.StatusUnknown {
		t.Errorf("Show = %s %q; want k-1, unknown", tk.ID, tk.Status)
	}
	for what, err := range map[string]error{
		"AppendNotes": b.AppendNotes(ctx, "k-1", "note"),
		"Defer":       b.Defer(ctx, "k-1", "reason"),
		"Reopen":      b.Reopen(ctx, "k-1"),
		"Undefer":     b.Undefer(ctx, "k-1"),
		"Close":       b.Close(ctx, "k-1", "reason"),
		"AddLabel":    b.AddLabel(ctx, "k-1", "unmerged"),
		"RemoveLabel": b.RemoveLabel(ctx, "k-1", "unmerged"),
		"SetMetadata": b.SetMetadata(ctx, "k-1", "key", "value"),
		"AddBlock":    b.AddBlock(ctx, "k-1.1", "k-1"),
		"AddBlock by": b.AddBlock(ctx, "k-1", "k-1.1"),
	} {
		if !errors.Is(err, dispatch.ErrNoTicket) {
			t.Errorf("%s: error %v; want not found", what, err)
		}
	}
	got, _ := os.ReadFile(calls)
	for line := range strings.Lines(string(got)) {
		if !strings.HasPrefix(line, "show ") {
			t.Errorf("bd %s; want only shows", strings.TrimSpace(line))
		}
	}

	if tk, err := b.Show(ctx, "k-1.1"); err != nil || tk.ID != "k-1.1" || tk.Status != dispatch.StatusOpen {
		t.Errorf("Show(k-1.1) = %s %q, %v; want it open", tk.ID, tk.Status, err)
	}
	if err := b.Close(ctx, "k-1.1", "done"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(calls); !strings.HasSuffix(string(got), "close k-1.1 --reason=done\n") {
		t.Errorf("bd calls end %q; want the close of k-1.1", got)
	}
}
