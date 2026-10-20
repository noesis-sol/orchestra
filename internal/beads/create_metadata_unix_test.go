//go:build unix

package beads

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Create gives the ticket the rest of its metadata beside its files, and the metadata alone when it
// has no files.
func TestCreateFilesTheTicketWithItsMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files []string
		want  string
	}{
		{"with files", []string{"a.go"}, `--metadata={"files":["a.go"],"spec":"docs/app.md"}`},
		{"without", nil, `--metadata={"spec":"docs/app.md"}`},
	} {
		record := createBd(t, `{"id":"k-1"}`)
		_, err := Tracker{Repo: t.TempDir()}.Create(context.Background(), NewTicket{Title: "Map", Type: "task",
			Files: tc.files, Metadata: map[string]string{"spec": "docs/app.md"}})
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(record); !strings.Contains(string(b), "\n"+tc.want+"\n") {
			t.Errorf("%s: bd ran with\n%s\nwant %s", tc.name, b, tc.want)
		}
	}
}
