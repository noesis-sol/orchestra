//go:build unix

package beads

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra-h5gs: a Checked ticket, and AddCheckedBlock's two, are the caller's to vouch for: bd
// files and links them without a bd show for any.
func TestCheckedCreateAndBlockShowNothing(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	faketool.Write(t, dir, "bd", "#!/bin/sh\necho \"$@\" >> '"+calls+"'\n"+
		"case \"$1\" in create) printf '{\"id\": \"k-3\"}\\n' ;; esac\n")
	t.Setenv("PATH", faketool.Path(t, dir))
	b := Tracker{Repo: t.TempDir()}
	ctx := context.Background()

	id, err := b.Create(ctx, NewTicket{Title: "Map", Type: "task", Priority: 2, Parent: "k-1",
		BlockedBy: []string{"k-2"}, Checked: true})
	if err != nil || id != "k-3" {
		t.Fatalf("Create = %q, %v; want k-3", id, err)
	}
	if err := b.AddCheckedBlock(ctx, "k-2", "k-3"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(calls)
	want := "create --json --title=Map --description= --type=task --priority=2 --parent=k-1 --deps=blocked-by:k-2\n" +
		"dep add k-3 k-2\n"
	if string(got) != want {
		t.Errorf("bd ran with\n%s\nwant\n%s", got, want)
	}
}
