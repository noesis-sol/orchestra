package claude

import (
	"encoding/json"
	"os"
	"testing"
)

// A worker's settings turn fork mode off, so that it can run a subagent it waits on (the verifier)
// in the foreground.
func TestWorkerSettingsTurnForkModeOff(t *testing.T) {
	wt := t.TempDir()
	var r Reporter
	for _, args := range [][]string{must(t)(r.ReportArgs(wt)), must(t)(r.ResumeArgs(wt))} {
		b, err := os.ReadFile(args[1])
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			Env map[string]string `json:"env"`
		}
		if err := json.Unmarshal(b, &settings); err != nil {
			t.Fatal(err)
		}
		if got := settings.Env["CLAUDE_CODE_FORK_SUBAGENT"]; got != "0" {
			t.Errorf("CLAUDE_CODE_FORK_SUBAGENT = %q, want 0", got)
		}
	}
}

func must(t *testing.T) func([]string, error) []string {
	return func(args []string, err error) []string {
		t.Helper()
		if err != nil || len(args) != 2 {
			t.Fatalf("args %q, %v", args, err)
		}
		return args
	}
}
