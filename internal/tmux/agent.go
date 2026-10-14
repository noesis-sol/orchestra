package tmux

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/hookterm"
)

// AdoptAgent names name the agent of kind launched in the pane (LaunchInPane), and returns its state.
// It needn't wait for the agent to show, as Herdr's does: the pane holds it from its launch.
func (t Terminal) AdoptAgent(ctx context.Context, pane, kind, name string) (dispatch.AgentState, error) {
	ps, err := t.panes(ctx)
	if err != nil {
		return "", err
	}
	p, ok := find(ps, func(p paneInfo) bool { return p.id == pane })
	switch {
	case !ok:
		return "", missing("adopt", pane, "pane")
	case p.kind != kind:
		return "", missing("adopt", pane, kind+" agent launched in the pane")
	}
	if err := t.setName(ctx, pane, name); err != nil {
		return "", err
	}
	return t.state(ctx, p), nil
}

// PaneAgent returns the agent in a pane: its name ("" if it was given none), kind and state, with
// state StateGone if the pane is gone or no agent was launched there, and an error if tmux could
// not be asked.
func (t Terminal) PaneAgent(
	ctx context.Context, pane string,
) (name, kind string, state dispatch.AgentState, err error) {
	ps, err := t.panes(ctx)
	if err != nil {
		return "", "", "", err
	}
	p, ok := find(ps, func(p paneInfo) bool { return p.id == pane })
	if !ok || p.kind == "" {
		return "", "", dispatch.StateGone, nil
	}
	return p.agent, p.kind, t.state(ctx, p), nil
}

// RenameAgent gives the agent, by name in t's repository or by its pane's ID, a new name.
func (t Terminal) RenameAgent(ctx context.Context, name, to string) error {
	ps, err := t.panes(ctx)
	if err != nil {
		return err
	}
	p, ok := t.named(ps, name)
	if !ok {
		p, ok = find(ps, func(p paneInfo) bool { return p.kind != "" && p.id == name })
	}
	if !ok {
		return missing("rename", name, "agent")
	}
	return t.setName(ctx, p.id, to)
}

// FreeName returns an agent name unused in t's repository for an earlier worker that holds name:
// name-1, name-2, …, or "" if none of the first 20 is free or tmux can't say.
func (t Terminal) FreeName(ctx context.Context, name string) string {
	ps, err := t.panes(ctx)
	if err != nil {
		return ""
	}
	return hookterm.FreeName(name, func(free string) bool {
		_, taken := find(ps, func(p paneInfo) bool { return p.agent == free && t.ours(p) })
		return taken
	})
}

// Status returns the agent's state, StateGone if no pane holds it. If tmux cannot be asked it returns
// the error and no state: the agent may well be there.
func (t Terminal) Status(ctx context.Context, name string) (dispatch.AgentState, error) {
	p, ok, err := t.agentPane(ctx, name)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return dispatch.StateGone, nil
	}
	return t.state(ctx, p), nil
}

// shells are the processes a pane runs when it is back at a shell's prompt.
var shells = []string{"sh", "bash", "zsh", "fish", "dash", "ksh", "mksh", "tcsh", "csh", "nu"}

// state is the state of the agent in the pane p: gone once its process exited (the pane dead), its
// session ended or a pane orchestra's launch no longer runs is back at a shell; otherwise what its
// hooks tell, read with its visible screen (claude.Hooks.Settle).
// A shell counts only once a hook ran: as the agent starts, the shell that runs it shows for a moment.
// Nor does it count in a pane whose process is the one orchestra launched: that runs the agent through
// exec, and ends dead; a shell there is a wrapper script that runs the agent as its child, which tmux
// names as the foreground process.
func (t Terminal) state(ctx context.Context, p paneInfo) dispatch.AgentState {
	if p.dead || p.kind == "" {
		return dispatch.StateGone
	}
	h := claude.ReadHooks(p.records, p.dir)
	switch {
	case h.State == dispatch.StateGone:
		return h.State
	case h.State != dispatch.StateUnknown && !p.launched && slices.Contains(shells, strings.TrimPrefix(p.command, "-")):
		return dispatch.StateGone
	}
	return h.Settle(func() (string, error) { return t.capture(ctx, p.id, true) })
}

