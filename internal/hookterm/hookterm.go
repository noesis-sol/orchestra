// Package hookterm is what orchestra's hook-driven terminals, tmux and cmux, share: terminals that
// can't tell what a Claude Code session is doing, as Herdr reads it from the screen, so that the
// session's hooks tell it, as its kind's AgentUI reads them (UI). An adapter for one writes its own
// primitives (tabs, launching, whether a process is still there, reading the screen, pasting, pressing
// a key, what it keeps of each pane) and calls these for the rest: its errors (Error), what an agent's
// hooks and screen tell (UI), waiting for an agent's state (Agents), the end of a prompt, Herdr's key
// names (ParseKey), free agent names (FreeName) and whether something listens on a socket (Probe).
package hookterm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
	"github.com/noesis-sol/orchestra/internal/command"
)

// Error is a terminal's call that failed, or found no tab, pane or agent to act on. Callers read its
// fields, never its text.
type Error struct {
	Tool     string // the terminal's command, such as tmux
	Op       string // its command, such as new-window
	Target   string // the tab, pane or agent it was for
	NoServer bool   // the terminal isn't running, so there are no tabs or panes at all
	Missing  bool   // the terminal has no such tab or pane, or no such agent
	Err      error  // the failed command (a *command.Error), or what was wrong with its answer
}

// Error is the command's own error, which names it with its arguments, or else what went wrong.
func (e *Error) Error() string {
	if _, ok := errors.AsType[*command.Error](e.Err); ok {
		return e.Err.Error()
	}
	if e.Target == "" {
		return fmt.Sprintf("%s %s: %v", e.Tool, e.Op, e.Err)
	}
	return fmt.Sprintf("%s %s %s: %v", e.Tool, e.Op, e.Target, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// NoServer reports whether err is a terminal's call that failed because the terminal isn't running.
func NoServer(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.NoServer
}

// Missing reports whether err says the terminal has no such tab, pane or agent.
func Missing(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Missing
}

// NoSuch is tool's error for a tab, pane or agent (what) it doesn't have.
func NoSuch(tool, op, target, what string) error {
	return &Error{Tool: tool, Op: op, Target: target, Missing: true, Err: errors.New("no such " + what)}
}

// Odd is tool's error for an answer of its that can't be read.
func Odd(tool, op, target, out string) error {
	return &Error{Tool: tool, Op: op, Target: target, Err: fmt.Errorf("unexpected output: %q", strings.TrimSpace(out))}
}

// Poll is how often a wait reads the worker's state again.
const Poll = 250 * time.Millisecond

// PasteSettle is how long Prompt leaves Claude Code to take a paste in before pressing Enter, which
// it could otherwise take as part of the paste.
var PasteSettle = 300 * time.Millisecond

// Agents are a terminal's agents as the waits read them: by the terminal's Status.
type Agents struct {
	// Tool is the terminal's command, such as tmux, which names it in errors.
	Tool string
	// Status returns the state of the agent named name, StateGone if no pane holds it, and an error if
	// the terminal cannot be asked.
	Status func(ctx context.Context, name string) (agentio.State, error)
}

// Wait reads the agent's state until it is one of want, for up to limit; the error, for op, says it
// went away, or what it was when the time ran out. A state the terminal can't give is read again.
func (a Agents) Wait(ctx context.Context, op, name string, limit time.Duration, want ...agentio.State) error {
	deadline := time.Now().Add(limit)
	for {
		st, err := a.Status(ctx, name)
		switch {
		case err == nil && slices.Contains(want, st):
			return nil
		case err == nil && st == agentio.StateGone:
			return &Error{Tool: a.Tool, Op: op, Target: name, Missing: true, Err: errors.New("the agent is gone")}
		case time.Now().After(deadline) && err != nil:
			return fmt.Errorf("agent %s was not %s within %s: %w", name, states(want),
				command.ShortDuration(limit), err)
		case time.Now().After(deadline):
			return &Error{Tool: a.Tool, Op: op, Target: name, Err: fmt.Errorf("the agent was not %s within %s, but %s",
				states(want), command.ShortDuration(limit), st)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(Poll):
		}
	}
}

// states is "idle or done", for a list of states.
func states(sts []agentio.State) string {
	s := make([]string, len(sts))
	for i, st := range sts {
		s[i] = string(st)
	}
	return strings.Join(s, " or ")
}

// WaitReady waits up to a minute for an agent that is already present to become idle.
func (a Agents) WaitReady(ctx context.Context, name string) bool {
	return a.Wait(ctx, "wait", name, agentio.ReadyWithin, agentio.StateIdle, agentio.StateDone) == nil
}

// WaitStarted waits up to 20 seconds for an agent to start working (or block).
func (a Agents) WaitStarted(ctx context.Context, name string) bool {
	return a.Wait(ctx, "wait", name, agentio.StartedWithin, agentio.StateWorking, agentio.StateBlocked) == nil
}

// StartAgent launches the agent name with launch and waits up to a minute for it to be ready for a
// prompt (idle). An error says it went away or never got ready.
func (a Agents) StartAgent(ctx context.Context, name string, launch func() error) error {
	if err := launch(); err != nil {
		return err
	}
	return a.Wait(ctx, "start", name, agentio.ReadyWithin, agentio.StateIdle, agentio.StateDone)
}

// Prompt puts a prompt in the agent's input box with put, leaves it PasteSettle to take it in,
// presses Enter with enter, and returns once the agent starts on it: working, or blocked (on a
// permission prompt, say). It does not wait for the turn to end. It fails if neither shows within 30
// seconds.
func (a Agents) Prompt(ctx context.Context, name string, put, enter func() error) error {
	if err := put(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(PasteSettle):
	}
	if err := enter(); err != nil {
		return err
	}
	return a.Wait(ctx, "prompt", name, agentio.PromptWithin, agentio.StateWorking, agentio.StateBlocked)
}

// FreeName returns an agent name for an earlier worker that holds name: name-1, name-2, …, the first
// one not taken, or "" if none of the first 20 is free.
func FreeName(name string, taken func(string) bool) string {
	for n := 1; n <= 20; n++ {
		if free := fmt.Sprintf("%s-%d", name, n); !taken(free) {
			return free
		}
	}
	return ""
}

// Names is what a hook-driven terminal says of agents' names, for an adapter to embed: it keeps an
// agent's name itself, so it takes any, and never refuses one (agentio.ErrNameRefused), nor an
// argument (agentio.ErrArgumentRefused), which it passes as it is.
type Names struct{}

// AgentName returns ticket id's worker's agent name: the ID itself, as the terminal has no rule for
// names.
func (Names) AgentName(id string) string { return id }
