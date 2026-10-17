package claude

import (
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// stopSummaryAt is Claude Code's note that a turn's Stop hooks have run, logged at at.
func stopSummaryAt(at time.Time) string {
	return `{"type":"system","subtype":"stop_hook_summary","level":"suggestion","timestamp":"` +
		at.UTC().Format(time.RFC3339Nano) + `"}`
}

// The transcript logs a turn's end with its turn_duration entry after the turn's last message, or with its
// stop_hook_summary once a moment has passed since, for the notice of the usage limit that may follow it.
func TestTranscriptLogsTheTurnsEnd(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 17, 4, 25, 7, 0, time.Local)
	for _, c := range []struct {
		name  string
		lines []string
		want  bool
	}{
		{"its last message alone", []string{graceNote, graceWrapUp}, false},
		{"its Stop hooks just run", []string{graceNote, graceWrapUp, stopSummaryAt(now.Add(-10 * time.Millisecond))},
			false},
		{"its Stop hooks run a while ago", []string{graceNote, graceWrapUp, stopSummaryAt(now.Add(-summaryLag))}, true},
		{"its Stop hooks run at a time not given", []string{graceNote, graceWrapUp,
			`{"type":"system","subtype":"stop_hook_summary"}`}, true},
		{"its duration", []string{graceNote, graceWrapUp, stopSummaryAt(now), graceArmed, graceDuration}, true},
		{"its duration with no Stop hooks", []string{graceWrapUp, graceArmed, graceDuration}, true},
		{"a message after an earlier turn's end", []string{graceWrapUp, graceStopSummary, graceDuration, graceContinue,
			graceWrapUp}, false},
		{"no message", []string{graceNote, informational("Usage limit has reset · press enter to continue")}, true},
		{"no transcript", nil, true},
	} {
		if got := endLogged([]byte(strings.Join(c.lines, "\n")), now); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// Claude Code runs a turn's Stop hooks before it logs the turn's end, and the notice that it ended at the
// usage limit with it: until the transcript logs it, the turn goes on, in the state the hooks give a terminal
// and in the last report the loop reads (Ending), for turnEndWait at most; once logged, the notice tells the
// limit.
func TestStopWaitsForTheTurnsEndToBeLogged(t *testing.T) {
	t.Parallel()
	steps := func(p string) []hookStep {
		return []hookStep{stepAt("SessionStart", sessionInput(testID, p)), stepPrompt, stepUseBash, stepUsedIt, stepStop}
	}
	for _, c := range []struct {
		name   string
		lines  []string
		ago    time.Duration // how long before now the Stop hook ran
		state  dispatch.AgentState
		ending bool
	}{
		{"its end not logged", []string{graceNote, graceWrapUp}, 0, dispatch.StateWorking, true},
		{"its end logged", []string{graceWrapUp, graceStopSummary, graceDuration}, 0, dispatch.StateIdle, false},
		{"its end at the limit logged", []string{graceNote, graceWrapUp, graceStopSummary, graceArmed}, 0,
			dispatch.StateLimited, false},
		{"its end never logged", []string{graceNote, graceWrapUp}, turnEndWait, dispatch.StateIdle, false},
	} {
		wt := t.TempDir()
		p := transcriptAt(t, t.TempDir(), testID, c.lines...)
		playHooksAt(t, wt, time.Now().Add(-c.ago), steps(p)...)
		if h := ReadHooks(Records(wt, ""), ""); h.State != c.state {
			t.Errorf("%s: hooks say %s, want %s", c.name, h.State, c.state)
		}
		u, ok := Reporter{}.LastToolUse(wt)
		if !ok || u.Event != "Stop" || u.Ending != c.ending || u.Limit.Reached() != (c.state == dispatch.StateLimited) {
			t.Errorf("%s: last report %+v %v, want the Stop, ending %v", c.name, u, ok, c.ending)
		}
	}
}
