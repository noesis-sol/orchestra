package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// hookStep is one hook Claude Code runs, with its input, or (resume) orchestra starting a resumed
// worker with ResumeArgs.
type hookStep struct {
	event, input string
	resume       bool
}

func stepAt(event, input string) hookStep { return hookStep{event: event, input: input} }

var (
	stepSessionStart = func(source string) hookStep {
		return stepAt("SessionStart", `{"hook_event_name":"SessionStart","session_id":"`+testID+`",`+
			`"transcript_path":"/c/projects/x/`+testID+`.jsonl","source":"`+source+`"}`)
	}
	stepSessionEnd = func(reason string) hookStep {
		return stepAt("SessionEnd", `{"hook_event_name":"SessionEnd","session_id":"`+testID+`","reason":"`+reason+`"}`)
	}
	stepNotification = func(kind string) hookStep {
		return stepAt("Notification", `{"hook_event_name":"Notification","message":"Claude needs your attention",`+
			`"notification_type":"`+kind+`"}`)
	}
	stepPrompt  = stepAt("UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","prompt":"work on the ticket"}`)
	stepUseBash = stepAt("PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./..."}}`)
	stepUsedIt  = stepAt("PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Bash",`+
		`"tool_input":{"command":"rm -rf build"},"tool_response":{"stdout":"ok"}}`)
	stepFailed = stepAt("PostToolUseFailure", `{"hook_event_name":"PostToolUseFailure","tool_name":"Bash",`+
		`"tool_input":{"command":"rm -rf build"},"error":"exit 1"}`)
	stepAskBash = stepAt("PermissionRequest", `{"hook_event_name":"PermissionRequest","tool_name":"Bash",`+
		`"tool_input":{"command":"rm -rf build"}}`)
	stepStop     = stepAt("Stop", `{"hook_event_name":"Stop","stop_hook_active":false}`)
	stepSubStart = stepAt("SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"verifier"}`)
	stepSubStop  = stepAt("SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"verifier"}`)
	stepSubBash  = stepAt("PreToolUse", `{"hook_event_name":"PreToolUse","agent_id":"a1","agent_type":"verifier",`+
		`"tool_name":"Bash","tool_input":{"command":"go vet ./..."}}`)
	stepSubAsk = stepAt("PermissionRequest", `{"hook_event_name":"PermissionRequest","agent_id":"a1","tool_name":"Bash",`+
		`"tool_input":{"command":"rm -rf build"}}`)
	stepForkedRead = stepAt("PreToolUse", `{"hook_event_name":"PreToolUse","agent_id":"f1","tool_name":"Read",`+
		`"tool_input":{"file_path":"x"}}`)
	stepCompact = func(trigger string) hookStep {
		return stepAt("PreCompact", `{"hook_event_name":"PreCompact","trigger":"`+trigger+`"}`)
	}
	stepResumed = hookStep{resume: true}
)

