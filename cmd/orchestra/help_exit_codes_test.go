package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// orchestra -h names every exit code a run can end with, each by its number.
func TestHelpNamesEveryExitCode(t *testing.T) {
	t.Parallel()
	_, stderr, err := runIn(t, t.TempDir(), nil, "-h")
	if err != nil {
		t.Fatalf("-h: %v", err)
	}
	_, codes, ok := strings.Cut(stderr, "Exit codes:")
	if !ok {
		t.Fatalf("-h lacks its exit codes:\n%s", stderr)
	}
	codes = " " + strings.Join(strings.Fields(codes), " ") // one line, each code after a space
	for code, want := range map[int]string{
		dispatch.ExitOK: "done", dispatch.ExitSetup: "setup problem", dispatch.ExitStuck: "worker blocked",
		dispatch.ExitTool: "terminal", dispatch.ExitDirty: "main checkout dirty", dispatch.ExitMerge: "merge failed",
		dispatch.ExitEnvironment: "environment failing workers", dispatch.ExitUsageLimit: "usage limit",
		dispatch.ExitInterrupted: "Ctrl+C",
	} {
		if want := fmt.Sprintf(" %d %s", code, want); !strings.Contains(codes, want) {
			t.Errorf("-h's exit codes lack %q: %s", want, codes)
		}
	}
}
