//go:build unix

package command_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// tool writes in dir a fake tool called name that prints said.
func tool(t *testing.T, dir, name, said string) {
	t.Helper()
	faketool.Write(t, dir, name, "#!/bin/sh\necho "+said+"\n")
}

// What WithEnv adds reaches every command run with the context, a later WithEnv's over an
// earlier one's, and a command's own env over both; the process's environment is left alone.
func TestWithEnvReachesTheCommands(t *testing.T) {
	t.Parallel()
	ctx := command.WithEnv(context.Background(), "ORCHESTRA_A=1", "ORCHESTRA_B=1")
	ctx = command.WithEnv(ctx, "ORCHESTRA_B=2")
	script := `printf %s "$ORCHESTRA_A $ORCHESTRA_B ${ORCHESTRA_C-unset}"`
	if out, err := command.Output(ctx, command.ReadLimit, "", "sh", "-c", script); err != nil || out != "1 2 unset" {
		t.Errorf("Output: %q, %v; want 1 2 unset", out, err)
	}
	out, err := command.OutputWithEnv(ctx, command.ReadLimit, "", []string{"ORCHESTRA_B=3"}, "sh", "-c", script)
	if err != nil || out != "1 3 unset" {
		t.Errorf("OutputWithEnv: %q, %v; want 1 3 unset", out, err)
	}
	if out, err := command.GroupOutput(ctx, time.Second, "", "sh", "-c", script); err != nil ||
		string(out) != "1 2 unset" {
		t.Errorf("GroupOutput: %q, %v; want 1 2 unset", out, err)
	}
	if got := command.Getenv(ctx, "ORCHESTRA_B"); got != "2" {
		t.Errorf("Getenv = %q, want 2", got)
	}
	if got := os.Getenv("ORCHESTRA_B"); got != "" {
		t.Errorf("the process got ORCHESTRA_B=%s", got)
	}
}

// A command is looked up in the PATH WithEnv gives, not the process's: a tool missing from it
// isn't found, though the process's PATH has it, and a folder named by a relative path is passed
// over.
func TestWithEnvLooksCommandsUpInItsPath(t *testing.T) {
	t.Parallel()
	first, second := t.TempDir(), t.TempDir()
	tool(t, first, "orchestra-tool", "first")
	tool(t, second, "orchestra-tool", "second")
	tool(t, second, "orchestra-other", "other")
	ctx := command.WithEnv(context.Background(), "PATH=relative"+string(os.PathListSeparator)+first+
		string(os.PathListSeparator)+second)
	for name, want := range map[string]string{"orchestra-tool": "first\n", "orchestra-other": "other\n"} {
		if out, err := command.Output(ctx, command.ReadLimit, "", name); err != nil || out != want {
			t.Errorf("%s: %q, %v; want %q", name, out, err, want)
		}
	}
	if p, err := command.LookPath(ctx, "orchestra-tool"); err != nil || p != filepath.Join(first, "orchestra-tool") {
		t.Errorf("LookPath = %q, %v; want the one in %s", p, err, first)
	}

	bare := command.WithEnv(context.Background(), "PATH="+first)
	if _, err := command.Output(bare, command.ReadLimit, "", "sh", "-c", "true"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("sh, off the given PATH, ran: %v", err)
	}
	if _, err := command.LookPath(bare, "sh"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("LookPath found sh off the given PATH: %v", err)
	}
	if _, err := command.LookPath(context.Background(), "sh"); err != nil {
		t.Errorf("LookPath without WithEnv didn't find sh on the process's PATH: %v", err)
	}
}