// State tells what a Claude session does from what its hooks recorded, for each sequence of hooks
// Claude Code runs.
func TestStateFromHookRecords(t *testing.T) {
	t.Parallel()
	turn := []hookStep{stepSessionStart("startup"), stepPrompt, stepUseBash, stepUsedIt}
	then := func(steps ...hookStep) []hookStep { return append(slices.Clone(turn), steps...) }
	for _, c := range []struct {
		name  string
		steps []hookStep
		want  dispatch.AgentState
	}{
		{"launched, no hook yet", nil, dispatch.StateUnknown},
		{"started", []hookStep{stepSessionStart("startup")}, dispatch.StateIdle},
		{"prompted", []hookStep{stepSessionStart("startup"), stepPrompt}, dispatch.StateWorking},
		{"using a tool", []hookStep{stepSessionStart("startup"), stepPrompt, stepUseBash}, dispatch.StateWorking},
		{"between tools", turn, dispatch.StateWorking},
		{"after a tool that failed", then(stepUseBash, stepFailed), dispatch.StateWorking},
		{"permission asked", then(stepUseBash, stepAskBash), dispatch.StateBlocked},
		{"permission asked, notified", then(stepUseBash, stepAskBash, stepNotification("permission_prompt")), dispatch.StateBlocked},
		{"permission asked then allowed", then(stepUseBash, stepAskBash, stepUsedIt), dispatch.StateWorking},
		{"permission asked then denied", then(stepUseBash, stepAskBash, stepNotification("idle_prompt")), dispatch.StateIdle},
		{"permission denied, then told what to do", then(stepUseBash, stepAskBash, stepPrompt), dispatch.StateWorking},
		{"permission asked after the Stop", then(stepStop, stepAskBash), dispatch.StateBlocked},
		{"Stop", then(stepStop), dispatch.StateIdle},
		{"Stop, then idle at the prompt", then(stepStop, stepNotification("idle_prompt")), dispatch.StateIdle},
		{"Stop, then a forked agent refused its tool", then(stepStop, stepForkedRead), dispatch.StateIdle},
		{"Stop, then a tool finished", then(stepStop, stepUseBash, stepUsedIt), dispatch.StateWorking},
		{"Stop, then a new prompt", then(stepStop, stepPrompt), dispatch.StateWorking},
		{"Stop with a subagent running", then(stepSubStart, stepSubBash, stepStop), dispatch.StateWorking},
		{"Stop, then the subagent's stop", then(stepSubStart, stepSubBash, stepStop, stepSubStop), dispatch.StateIdle},
		{"idle notification with a subagent running", then(stepSubStart, stepStop, stepNotification("idle_prompt")),
			dispatch.StateWorking},
		{"subagent asked permission", then(stepSubStart, stepSubBash, stepSubAsk), dispatch.StateBlocked},
		{"interrupted turn", then(stepUseBash, stepNotification("idle_prompt")), dispatch.StateIdle},
		{"asking for input", then(stepNotification("elicitation_dialog")), dispatch.StateBlocked},
		{"signed in", then(stepStop, stepNotification("auth_success")), dispatch.StateIdle},
		{"compacting mid-turn", then(stepCompact("auto"), stepSessionStart("compact")), dispatch.StateWorking},
		{"compacted mid-turn, then Stop", then(stepCompact("auto"), stepSessionStart("compact"), stepStop), dispatch.StateIdle},
		{"compacted at rest", then(stepStop, stepCompact("manual"), stepSessionStart("compact")), dispatch.StateIdle},
		{"resuming, no hook yet", then(stepStop, stepResumed), dispatch.StateUnknown},
		{"resumed", then(stepStop, stepResumed, stepSessionStart("resume")), dispatch.StateIdle},
		{"resumed and prompted", then(stepResumed, stepSessionStart("resume"), stepPrompt), dispatch.StateWorking},
		{"exited", then(stepStop, stepSessionEnd("prompt_input_exit")), dispatch.StateGone},
		{"exited mid-turn", then(stepSessionEnd("other")), dispatch.StateGone},
		{"logged out", then(stepStop, stepSessionEnd("logout")), dispatch.StateGone},
		{"cleared, no new session yet", then(stepStop, stepSessionEnd("clear")), dispatch.StateIdle},
		{"cleared, then a new session", then(stepStop, stepSessionEnd("clear"), stepSessionStart("clear")), dispatch.StateIdle},
		{"cleared, then prompted", then(stepStop, stepSessionEnd("clear"), stepSessionStart("clear"), stepPrompt), dispatch.StateWorking},
		{"exited, then resumed with its records", then(stepStop, stepSessionEnd("prompt_input_exit"), stepSessionStart("resume")),
			dispatch.StateIdle},
		{"exited, resuming afresh", then(stepStop, stepSessionEnd("prompt_input_exit"), stepResumed), dispatch.StateUnknown},
		{"exited, resumed afresh", then(stepStop, stepSessionEnd("prompt_input_exit"), stepResumed, stepSessionStart("resume")),
			dispatch.StateIdle},
		{"resumed, then exited", then(stepStop, stepResumed, stepSessionStart("resume"), stepSessionEnd("prompt_input_exit")),
			dispatch.StateGone},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			if got := playHooks(t, wt, c.steps); got != c.want {
				t.Errorf("state after %s: %s, want %s", c.name, got, c.want)
			}
		})
	}
}

// playHooks runs steps in turn with the hooks ReportArgs installs in wt, and returns the state its
// records then give. Each step's records are dated a second after the step before's: a file's time
// can be as coarse as the kernel's clock tick, finer than two hooks run one after the other. The
// dates are a minute before now, which no hook writes at, and recent: a subagent quiet for
// dispatch.SubagentQuiet counts as ended.
func playHooks(t *testing.T, wt string, steps []hookStep) dispatch.AgentState {
	t.Helper()
	settings := reportingSettings(t, wt, false)
	records := Records(wt, "")
	clock := time.Now().Add(-time.Minute).Truncate(time.Second)
	dated := map[string]time.Time{}
	for _, s := range steps {
		if s.resume {
			settings = reportingSettings(t, wt, true)
		} else {
			runHooks(t, settings, s.event, s.input)
		}
		clock = clock.Add(time.Second)
		entries, _ := os.ReadDir(records) // none yet: nothing written
		for _, e := range entries {
			name := e.Name()
			path := filepath.Join(records, name)
			fi, err := os.Stat(path)
			if err != nil || name == settingsName || fi.ModTime().Equal(dated[name]) {
				continue // not there, not a record, or not written by this step
			}
			if err := os.Chtimes(path, clock, clock); err != nil {
				t.Fatal(err)
			}
			dated[name] = clock
		}
	}
	return State(records)
}

