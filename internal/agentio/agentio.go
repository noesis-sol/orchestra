// Package agentio is the vocabulary a worker's agent is read in, shared by the run loop
// (internal/dispatch) and the adapters that read it (internal/herdr, internal/tmux, internal/cmux,
// internal/hookterm, internal/claude): its state as the terminal reports it, what every terminal keeps
// to in giving it, and the records its hooks write. It imports none of them, so the adapters depend on
// it rather than on the run loop.
package agentio

import (
	"errors"
	"strings"
	"time"
)

// State is a worker's status as the terminal reports it, or StateGone when it has no such agent.
type State string

// The states an agent can be in.
const (
	StateIdle    State = "idle"
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateDone    State = "done"
	// The agent's turn ended on its account's usage limit, and it waits for the limit to reset or for a
	// prompt (ToolUse.Limit says when it resets, if known).
	StateLimited State = "limited"
	StateUnknown State = "unknown" // the terminal can't tell what the agent is doing
	StateGone    State = "gone"    // the terminal has no such agent
)

// ErrNameRefused is the terminal refusing an agent name (errors.Is): it would refuse it on every
// try. Only Herdr has a rule for names; tmux and cmux take any.
var ErrNameRefused = errors.New("the terminal refuses the agent name")

// ErrArgumentRefused is the terminal refusing the agent's arguments (errors.Is), as Herdr does those
// it can't type into a shell: it would refuse them on every try. tmux and cmux pass any.
var ErrArgumentRefused = errors.New("the terminal refuses the agent's arguments")

// How long every terminal waits for an agent: to be ready for a prompt (dispatch.Starter's StartAgent
// and WaitReady), to start on a prompt it submits (dispatch.Agents's Prompt), and to start working on
// one it was given at launch or by a key (dispatch.Agents's WaitStarted).
const (
	ReadyWithin   = time.Minute
	PromptWithin  = 30 * time.Second
	StartedWithin = 20 * time.Second
)

// ScreenLines is how many lines of a worker's terminal dispatch.Agents's Screen gives, at most, in
// every terminal.
const ScreenLines = 80

// LastLines is the last n lines of s, a terminal's screen, with the empty lines below its last one
// left out, ending in "\n"; "" when it has none.
func LastLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n") + "\n"
}
