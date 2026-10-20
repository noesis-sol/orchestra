//go:build unix

package bdjson

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// TestMain runs the tests through faketool, for the fake bd they put on PATH.
func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }

// Run gives bd, in the repository, BD_JSON_ENVELOPE=0, which pins the --json shape orchestra reads.
func TestRunPinsTheJSONShape(t *testing.T) {
	dir, repo := t.TempDir(), t.TempDir()
	faketool.Write(t, dir, "bd", "#!/bin/sh\nprintf '%s %s %s' \"${BD_JSON_ENVELOPE-unset}\" \"$PWD\" \"$*\"\n")
	t.Setenv("PATH", faketool.Path(t, dir))
	out, err := Run(context.Background(), command.ReadLimit, repo, "count", "--json")
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(repo)
	if want := "0 " + resolved + " count --json"; out != want {
		t.Errorf("Run = %q, want %q", out, want)
	}
}
