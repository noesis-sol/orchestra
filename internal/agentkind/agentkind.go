// Package agentkind describes each kind of agent orchestra can run its workers as, once, in a table:
// what a worker of the kind is given and told. The code that starts workers, reads them and sets a
// project up for them reads the kind's row (Of) rather than comparing its name.
package agentkind

import (
	"slices"

	"github.com/noesis-sol/orchestra/internal/claudescreen"
)

// Kind is one kind of agent, as a row of kinds describes it.
type Kind struct {
	// Name is the kind as --agent, AGENT_KIND and the terminals name it.
	Name string
	// SkillsDir is the folder, from the repository's top, where the kind finds a project's skills; ""
	// for a kind orchestra knows no folder for.
	SkillsDir string
	// ClaudeArgs says it takes Claude Code's arguments: the worker arguments (worker_args), the
	// permission mode, the tools workers never use, as read from its --help, and the probe's
	// --strict-mcp-config.
	ClaudeArgs bool
	// MCP says it is passed the MCP servers the project chose, which orchestra's own Claude Code
	// sessions then get too; for another kind orchestra has only their names.
	MCP bool
	// Rules says its standing rules go in its system prompt (--append-system-prompt-file), so that its
	// first message need only name its ticket.
	Rules bool
	// LaunchPrompt says it can start with its prompt already submitted, from a file it reads.
	LaunchPrompt bool
	// Hooks says it reports through Claude Code's hooks what it does (the tools it uses, its
	// permission prompts and compactions, for the run report), in an environment they give it, which
	// carries its share of the machine's cores.
	Hooks bool
	// CheckTimeNote says it is told how to give a check its time: its Bash tool's timeout and Monitor.
	CheckTimeNote bool
	// LimitNote says it is told what to do at the account's usage limit, which its turn may run past.
	LimitNote bool
	// Subagents says init can write subagents for it, such as the verifier.
	Subagents bool
	// Unset names the variables a launch of the kind leaves out of the environment it inherits (see
	// Env): a launcher's session markers, which a worker started from a Claude Code session, through a
	// tmux server its Bash tool started say, would otherwise take for its own.
	Unset []string
	// Screen reads its screen, as a terminal gives it (see ScreenReader); nil for a kind whose screen
	// orchestra can't read, which gets no reading of it (Reader).
	Screen ScreenReader
}

// ScreenReader reads what an agent kind's screen shows, which only the kind knows: its glyphs, its
// input box, its dialogs. A screen is the end of a worker's terminal, as dispatch.Agents.Screen gives it.
type ScreenReader interface {
	// InputHolds reports whether the input box still holds prompt, unsent.
	InputHolds(screen, prompt string) bool
	// LastActivity is the worker's most recent action or spinner line; "" when the screen shows none.
	LastActivity(screen string) string
	// ShowsUsageLimit reports whether the screen shows its last turn ended on the account's usage limit,
	// which Herdr, having no state for it, reads there.
	ShowsUsageLimit(screen string) bool
}

// Reader is the kind's Screen, or for a kind without one a reader that finds nothing on a screen.
func (k Kind) Reader() ScreenReader {
	if k.Screen == nil {
		return noScreen{}
	}
	return k.Screen
}

// Env is what a launch of the kind changes in the environment it inherits, in the form of
// internal/command's env entries: a name alone leaves that variable out (Unset), NAME=value sets it.
// Every kind runs with path, orchestra's PATH, as orchestra would run it, whatever the PATH of the
// terminal or shell it is launched in.
func (k Kind) Env(path string) []string {
	return append(slices.Clone(k.Unset), "PATH="+path)
}

// claudeSession are the variables Claude Code sets for the commands it runs, which mark them as run
// from a session of its own: a Claude Code that inherits CLAUDECODE runs as that session's child (it
// keeps no prompt history, for one) rather than as a worker of its own.
var claudeSession = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION"}

// noScreen is the ScreenReader of a kind whose screen orchestra can't read.
type noScreen struct{}

func (noScreen) InputHolds(string, string) bool { return false }
func (noScreen) LastActivity(string) string     { return "" }
func (noScreen) ShowsUsageLimit(string) bool    { return false }

// Claude is Claude Code, the default kind.
var Claude = Kind{
	Name:          "claude",
	SkillsDir:     ".claude/skills",
	ClaudeArgs:    true,
	MCP:           true,
	Rules:         true,
	LaunchPrompt:  true,
	Hooks:         true,
	CheckTimeNote: true,
	LimitNote:     true,
	Subagents:     true,
	Unset:         claudeSession,
	Screen:        claudescreen.Reader{},
}

// Codex is OpenAI's Codex CLI, of which orchestra knows only where it finds a project's skills, and
// that a launcher's Claude Code session is none of its own.
var Codex = Kind{Name: "codex", SkillsDir: ".agents/skills", Unset: claudeSession}

// kinds are the agent kinds orchestra knows: adding one, or giving one a feature, is its row here.
var kinds = []Kind{Claude, Codex}

// Default is the kind of a run or a project that names none (--agent's default).
var Default = Claude

// Of is the kind named name: Default for "", and for a kind orchestra doesn't know, one of that name
// that is given and told nothing of the others'.
func Of(name string) Kind {
	if name == "" {
		return Default
	}
	for _, k := range kinds {
		if k.Name == name {
			return k
		}
	}
	return Kind{Name: name}
}

// Named is the kind of an agent as a terminal names it, its kind or the one it was launched as: Of's,
// but for "", which there is no kind, given nothing. A run passes the terminal its kind's Name, never "".
func Named(name string) Kind {
	if name == "" {
		return Kind{}
	}
	return Of(name)
}
