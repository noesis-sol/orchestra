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
	// while the terminal shows it at rest, using it once allowed), PostToolUse (between tools) or Stop
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
	Permissions []Permission // each permission prompt, oldest first
	Compactions []string     // what triggered each compaction (auto, or manual: /compact), oldest first
	Subagents   int          // subagents started and not stopped
	// When a subagent started and stopped, if its stop is the hooks' last note (its answer starts
	// the worker's next turn); zero otherwise
	SubagentStopped time.Time
	Noted           time.Time // when the hooks last noted one of these, or a session's start or end; zero: never
}

// Permission is a permission prompt a worker's hooks noted.
type Permission struct {
	Tool    string // the tool it asked to use: Bash, Edit, …
	Command string // the start of Bash's command; "" for another tool, or a note that has none
	// The removal in Command that Claude Code's built-in safety check holds for a person (see
	// GuardedRemoval); "" when the prompt is an ordinary one, which an allow rule would spare
	Removal string
}

// String names the prompt for the evidence: its tool, and its command when there is one.
func (p Permission) String() string {
	switch {
	case p.Removal != "":
		return p.Tool + " (" + p.Removal + ")"
	case p.Command != "":
		return p.Tool + " (" + p.Command + ")"
	}
	return p.Tool
}

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

// SubagentQuiet is how long a subagent started and not stopped may go without any hook reporting
// before it counts as ended. Claude Code runs no SubagentStop for a subagent cut off by Esc, nor a
// Stop for the turn, and a crash may end one the same way: without this, the worker would read
// working for the rest of its session. A subagent at work reports each tool use; one running a
// single command longer than this is taken as ended until its next report, as a background one
// left idle that long already settles its worker (quietFor).
const SubagentQuiet = idleGrace

// SubagentsRunning reports whether a subagent the record counts is still running at now, the
// worker's hooks having last reported a tool use (of any of its agents) at used: until no hook has
// reported anything for SubagentQuiet.
func (r HookRecord) SubagentsRunning(used, now time.Time) bool {
	return r.Subagents > 0 && now.Sub(later(r.Noted, used)) < SubagentQuiet
}

// Evidence says what the record shows of a worker's session that its transcript and screen may not:
// permission prompts and compactions, one line each; "" when there were none.
func (r HookRecord) Evidence() string {
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
	if u.Event != "PreToolUse" && u.Event != EventPermission {
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
// u (ok: they did), is waiting on a permission prompt: asked, and the terminal shows it at rest
// (idle, blocked or done). A worker the terminal shows working was allowed, and is using the tool.
func waitsOnPermission(st AgentState, u ToolUse, ok bool) bool {
	return ok && u.Event == EventPermission && (st == StateIdle || st == StateBlocked || st == StateDone)
}
