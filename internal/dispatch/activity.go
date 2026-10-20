package dispatch

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// What a worker's hooks report is internal/agent's, the loop's names for it aliases.
type (
	// ToolUse is what a worker last reported through its hooks (agentio.ToolUse).
	ToolUse = agentio.ToolUse
	// UsageLimit is the account's usage limit a worker's turn ended on (agentio.UsageLimit).
	UsageLimit = agentio.UsageLimit
	// HookRecord is what a worker's hooks noted besides its last tool use (agentio.HookRecord).
	HookRecord = agentio.HookRecord
	// Permission is a permission prompt a worker's hooks noted (agentio.Permission).
	Permission = agentio.Permission
)

// permissionsSaid says what the prompts were: the tools Claude Code asked to allow, an allow rule's
// for each sparing its wait, apart from the removals its safety check held, which no allow rule spares.
func permissionsSaid(ps []Permission) string {
	var asked, held []string
	for _, p := range ps {
		if p.Removal != "" {
			held = append(held, p.String())
		} else {
			asked = append(asked, p.String())
		}
	}
	var said []string
	if len(asked) > 0 {
		said = append(said, "Claude Code asking to allow: "+strings.Join(asked, ", "))
	}
	if n := len(held); n > 0 {
		which := "a removal"
		switch {
		case len(asked) > 0:
			which = fmt.Sprintf("%d of them a removal", n)
		case n > 1:
			which = "each a removal"
		}
		said = append(said, fmt.Sprintf("%s held by Claude Code's built-in safety check, its target a variable "+
			"or a glob (%s), which only a person can approve and no allow rule skips: the worker prompt should "+
			"keep workers from writing such removals", which, strings.Join(held, ", ")))
	}
	return strings.Join(said, "; ")
}

// recordEvidence says what r, a worker's hook record, shows of its session that its transcript and
// screen may not: permission prompts and compactions, one line each; "" when there were none.
func recordEvidence(r HookRecord) string {
	var lines []string
	if n := len(r.Permissions); n > 0 {
		lines = append(lines, fmt.Sprintf("It waited on a permission prompt %s, %s.", times(n),
			permissionsSaid(r.Permissions)))
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

// Doing is what a working worker is doing, from what it reported: more precise than the terminal's
// "working". "" means nothing more precise.
type Doing string

// What a working worker can be doing.
const (
	DoingTesting  Doing = "testing" // running the project's check command or a test runner
	DoingEditing  Doing = "editing"
	DoingReading  Doing = "reading"
	DoingSubagent Doing = "subagent" // its Agent tool, or a subagent started and not stopped
)

// DoingOf names what a working worker is doing from what it reported: testing (the project's check
// command or a test runner), editing, reading or running a subagent. A tool it asked permission
// for is running once the terminal shows it working. "" means nothing more precise than the
// terminal's "working": thinking between tools, another command, or no report.
func DoingOf(u ToolUse, check string) Doing {
	if u.Event != "PreToolUse" && u.Event != agentio.EventPermission {
		return ""
	}
	switch u.Tool {
	case "Agent", "Task": // the subagent's own tool uses replace this report as they come
		return DoingSubagent
	case "Bash":
		cmd := strings.TrimSpace(u.Command)
		if check != "" && strings.Contains(cmd, check) || testRunner.MatchString(cmd) {
			return DoingTesting
		}
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		return DoingEditing
	case "Read", "Grep", "Glob", "LS", "WebFetch", "WebSearch":
		return DoingReading
	}
	return ""
}

// waitsOnPermission says whether a worker the terminal shows in state st, whose hooks last reported
// u (ok: they did), is waiting on a permission prompt: asked, not refused since (u.Idle), and the
// terminal shows it at rest (idle, blocked or done). A worker the terminal shows working was allowed,
// and is using the tool.
func waitsOnPermission(st AgentState, u ToolUse, ok bool) bool {
	return ok && u.Event == agentio.EventPermission && u.Idle.IsZero() &&
		(st == StateIdle || st == StateBlocked || st == StateDone)
}
