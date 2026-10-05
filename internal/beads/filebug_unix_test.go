//go:build unix

package beads

import (
	"context"
	"os"
	"testing"
)

func TestFileBugFilesAP2BugWithItsLabel(t *testing.T) {
	record := createBd(t, `{"id":"k-9"}`)
	id, err := Tracker{Repo: t.TempDir()}.FileBug(context.Background(), "Full check fails: lint", "why", "check-full")
	if err != nil || id != "k-9" {
		t.Fatalf("id %q, %v", id, err)
	}
	b, _ := os.ReadFile(record)
	want := "create\n--json\n--title=Full check fails: lint\n--description=why\n--type=bug\n--priority=2\n" +
		"--labels=check-full\n"
	if string(b) != want {
		t.Errorf("bd ran with\n%s\nwant\n%s", b, want)
	}
}
