package herdr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// limitScreen is the end of Claude Code 2.1.301's screen once its turn ended on the session limit and
// its dialog was answered "Stop and wait for limit to reset", its blank lines left out.
const limitScreen = "❯ say hi\n" +
	"  ⎿  You've hit your session limit · resets 7:41pm (Europe/Athens)\n" +
	"✻ Brewed for 0s · done 4:42 PM\n" +
	"────────────────────────────────────────\n" +
	"❯ \n" +
	"────────────────────────────────────────\n" +
	"  ⏵⏵ auto mode on (shift+tab to cycle) · ← for agents\n"

// statusHerdr puts a herdr on PATH whose agent is of kind, in status, and shows screen; it returns the
// log of the calls made to it.
func statusHerdr(t *testing.T, kind, status, screen string) string {
	t.Helper()
	dir := t.TempDir()
	calls, shown := filepath.Join(dir, "calls"), filepath.Join(dir, "screen")
	if err := os.WriteFile(shown, []byte(screen), 0o644); err != nil {
		t.Fatal(err)
	}
	herdrScript(t, "#!/bin/sh\necho \"$*\" >> '"+calls+"'\n"+
		"case \"$*\" in\n"+
		"'agent get'*) echo '{\"result\":{\"agent\":{\"name\":\"a\",\"agent\":\""+kind+"\",\"agent_status\":\""+status+"\"}}}' ;;\n"+
		"'agent read'*) cat '"+shown+"' ;;\n"+
		"esac\n")
	return calls
}

// Herdr has no state for a Claude Code agent whose turn ended on its account's usage limit: one it
// shows at rest whose screen shows the limit is limited. Its screen is read only then.
func TestStatusOfAClaudeAgentAtItsUsageLimit(t *testing.T) {
	for _, c := range []struct {
		name, kind, status, screen string
		want                       dispatch.AgentState
		read                       bool
	}{
		{"idle at the limit", "claude", "idle", limitScreen, dispatch.StateLimited, true},
		{"done at the limit", "claude", "done", limitScreen, dispatch.StateLimited, true},
		{"on the limit's dialog", "claude", "blocked", limitScreen +
			"   What do you want to do?\n   ❯ 1. Stop and wait for limit to reset\n", dispatch.StateLimited, true},
		{"idle with no limit", "claude", "idle", strings.Replace(limitScreen, "You've hit your session limit",
			"API Error: Server is temporarily limiting requests (not your usage limit)", 1), dispatch.StateIdle, true},
		{"working", "claude", "working", limitScreen, dispatch.StateWorking, false},
		{"another kind of agent", "codex", "idle", limitScreen, dispatch.StateIdle, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := statusHerdr(t, c.kind, c.status, c.screen)
			if st, err := (Terminal{}).Status(context.Background(), "a"); st != c.want || err != nil {
				t.Errorf("Status = %q %v, want %q", st, err, c.want)
			}
			log, _ := os.ReadFile(calls)
			if read := strings.Contains(string(log), "agent read"); read != c.read {
				t.Errorf("screen read: %v, want %v; calls:\n%s", read, c.read, log)
			}
		})
	}
}
