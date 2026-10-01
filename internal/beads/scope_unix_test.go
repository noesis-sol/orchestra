//go:build unix

package beads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeBd puts a bd on PATH that answers for ticket k-1 with subtickets k-1.1 (child) and k-1.1.1
// (grandchild), and logs each call's arguments to the returned file.
func scopeBd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$@" >> '` + calls + `'
case "$*" in
*"ready"*"--parent k-1") echo '[{"id":"k-1.1.1","status":"open","priority":3,"parent":"k-1.1"}]' ;;
*ready*) echo '[{"id":"k-9","status":"open","priority":0},{"id":"k-1","status":"open","priority":1}]' ;;
*"list"*"--parent k-1") echo '[{"id":"k-1.1","status":"in_progress","parent":"k-1"}]' ;;
*"list"*"--parent k-1.1") echo '{"schema_version":1,"data":[{"id":"k-1.1.1","status":"open","parent":"k-1.1"}]}' ;;
*"list"*"--parent "*) echo '[]' ;;
*list*) echo '[{"id":"k-1.1","status":"in_progress","parent":"k-1","created_at":"2026-09-30T14:53:21Z"}]' ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func ids(t *testing.T, what string, got []string, err error, want ...string) {
	t.Helper()
	if err != nil || strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%s = %v, %v; want %v", what, got, err, want)
	}
}

func TestScopedReadyIsTheTicketAndItsReadyDescendants(t *testing.T) {
	calls := scopeBd(t)
	b := Tracker{Repo: t.TempDir()}
	ready, err := b.Ready(context.Background(), "k-1")
	var got []string
	for _, tk := range ready {
		got = append(got, tk.ID)
	}
	ids(t, "Ready(k-1)", got, err, "k-1", "k-1.1.1") // priority order; k-9 is outside
	if log, _ := os.ReadFile(calls); !strings.Contains(string(log), "ready --json --limit 0 --exclude-label human --parent k-1") {
		t.Errorf("bd calls:\n%s", log)
	}
}

func TestDescendantsAreListedLevelByLevel(t *testing.T) {
	calls := scopeBd(t)
	subs, err := Tracker{Repo: t.TempDir()}.Descendants(context.Background(), "k-1")
	var got []string
	for _, tk := range subs {
		got = append(got, tk.ID+"<"+tk.Parent)
	}
	ids(t, "Descendants(k-1)", got, err, "k-1.1<k-1", "k-1.1.1<k-1.1")
	if log, _ := os.ReadFile(calls); !strings.Contains(string(log), "list --json --limit 0 --all --brief --parent k-1.1.1") {
		t.Errorf("each subticket's own children are listed too; bd calls:\n%s", log)
	}
}

func TestUnclosedCarriesEachTicketsParent(t *testing.T) {
	scopeBd(t)
	open, err := Tracker{Repo: t.TempDir()}.Unclosed(context.Background())
	if err != nil || len(open) != 1 || open[0].Parent != "k-1" || open[0].CreatedAt != "2026-09-30T14:53:21Z" {
		t.Errorf("Unclosed = %+v, %v", open, err)
	}
}

func TestScopeReadsCarryBdsStderr(t *testing.T) {
	failingBd(t)
	b := Tracker{Repo: t.TempDir()}
	for what, err := range map[string]error{
		"Ready(scope)": func() error { _, err := b.Ready(context.Background(), "k-1"); return err }(),
		"Unclosed":     func() error { _, err := b.Unclosed(context.Background()); return err }(),
		"Descendants":  func() error { _, err := b.Descendants(context.Background(), "k-1"); return err }(),
	} {
		if err == nil || !strings.Contains(err.Error(), "database is locked") {
			t.Errorf("%s: error %v, want bd's stderr", what, err)
		}
	}
}
