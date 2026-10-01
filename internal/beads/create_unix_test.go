//go:build unix

package beads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createBd puts a bd on PATH that records its arguments, one per line, and prints out.
func createBd(t *testing.T, out string) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + record + "'\nprintf '%s' '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

func TestCreateFilesTheTicketWithItsFiles(t *testing.T) {
	for _, out := range []string{`{"id":"k-1.2","title":"x"}`, `{"schema_version":1,"data":{"id":"k-1.2"}}`} {
		record := createBd(t, out)
		id, err := Tracker{Repo: t.TempDir()}.Create(context.Background(), NewTicket{Title: "Add -x", Description: "--why",
			Acceptance: "tests pass", Type: "task", Priority: 1, Parent: "k-1", Files: []string{"a.go", "b/c.go"}})
		if err != nil || id != "k-1.2" {
			t.Errorf("%s: id %q, %v", out, id, err)
		}
		b, _ := os.ReadFile(record)
		want := "create\n--json\n--title=Add -x\n--description=--why\n--type=task\n--priority=1\n--acceptance=tests pass\n" +
			"--parent=k-1\n--metadata={\"files\":[\"a.go\",\"b/c.go\"]}\n"
		if string(b) != want {
			t.Errorf("bd ran with\n%s\nwant\n%s", b, want)
		}
	}

	record := createBd(t, `{"id":"k-2"}`)
	if _, err := (Tracker{Repo: t.TempDir()}).Create(context.Background(), NewTicket{Title: "Epic", Type: "epic"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(record); strings.Contains(string(b), "--parent") || strings.Contains(string(b), "--metadata") ||
		strings.Contains(string(b), "--acceptance") {
		t.Errorf("empty fields were passed:\n%s", b)
	}

	createBd(t, `Created issue k-3`)
	if _, err := (Tracker{Repo: t.TempDir()}).Create(context.Background(), NewTicket{Title: "x", Type: "task"}); err == nil ||
		!strings.Contains(err.Error(), "gave no ID") {
		t.Errorf("output without an ID: %v", err)
	}
}