// capture reads the pane's screen: its last dispatch.ScreenLines lines, or only those of the visible
// screen if visible, with wrapped lines joined and the empty lines below its last one left out.
func (t Terminal) capture(ctx context.Context, pane string, visible bool) (string, error) {
	args := []string{"-p", "-J", "-t", pane}
	if !visible {
		args = append(args, "-S", strconv.Itoa(-dispatch.ScreenLines))
	}
	out, err := t.run(ctx, command.ReadLimit, "capture-pane", pane, args...)
	if err != nil {
		return "", err
	}
	return dispatch.LastLines(out, dispatch.ScreenLines), nil
}

// Screen returns the end of the agent's terminal, given its state as just read ("" if not known), as
// dispatch.Agents gives it: the last dispatch.ScreenLines lines, only of the visible screen while it
// works or is blocked. "" when tmux can't read it.
func (t Terminal) Screen(ctx context.Context, name string, state dispatch.AgentState) string {
	p, ok, err := t.agentPane(ctx, name)
	if err != nil || !ok {
		return ""
	}
	screen, _ := t.capture(ctx, p.id, state == dispatch.StateWorking || state == dispatch.StateBlocked)
	return screen // "" when tmux can't read it
}

// Prompt pastes the prompt into the agent's input box (bracketed, so its lines don't submit it one by
// one), presses Enter, and returns once the agent starts on it: working, or blocked (on a permission
// prompt, say). It does not wait for the turn to end. It fails if neither shows within 30 seconds.
func (t Terminal) Prompt(ctx context.Context, name, prompt string) error {
	p, ok, err := t.agentPane(ctx, name)
	switch {
	case err != nil:
		return err
	case !ok:
		return missing("prompt", name, "agent")
	}
	paste := func() error {
		buf := "orchestra-" + strings.TrimPrefix(p.id, "%")
		if _, err := t.runWithInput(ctx, command.ReadLimit, "load-buffer", name, prompt, "-b", buf, "-"); err != nil {
			return err
		}
		_, err := t.run(ctx, command.ReadLimit, "paste-buffer", name, "-d", "-p", "-b", buf, "-t", p.id)
		return err
	}
	enter := func() error {
		_, err := t.run(ctx, command.ReadLimit, "send-keys", name, "-t", p.id, "Enter")
		return err
	}
	return t.agents().Prompt(ctx, name, paste, enter)
}

// SendKeys presses keys in the agent's pane, by Herdr's names (enter, esc, ctrl+c, up…); anything
// else is typed as it is.
func (t Terminal) SendKeys(ctx context.Context, name string, keys ...string) error {
	p, ok, err := t.agentPane(ctx, name)
	switch {
	case err != nil:
		return err
	case !ok:
		return missing("send-keys", name, "agent")
	}
	var cmds [][]string
	for _, k := range keys {
		if key, ok := keyNames.Name(k); ok {
			cmds = append(cmds, []string{"-t", p.id, key})
			continue
		}
		// "--" keeps text that starts with '-' from reading as options. tmux takes an argument ending
		// in ';' for the end of a command, so the text's last semicolons go as their code.
		text := strings.TrimRight(k, ";")
		if text != "" {
			cmds = append(cmds, []string{"-t", p.id, "-l", "--", text})
		}
		if n := len(k) - len(text); n > 0 {
			cmds = append(cmds, append([]string{"-t", p.id, "-H"}, slices.Repeat([]string{"3b"}, n)...))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	args := cmds[0]
	for _, c := range cmds[1:] {
		args = append(append(args, ";", "send-keys"), c...)
	}
	_, err = t.run(ctx, command.ReadLimit, "send-keys", name, args...)
	return err
}

// agents are t's agents as hookterm waits for them.
func (t Terminal) agents() hookterm.Agents { return hookterm.Agents{Tool: tool, Status: t.Status} }

// WaitReady waits up to a minute for an agent that is already present to become idle.
func (t Terminal) WaitReady(ctx context.Context, name string) bool {
	return t.agents().WaitReady(ctx, name)
}

// WaitStarted waits up to 20 seconds for an agent to start working (or block).
func (t Terminal) WaitStarted(ctx context.Context, name string) bool {
	return t.agents().WaitStarted(ctx, name)
}
