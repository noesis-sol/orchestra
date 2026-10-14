package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A subagent cut off by Esc runs no SubagentStop, nor its turn a Stop: once no hook has reported
// anything for dispatch.SubagentQuiet, it counts as ended, and the session reads idle at its prompt
// or after its Stop. One that reports a tool use after that long a quiet counts as running again.
func TestSubagentQuietSoLongCountsAsEnded(t *testing.T) {
	t.Parallel()
	turn := []hookStep{stepSessionStart("startup"), stepPrompt, stepUseBash, stepUsedIt, stepSubStart, stepSubBash}
	then := func(steps ...hookStep) []hookStep { return append(append([]hookStep(nil), turn...), steps...) }
	for _, c := range []struct {
		name        string
		steps       []hookStep
		recent, old dispatch.AgentState
	}{
		{"cut off by Esc, idle at the prompt", then(stepNotification("idle_prompt")),
			dispatch.StateWorking, dispatch.StateIdle},
		{"cut off, then the turn's Stop", then(stepStop), dispatch.StateWorking, dispatch.StateIdle},
		{"cut off, then a prompt", then(stepStop, stepPrompt), dispatch.StateWorking, dispatch.StateWorking},
		{"stopped", then(stepSubStop, stepStop), dispatch.StateIdle, dispatch.StateIdle},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			if got := playHooks(t, wt, c.steps); got != c.recent {
				t.Errorf("state: %s, want %s", got, c.recent)
			}
			r := Reporter{}
			if rec := r.HookRecord(wt); rec.Noted.IsZero() {
				t.Errorf("record %+v, want the time of its last note", rec)
			}
			age(t, Records(wt, ""), dispatch.SubagentQuiet)
			if got := State(Records(wt, "")); got != c.old {
				t.Errorf("state with no hook for %s: %s, want %s", dispatch.SubagentQuiet, got, c.old)
			}
			u, _ := r.LastToolUse(wt)
			if r.HookRecord(wt).SubagentsRunning(u.At, time.Now()) {
				t.Errorf("subagent running with no hook for %s", dispatch.SubagentQuiet)
			}
		})
	}
}

// A subagent that goes on reporting tool uses counts as running, however long ago it started.
func TestSubagentReportingToolUsesKeepsRunning(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	playHooks(t, wt, []hookStep{stepSessionStart("startup"), stepPrompt, stepSubStart, stepSubBash, stepStop})
	records := Records(wt, "")
	age(t, records, dispatch.SubagentQuiet)
	runHooks(t, readSettings(t, filepath.Join(records, settingsName)), stepSubBash.event, stepSubBash.input)
	if got := State(records); got != dispatch.StateWorking {
		t.Errorf("state: %s, want working while the subagent reports tool uses", got)
	}
	r := Reporter{}
	u, _ := r.LastToolUse(wt)
	if rec := r.HookRecord(wt); !rec.SubagentsRunning(u.At, time.Now()) {
		t.Errorf("record %+v, last tool use at %s: want the subagent running", rec, u.At)
	}
}

// age dates every record in the folder records d earlier, as if no hook had run for that long.
func age(t *testing.T, records string, d time.Duration) {
	t.Helper()
	entries, err := os.ReadDir(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		path := filepath.Join(records, e.Name())
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, fi.ModTime().Add(-d), fi.ModTime().Add(-d)); err != nil {
			t.Fatal(err)
		}
	}
}
