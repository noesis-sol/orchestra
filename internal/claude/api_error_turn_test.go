package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A turn that ends on an API error other than the usage limit ends as a Stop does: the last report
// says when, and the error's kind, also once an agent Claude Code forks at the end of the turn has
// replaced it with a tool use of its own, and a prompt that starts the next turn clears them. One on
// the usage limit tells its kind, and the limit, but no turn's end: the run holds for it.
func TestLastToolUseEndsTheTurnOnAnAPIError(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, input, kind string
		ends              bool
	}{
		{"server error", failureServerError, "server_error", true},
		{"overloaded", failureOverloaded, "server_error", true},
		{"requests limited", failureRequestsLimited, "rate_limit", true},
		{"usage limit", failureSessionLimit, "rate_limit", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			playHooks(t, wt, []hookStep{stepSessionStart("startup"), stepPrompt, stepUseBash, stepFailure(c.input)})
			records := Records(wt, "")
			turn, err := os.Stat(filepath.Join(records, turnName))
			if err != nil {
				t.Fatal(err)
			}
			var stopped time.Time
			if c.ends {
				stopped = turn.ModTime()
			}
			u, ok := Reporter{}.LastToolUse(wt)
			if !ok || u.Event != "StopFailure" || u.Failure != c.kind || !u.Stopped.Equal(stopped) ||
				u.Limit.Reached() == c.ends {
				t.Errorf("at the StopFailure: %+v %v, want its kind %s and the turn ended at %s", u, ok, c.kind, stopped)
			}

			settings := readSettings(t, filepath.Join(records, settingsName))
			runHooks(t, settings, stepForkedRead.event, stepForkedRead.input)
			u, ok = Reporter{}.LastToolUse(wt)
			if !ok || u.Event != "PreToolUse" || u.Failure != c.kind || !u.Stopped.Equal(stopped) ||
				u.Limit.Reached() == c.ends {
				t.Errorf("a fork's tool use since: %+v %v, want its kind %s and the turn ended at %s", u, ok, c.kind,
					stopped)
			}

			runHooks(t, settings, stepPrompt.event, stepPrompt.input)
			if u, _ := (Reporter{}).LastToolUse(wt); u.Failure != "" || !u.Stopped.IsZero() || u.Limit.Reached() {
				t.Errorf("prompted since: %+v, want no turn's end", u)
			}
		})
	}
}
