package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// runHooksApart is runHooks for a Claude Code other than the one runHooks plays, the test's own
// process: the hooks run under a shell of their own, which is their $PPID.
func runHooksApart(t *testing.T, settings map[string]any, event, input string) {
	t.Helper()
	for _, e := range settings["hooks"].(map[string]any)[event].([]any) {
		for _, h := range e.(map[string]any)["hooks"].([]any) {
			// The command after the inner shell keeps the outer one from running it in its own place.
			cmd := exec.Command("sh", "-c", `sh -c "$1"; exit $?`, "sh", h.(map[string]any)["command"].(string))
			cmd.Stdin = strings.NewReader(input)
			if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
				t.Fatalf("%s hook: %v %q", event, err, out)
			}
		}
	}
}

// An earlier worker left running in its tab beside the worker started since in the same worktree
// that is cleared, or resumes another session, starts a session of a new ID, which its hooks write to
// the same folder: it is still the earlier Claude Code's, and left out of the records, while the
// later worker's own clear or resume is its own.
func TestEarlierWorkerClearedIsNotRecorded(t *testing.T) {
	t.Parallel()
	const earlier, later = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	const other = "33333333-3333-4333-8333-333333333333"
	start := func(id, source string) string {
		return `{"hook_event_name":"SessionStart","session_id":"` + id + `",` +
			`"transcript_path":"/c/projects/x/` + id + `.jsonl","source":"` + source + `"}`
	}
	end := func(id, reason string) string {
		return `{"hook_event_name":"SessionEnd","session_id":"` + id + `","reason":"` + reason + `"}`
	}
	stop := func(id string) string { return `{"hook_event_name":"Stop","session_id":"` + id + `"}` }
	type step struct {
		later        bool // of the later worker, the test's own process; else the earlier one's
		event, input string
	}
	for _, c := range []struct {
		name    string
		steps   []step
		want    dispatch.AgentState
		session string
	}{
		{"the earlier one is cleared", []step{{false, "SessionEnd", end(earlier, "clear")},
			{false, "SessionStart", start(other, "clear")}, {false, "Stop", stop(other)}}, dispatch.StateWorking, later},
		{"the earlier one resumes another session", []step{{false, "SessionEnd", end(earlier, "resume")},
			{false, "SessionStart", start(other, "resume")}, {false, "SessionEnd", end(other, "prompt_input_exit")}},
			dispatch.StateWorking, later},
		{"the earlier one is cleared and exits", []step{{false, "SessionEnd", end(earlier, "clear")},
			{false, "SessionStart", start(other, "clear")}, {false, "SessionEnd", end(other, "prompt_input_exit")}},
			dispatch.StateWorking, later},
		{"the later one is cleared", []step{{true, "SessionEnd", end(later, "clear")},
			{true, "SessionStart", start(other, "clear")}, {true, "Stop", stop(other)}}, dispatch.StateIdle, other},
		{"the later one resumes another session", []step{{true, "SessionStart", start(other, "resume")},
			{true, "SessionEnd", end(other, "prompt_input_exit")}}, dispatch.StateGone, other},
		{"the later one is cleared after the earlier one", []step{{false, "SessionEnd", end(earlier, "clear")},
			{false, "SessionStart", start(earlier+"0", "clear")}, {true, "SessionEnd", end(later, "clear")},
			{true, "SessionStart", start(other, "clear")}, {true, "Stop", stop(other)}}, dispatch.StateIdle, other},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			records := Records(wt, "")
			settings := reportingSettings(t, wt, false)
			runHooksApart(t, settings, "SessionStart", start(earlier, "startup"))
			runHooksApart(t, settings, "Stop", stop(earlier))
			settings = reportingSettings(t, wt, false) // the later worker, launched in the same worktree
			runHooks(t, settings, "SessionStart", start(later, "startup"))
			runHooks(t, settings, "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","session_id":"`+later+`"}`)
			for _, s := range c.steps {
				run := runHooksApart
				if s.later {
					run = runHooks
				}
				run(t, settings, s.event, s.input)
			}
			if got := State(records); got != c.want {
				t.Errorf("%s: %s, want %s", c.name, got, c.want)
			}
			b, _, err := readRun(wt, sessionName)
			var rec hookInput
			if err != nil || json.Unmarshal(b, &rec) != nil || rec.Session != c.session {
				t.Errorf("%s: the session record is %q (%v), want %s", c.name, b, err, c.session)
			}
			if b, _, err := readRun(wt, processName); err != nil || string(b) != strconv.Itoa(os.Getpid())+"\n" {
				t.Errorf("%s: the process record is %q (%v), want the later one's, %d", c.name, b, err, os.Getpid())
			}
		})
	}
}

// A worker launched by an orchestra before the hooks named their process, its hooks naming none, is
// told from another session by its session record alone, and the hooks that name theirs from it by
// their process once one is recorded.
func TestHooksNamingNoProcess(t *testing.T) {
	t.Parallel()
	const earlier, later = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	cookie := func(string) string { return "1" }
	wt := t.TempDir()
	records := Records(wt, "")
	if _, err := (Reporter{}).ReportArgs(wt); err != nil {
		t.Fatal(err)
	}
	hook := func(args []string, in string) {
		t.Helper()
		if code := Hook(args, cookie, strings.NewReader(in), &strings.Builder{}); code != 0 {
			t.Fatalf("Hook(%q) = %d", args, code)
		}
	}
	hook([]string{"SessionStart", records}, `{"hook_event_name":"SessionStart","session_id":"`+earlier+`","source":"startup"}`)
	if _, _, err := readRun(wt, processName); err == nil {
		t.Error("hooks naming no process recorded one")
	}
	hook([]string{"SessionEnd", records}, `{"hook_event_name":"SessionEnd","session_id":"`+later+
		`","reason":"prompt_input_exit"}`) // another session's
	if got := State(records); got != dispatch.StateIdle {
		t.Errorf("after another session's end: %s, want idle", got)
	}
	hook([]string{"SessionStart", records, "7"},
		`{"hook_event_name":"SessionStart","session_id":"`+later+`","source":"startup"}`)
	hook([]string{"UserPromptSubmit", records, "7"}, `{"hook_event_name":"UserPromptSubmit","session_id":"`+later+`"}`)
	hook([]string{"SessionEnd", records}, `{"hook_event_name":"SessionEnd","session_id":"`+later+
		`","reason":"prompt_input_exit"}`) // hooks of an earlier start, naming the session it now holds
	if got := State(records); got != dispatch.StateWorking {
		t.Errorf("after an end by hooks naming no process: %s, want working", got)
	}
}
