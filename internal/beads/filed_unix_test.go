//go:build unix

package beads

import (
	"context"
	"os"
	"testing"
)

func TestCreateFilesTheTicketBehindThoseItWaitsFor(t *testing.T) {
	record := createBd(t, `{"id":"k-3"}`)
	_, err := Tracker{Repo: t.TempDir()}.Create(context.Background(), NewTicket{Title: "Map", Type: "task", Priority: 2,
		Parent: "k-1", BlockedBy: []string{"k-2", "k-0"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(record)
	want := "create\n--json\n--title=Map\n--description=\n--type=task\n--priority=2\n--parent=k-1\n" +
		"--deps=blocked-by:k-2\n--deps=blocked-by:k-0\n"
	if string(b) != want {
		t.Errorf("bd ran with\n%s\nwant\n%s", b, want)
	}
}

func TestFiledListsTheTicketsWithTheLabelNotClosed(t *testing.T) {
	record := createBd(t, `[{"id":"k-1","title":"Set up","status":"open"},{"id":"k-2","title":"Map","status":"in_progress"}]`)
	got, err := Tracker{Repo: t.TempDir()}.Filed(context.Background(), "orchestra-tests")
	if err != nil || len(got) != 2 || got[0].Title != "Set up" || got[1].ID != "k-2" {
		t.Fatalf("%+v, %v", got, err)
	}
	b, _ := os.ReadFile(record)
	if want := "list\n--json\n--limit\n0\n--brief\n--label\norchestra-tests\n"; string(b) != want {
		t.Errorf("bd ran with\n%s\nwant\n%s", b, want)
	}
}
