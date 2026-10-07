package dispatch

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ToolUse is what a worker last reported through its hooks.
type ToolUse struct {
	// PreToolUse (using Tool now), PermissionRequest (asking to use Tool: waiting on the answer
	// while Herdr shows it at rest, using it once allowed), PostToolUse (between tools) or Stop
	// (turn over)
	Event   string
	Tool    string    // Bash, Edit, Read, …
	Command string    // Bash's command
	At      time.Time // when it was reported; zero if not known

	// When the worker's last turn ended, at its Stop hook, if no prompt has started another since;
	// zero otherwise. A record of a tool use written after it comes from no turn of the worker's.
	Stopped time.Time

	// Who reported it, as the hook's input says (set for a tool use or a permission prompt):
	// the Claude Code session, its transcript, and the subagent (ID and type) whose tool it is,
	// empty for the session's main agent.
	Session, Transcript, Agent, AgentType string
}

// Reporter says who reported u: the tool, the session and the agent, for the log. A record that
// replaces a worker's Stop without a turn of its own comes from somewhere it should be traced to.
func (u ToolUse) Reporter() string {
	or := func(s, none string) string {
		if s == "" {
			return none
		}
		return s
	}
	agent := "main agent"
	if u.Agent != "" || u.AgentType != "" {
		agent = "agent " + or(u.Agent, "with no ID") + " (" + or(u.AgentType, "no type") + ")"
	}
	at := "at an unknown time"
	if !u.At.IsZero() {
		at = "at " + u.At.Format("15:04:05")
	}
	return fmt.Sprintf("written %s by tool %s, session %s, %s, transcript %s",
		at, or(u.Tool, "unnamed"), or(u.Session, "unnamed"), agent, or(u.Transcript, "unnamed"))
}

// EventPermission is the ToolUse event of a permission prompt.
const EventPermission = "PermissionRequest"

// HookRecord is what a worker's hooks noted besides its last tool use, since it was started (or
// resumed): the permission prompts it waited on, its context's compactions and its subagents.
type HookRecord struct {
	Permissions []string // the tool each permission prompt asked about, oldest first
	Compactions []string // what triggered each compaction (auto, or manual: /compact), oldest first
	Subagents   int      // subagents started and not stopped
}

// Evidence says what the record shows of a worker's session that its transcript and screen may not:
// permission prompts and compactions, one line each; "" when there were none.
func (r HookRecord) Evidence() string {
	var lines []string
	if n := len(r.Permissions); n > 0 {
		lines = append(lines, fmt.Sprintf("It waited on a permission prompt %s, Claude Code asking to allow: %s.",
			times(n), strings.Join(r.Permissions, ", ")))
	}
	if n := len(r.Compactions); n > 0 {
		lines = append(lines, fmt.Sprintf("Its context was compacted %s (%s): the summary that replaced its "+
			"earlier messages may have lost some of what it was told or found.",
			times(n), strings.Join(r.Compactions, ", ")))
	}
	return strings.Join(lines, "\n")
}

// testRunner matches commands that run a test suite, for projects whose check command is not
// the one being run.
var testRunner = regexp.MustCompile(`(^|[\s;&|(])(` +
	`go test|swift test|cargo test|py\.?test|jest|vitest|rspec|phpunit|mix test|dotnet test|ctest|tox|` +
	`(npm|pnpm|yarn|bun)( run)? test|make (test|check)|gradlew? test|mvnw? test|xcodebuild\b.*\btest` +
	`)\b`)

// Doing names what a working worker is doing from what it reported: testing (the project's check
// command or a test runner), editing, reading or running a subagent. A tool it asked permission
// for is running once Herdr shows it working. "" means nothing more precise than Herdr's
// "working": thinking between tools, another command, or no report.
func Doing(u ToolUse, check string) string {
	if u.Event != "PreToolUse" && u.Event != EventPermission {
		return ""
	}
	switch u.Tool {
	case "Agent", "Task": // the subagent's own tool uses replace this report as they come
		return DoingSubagent
	case "Bash":
		cmd := strings.TrimSpace(u.Command)
		if check != "" && strings.Contains(cmd, check) || testRunner.MatchString(cmd) {
			return "testing"
		}
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return "editing"
	case "Read", "Grep", "Glob", "LS", "WebFetch", "WebSearch":
		return "reading"
	}
	return ""
}

// DoingSubagent is Doing for a worker running a subagent: its Agent tool, or the hooks' record of
// a subagent started and not stopped while it thinks between the subagent's tools.
const DoingSubagent = "subagent"

// waitsOnPermission says whether a worker Herdr shows in state st, whose hooks last reported u
// (ok: they did), is waiting on a permission prompt: asked, and Herdr shows it at rest (idle,
// blocked or done). A worker Herdr shows working was allowed, and is using the tool.
func waitsOnPermission(st AgentState, u ToolUse, ok bool) bool {
	return ok && u.Event == EventPermission && (st == StateIdle || st == StateBlocked || st == StateDone)
}
