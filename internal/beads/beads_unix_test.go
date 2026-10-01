//go:build unix

package beads

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// failingBd puts a bd on PATH that fails every command with a lock error on stderr.
func failingBd(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'Error: database is locked' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestBdFailuresCarryItsStderr(t *testing.T) {
	failingBd(t)
	b := Tracker{Repo: t.TempDir()}
	check := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "database is locked") {
			t.Errorf("%s: error %v, want bd's stderr", what, err)
		}
	}
	_, err := b.Ready(context.Background(), "")
	check("Ready", err)
	tk, err := b.Show(context.Background(), "k-1")
	check("Show", err)
	if tk.Status != "unknown" {
		t.Errorf("Show status = %q", tk.Status)
	}
	st, err := b.Status(context.Background(), "k-1")
	check("Status", err)
	if st != "unknown" {
		t.Errorf("Status = %q", st)
	}
	check("AppendNotes", b.AppendNotes(context.Background(), "k-1", "note"))
	check("Defer", b.Defer(context.Background(), "k-1", "reason"))
	check("Reopen", b.Reopen(context.Background(), "k-1"))
	check("AddLabel", b.AddLabel(context.Background(), "k-1", "unmerged"))
	check("RemoveLabel", b.RemoveLabel(context.Background(), "k-1", "unmerged"))
	_, err = b.Closed(context.Background(), "unmerged")
	check("Closed", err)
}

// A bd that hangs (waiting on Dolt's lock, say) is stopped by Ctrl+C within a second, and the
// ticket's status is unknown, with the reason, as when bd fails.
func TestHungBdIsStoppedByCtrlC(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte("#!/bin/sh\nsleep 5\necho '[]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(dispatch.Interrupted("with Ctrl+C")) })
	start := time.Now()
	st, err := Tracker{Repo: t.TempDir()}.Status(ctx, "k-1")
	if took := time.Since(start); took > time.Second {
		t.Errorf("returned %s after Ctrl+C", took-100*time.Millisecond)
	}
	if st != "unknown" || err == nil || err.Error() != "bd show k-1 --json: stopped with Ctrl+C" {
		t.Errorf("got %q, %v; want unknown and why bd stopped", st, err)
	}
}

func TestStatusWithoutOneIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte("#!/bin/sh\necho '[]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if st, err := (Tracker{Repo: t.TempDir()}).Status(context.Background(), "k-1"); st != "unknown" || err == nil {
		t.Errorf("got %q, %v; want unknown and an error", st, err)
	}
}

func TestReadyPassesTheExcludedTypesToBd(t *testing.T) {
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > '" + args + "'\necho '[]'\n"
	if err := os.WriteFile(filepath.Join(dir, "bd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, c := range []struct {
		types []string
		want  string
	}{
		{[]string{"epic", "decision"}, "--exclude-type epic,decision"},
		{nil, ""},
	} {
		if _, err := (Tracker{Repo: t.TempDir(), ExcludeTypes: c.types}).Ready(context.Background(), ""); err != nil {
			t.Fatal(err)
		}
		b, _ := os.ReadFile(args)
		got := string(b)
		if !strings.Contains(got, "--exclude-label human") {
			t.Errorf("%v: bd %s", c.types, got)
		}
		if c.want != "" && !strings.Contains(got, c.want) || c.want == "" && strings.Contains(got, "--exclude-type") {
			t.Errorf("%v: bd %s, want %q", c.types, got, c.want)
		}
	}
}
