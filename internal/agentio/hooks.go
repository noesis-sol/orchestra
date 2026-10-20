package agentio

import (
	"fmt"
	"time"
)

// ToolUse is what a worker last reported through its hooks.
type ToolUse struct {
	// PreToolUse (using Tool now), PermissionRequest (asking to use Tool: waiting on the answer
	// while the terminal shows it at rest, using it once allowed), PostToolUse (between tools), Stop
	// (turn over) or StopFailure (turn over on an API error, which runs no Stop: see Limit)
	Event   string
	Tool    string    // Bash, Edit, Read, …
	Command string    // Bash's command
	At      time.Time // when it was reported; zero if not known

	// When the worker's last turn ended, at its Stop hook or its StopFailure for an API error other
	// than the usage limit, if no prompt has started another since; zero otherwise. A record of a tool
	// use written after it comes from no turn of the worker's.
	Stopped time.Time

	// When Claude Code noted the worker idle at its prompt for a while (its idle_prompt notification)
	// after this report; zero otherwise. A permission prompt reported has been answered by then: refused,
	// which runs no hook, as allowing it would have the tool's end replace this report.
	Idle time.Time

	// The usage limit the worker's last turn ended on, if it ended on one and no prompt has started
	// another since; zero otherwise.
	Limit UsageLimit

	// Whether the worker's last turn, which its Stop hook ended (see Stopped), is still ending: Claude
	// Code has yet to log the turn's end, and with it whether the turn ended on the usage limit, which it
	// does a moment after the turn's Stop hooks have run, the user's own too. The turn hasn't ended yet.
	Ending bool

	// The kind of API error that ended the turn (StopFailure's error, as Claude Code names it:
	// server_error, rate_limit, …): of this report, a StopFailure, or of the turn's end a later report
	// replaced (see Stopped); empty for none, or when Claude Code named none.
	Failure string

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

// UsageLimit is the account's usage limit (its five-hour session limit, its weekly one, …) a worker's
// turn ended on: Claude Code refuses its requests until the limit resets. The zero UsageLimit is none.
type UsageLimit struct {
	At    time.Time // when the turn ended on it; zero: it didn't
	Reset time.Time // when the limit resets; zero when Claude Code didn't say, or said it in words not read
	// What Claude Code said, as "You've hit your session limit · resets 7pm (Europe/Athens)"
	Message string
}

// Reached reports whether the turn ended on the usage limit.
func (l UsageLimit) Reached() bool { return !l.At.IsZero() }

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

// SubagentQuiet is how long a subagent started and not stopped may go without any hook reporting
// before it counts as ended. Claude Code runs no SubagentStop for a subagent cut off by Esc, nor a
// Stop for the turn, and a crash may end one the same way: without this, the worker would read
// working for the rest of its session. A subagent at work reports each tool use; one running a
// single command longer than this is taken as ended until its next report, as a background one
// left idle that long already settles its worker: it is the run loop's idle grace.
const SubagentQuiet = 10 * time.Minute

// SubagentsRunning reports whether a subagent the record counts is still running at now, the
// worker's hooks having last reported a tool use (of any of its agents) at used: until no hook has
// reported anything for SubagentQuiet.
func (r HookRecord) SubagentsRunning(used, now time.Time) bool {
	last := r.Noted
	if used.After(last) {
		last = used
	}
	return r.Subagents > 0 && now.Sub(last) < SubagentQuiet
}

// Reporter has a worker report each tool it uses (Claude Code hooks), so the dashboard can say
// what it is doing without reading its screen.
type Reporter interface {
	ReportArgs(worktree string) ([]string, error) // agent arguments that turn reporting on
	ResumeArgs(worktree string) ([]string, error) // the same for a resumed worker, keeping the files edited before
	LastToolUse(worktree string) (ToolUse, bool)  // false when the worker reported nothing
	HookRecord(worktree string) HookRecord        // its permission prompts, compactions and subagents
	EditedFiles(worktree string) []string         // repository files the worker has edited so far
	Session(worktree string) (Session, bool)      // the worker's session, false when there is none to resume
	TranscriptTail(worktree string) string        // the end of its session's transcript, as text; "" if unreadable
	// ReportArgs for a worker with no worktree of its own, its records in the folder of that name in
	// dir's .orchestra/run/ (project.ProbeRun, project.InterviewRun)
	ReportArgsIn(dir, folder string) ([]string, error)
}

// EnvReporter is a Reporter whose hooks' settings can also give the worker environment variables,
// which its commands run with (Claude Code's settings env).
type EnvReporter interface {
	// WithEnv is the Reporter whose ReportArgs and ResumeArgs give the worker env too
	WithEnv(env map[string]string) Reporter
}

// Session is a Claude Code worker's session, as its hooks recorded it: resumed (claude --resume),
// the worker has all it knew of its ticket.
type Session struct {
	ID         string // the session's ID, a UUID
	Transcript string // the path of its transcript
}
