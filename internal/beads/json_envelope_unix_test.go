//go:build unix

package beads

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// bd is run with BD_JSON_ENVELOPE=0, which pins the --json shape orchestra reads.
func TestBdIsRunWithoutTheJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, "env")
	faketool.Write(t, dir, "bd", "#!/bin/sh\nprintf %s \"${BD_JSON_ENVELOPE-unset}\" > '"+env+"'\necho '[]'\n")
	t.Setenv("PATH", faketool.Path(t, dir))
	if _, err := (Tracker{Repo: t.TempDir()}).Ready(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(env); string(b) != "0" {
		t.Errorf("bd ran with BD_JSON_ENVELOPE %q, want 0", b)
	}
}