// A session with no worktree of its own has its hooks write to its own folder in the main checkout's
// .orchestra/run/, apart from a ticket's records and each other's, and its state is read there.
func TestReportArgsInKeepsASessionsRecordsApart(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	ticket := reportingSettings(t, repo, false)
	runHooks(t, ticket, "UserPromptSubmit", stepPrompt.input)

	for _, folder := range []string{project.ProbeRun, project.InterviewRun} {
		args, err := Reporter{}.ReportArgsIn(repo, folder)
		if err != nil {
			t.Fatal(err)
		}
		records, ok := RecordsOf(append([]string{"--strict-mcp-config"}, args...))
		if want := filepath.Join(repo, ".orchestra", "run", folder); !ok || records != want || Records(repo, folder) != want {
			t.Fatalf("%s: records in %q (%v), Records %q, want %q", folder, records, ok, Records(repo, folder), want)
		}
		if got := State(records); got != dispatch.StateUnknown {
			t.Errorf("%s before any hook: %s, want unknown", folder, got)
		}
		settings := readSettings(t, args[1])
		runHooks(t, settings, "SessionStart", stepSessionStart("startup").input)
		if got := State(records); got != dispatch.StateIdle {
			t.Errorf("%s started: %s, want idle", folder, got)
		}
		runHooks(t, settings, "SessionEnd", stepSessionEnd("prompt_input_exit").input)
		if got := State(records); got != dispatch.StateGone {
			t.Errorf("%s exited: %s, want gone", folder, got)
		}
		// Started again, it starts afresh.
		if _, err := (Reporter{}).ReportArgsIn(repo, folder); err != nil {
			t.Fatal(err)
		}
		if got := State(records); got != dispatch.StateUnknown {
			t.Errorf("%s started again: %s, want unknown", folder, got)
		}
	}
	if got := State(Records(repo, "")); got != dispatch.StateWorking {
		t.Errorf("the ticket's worker: %s, want working, as its own records say", got)
	}
}

// A file or a symlink in place of a session's folder, wherever it points, is replaced by the folder,
// so the records are never written through a link.
func TestReportArgsInReplacesWhatIsInPlaceOfItsFolder(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	inside, outside := filepath.Join(repo, "src"), t.TempDir()
	run := filepath.Join(repo, ".orchestra", "run")
	for _, dir := range []string{inside, run} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{project.ProbeRun: inside, "relative": filepath.Join("..", "..", "src"), "out": outside}
	for folder, to := range links {
		if err := os.Symlink(to, filepath.Join(run, folder)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(run, project.InterviewRun), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{project.ProbeRun, project.InterviewRun, "relative", "out"} {
		if _, err := (Reporter{}).ReportArgsIn(repo, folder); err != nil {
			t.Fatalf("%s: %v", folder, err)
		}
		if fi, err := os.Lstat(filepath.Join(run, folder)); err != nil || !fi.IsDir() {
			t.Errorf("%s: %v %v, want a folder", folder, fi, err)
		}
	}
	for _, dir := range []string{inside, outside} {
		if entries, _ := os.ReadDir(dir); len(entries) > 0 {
			t.Errorf("written through a symlink to %s: %v", dir, entries)
		}
	}
}

// RecordsOf finds the records' folder in a session's arguments, wherever --settings is among its
// options, and only there.
func TestRecordsOf(t *testing.T) {
	t.Parallel()
	settings := filepath.Join(string(filepath.Separator), "repo", ".orchestra", "run", settingsName)
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"--settings", settings}, filepath.Dir(settings)},
		{[]string{"--strict-mcp-config", "--settings=" + settings, "--effort", "high"}, filepath.Dir(settings)},
		{[]string{"--settings", "relative/" + settingsName}, ""},
		{[]string{"--settings", filepath.Join(filepath.Dir(settings), "other.json")}, ""},
		{[]string{"--", "--settings", settings}, ""},
		{[]string{"--settings"}, ""},
		{nil, ""},
	} {
		got, ok := RecordsOf(c.args)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("RecordsOf(%q) = %q %v, want %q", c.args, got, ok, c.want)
		}
	}
}

// The hooks a session's state is read from are installed with the others.
func TestHooksForStateAreInstalled(t *testing.T) {
	t.Parallel()
	hooks := reportingSettings(t, t.TempDir(), false)["hooks"].(map[string]any)
	for _, event := range []string{"SessionStart", "SessionEnd", "Notification", "UserPromptSubmit", "PreToolUse",
		"PostToolUse", "PermissionRequest", "Stop", "SubagentStart", "SubagentStop", "PreCompact"} {
		if _, ok := hooks[event]; !ok {
			t.Errorf("no %s hook", event)
		}
	}
}

// readSettings reads the hook settings ReportArgs and the like wrote to path.
func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	var settings map[string]any
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	return settings
}
