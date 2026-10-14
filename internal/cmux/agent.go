package cmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// agentSurface returns the surface holding the agent named name, as the book has it, and false if
// no surface orchestra launched an agent in holds one so named.
func (t Terminal) agentSurface(name string) (string, surfaceEntry, bool, error) {
	b, err := t.view()
	if err != nil {
		return "", surfaceEntry{}, false, err
	}
	for id, e := range b.Surfaces {
		if e.Kind != "" && e.Agent == name {
			return id, e, true, nil
		}
	}
	return "", surfaceEntry{}, false, nil
}

// state is the state of the agent the book has in surface id: gone once cmux has no such surface,
// once nothing was launched there, once its session ended, or once the process the launch started
// is no longer in its workspace (killed, crashed, or ended by cmux quitting, none of which runs a
// hook); otherwise what its hooks tell (claude.State), and blocked while its screen shows a dialog
// that runs no hook. A process cmux can't list is taken to be there.
func (t Terminal) state(ctx context.Context, id string, e surfaceEntry, l layout) dispatch.AgentState {
	ws, open := l.workspaces[key(id)]
	if !open || e.Kind == "" {
		return dispatch.StateGone
	}
	records := e.Records
	if records == "" {
		records = claude.Records(e.Dir, "") // launched without hooks: a ticket's worktree has them there
	}
	st := claude.State(records)
	if st == dispatch.StateGone {
		return st
	}
	if pid, ok := t.launched(id); ok {
		if alive, err := t.running(ctx, ws, pid); err == nil && !alive {
			return dispatch.StateGone
		}
	}
	if st == dispatch.StateUnknown || st == dispatch.StateIdle {
		if screen, err := t.readScreen(ctx, ws, id, true); err == nil && showsDialog(screen) {
			return dispatch.StateBlocked
		}
	}
	return st
}

// running reports whether process pid runs in the workspace, as cmux's top lists its processes. A
// process ID names what a launch started from the script's start (which writes it) to its command's
// end, as the command replaces the script; the workspace is orchestra's, so a process given the
// same ID since isn't found there.
func (t Terminal) running(ctx context.Context, workspace string, pid int) (bool, error) {
	out, err := t.run(ctx, "top", workspace, true, "--processes", "--workspace", workspace)
	if err != nil {
		return false, err
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return false, odd("top", workspace, out)
	}
	return hasPID(v, pid), nil
}

// hasPID reports whether the JSON value v holds an object whose pid is pid, at any depth.
func hasPID(v any, pid int) bool {
	switch v := v.(type) {
	case map[string]any:
		if n, ok := v["pid"].(float64); ok && int(n) == pid {
			return true
		}
		for _, c := range v {
			if hasPID(c, pid) {
				return true
			}
		}
	case []any:
		if slices.ContainsFunc(v, func(c any) bool { return hasPID(c, pid) }) {
			return true
		}
	}
	return false
}

// dialogs are what Claude Code shows of the dialogs it asks before a session starts, which run no
// hook: whether to trust the folder, and whether to use the project's MCP servers.
var dialogs = []string{"trust this folder", "mcp server found in this project", "mcp servers found in this project"}

// showsDialog reports whether the bottom of screen shows one of Claude Code's dialogs.
func showsDialog(screen string) bool {
	lines := strings.Split(strings.ToLower(screen), "\n")
	lines = lines[max(0, len(lines)-25):]
	for _, l := range lines {
		for _, d := range dialogs {
			if strings.Contains(l, d) {
				return true
			}
		}
	}
	return false
}

// AdoptAgent names name the agent of kind launched in the surface (LaunchInPane), and returns its
// state. It needn't wait for the agent to show, as Herdr's does: the book has it from its launch.
func (t Terminal) AdoptAgent(ctx context.Context, pane, kind, name string) (dispatch.AgentState, error) {
	b, err := t.view()
	if err != nil {
		return "", err
	}
	e, ok := b.Surfaces[key(pane)]
	switch {
	case !ok:
		return "", missing("adopt", pane, "terminal orchestra opened")
	case e.Kind != kind:
		return "", missing("adopt", pane, kind+" agent launched in the terminal")
	}
	if err := t.edit(func(b *book) error { setName(b, pane, name); return nil }); err != nil {
		return "", err
	}
	l, err := t.layout(ctx)
	if err != nil {
		return "", err
	}
	return t.state(ctx, key(pane), e, l), nil
}

// PaneAgent returns the agent in a surface: its name ("" if it was given none), kind and state, with
// state StateGone if the surface is gone or no agent was launched there, and an error if cmux could
// not be asked.
func (t Terminal) PaneAgent(
	ctx context.Context, pane string,
) (name, kind string, state dispatch.AgentState, err error) {
	b, err := t.view()
	if err != nil {
		return "", "", "", err
	}
	e, ok := b.Surfaces[key(pane)]
	if !ok || e.Kind == "" {
		return "", "", dispatch.StateGone, nil
	}
	l, err := t.layout(ctx)
	if err != nil {
		return "", "", "", err
	}
	return e.Agent, e.Kind, t.state(ctx, key(pane), e, l), nil
}

// RenameAgent gives the agent, by name or by its surface's UUID, a new name.
func (t Terminal) RenameAgent(_ context.Context, name, to string) error {
	return t.edit(func(b *book) error {
		for id, e := range b.Surfaces {
			if e.Kind != "" && (e.Agent == name || sameID(id, name)) {
				setName(b, id, to)
				return nil
			}
		}
		return missing("rename", name, "agent")
	})
}

// FreeName returns an unused agent name for an earlier worker that holds name: name-1, name-2, …,
// or "" if none of the first 20 is free or the book can't say.
func (t Terminal) FreeName(_ context.Context, name string) string {
	b, err := t.view()
	if err != nil {
		return ""
	}
	for n := 1; n <= 20; n++ {
		free := fmt.Sprintf("%s-%d", name, n)
		if !slices.ContainsFunc(entries(b), func(e surfaceEntry) bool { return e.Agent == free }) {
			return free
		}
	}
	return ""
}

// entries are the book's surfaces.
func entries(b book) []surfaceEntry {
	es := make([]surfaceEntry, 0, len(b.Surfaces))
	for _, e := range b.Surfaces {
		es = append(es, e)
	}
	return es
}

// Status returns the agent's state, StateGone if no surface holds it. If cmux cannot be asked it
// returns the error and no state: the agent may well be there.
func (t Terminal) Status(ctx context.Context, name string) (dispatch.AgentState, error) {
	id, e, ok, err := t.agentSurface(name)
	switch {
	case err != nil:
		return "", err
	case !ok:
		return dispatch.StateGone, nil
	}
	l, err := t.layout(ctx)
	if err != nil {
		return "", err
	}
	return t.state(ctx, id, e, l), nil
}

// Screen returns the end of the agent's terminal, given its state as just read ("" if not known):
// the visible screen while it works or is blocked, as Herdr gives it, and otherwise the last 60
// lines. "" when cmux can't read it.
func (t Terminal) Screen(ctx context.Context, name string, state dispatch.AgentState) string {
	id, e, ok, err := t.agentSurface(name)
	if err != nil || !ok {
		return ""
	}
	screen, _ := t.readScreen(ctx, e.Workspace, id, state == dispatch.StateWorking || state == dispatch.StateBlocked)
	return screen // "" when cmux can't read it
}

// longestTyped is the longest prompt typed into the agent's input box as it is, by a cmux that
// doesn't paste.
const longestTyped = 2000

// Prompt gives the agent the prompt, presses Enter, and returns once the agent starts on it:
// working, or blocked (on a permission prompt, say). It does not wait for the turn to end. It fails
// if neither shows within 30 seconds. A cmux that pastes (cmux paste, from 0.65.0) pastes the prompt
// into the input box, bracketed, so its lines don't submit it one by one. An older one types what it
// is given and would press Enter at each line break: a prompt of one line is typed into the input
// box, and any other is written to a file in the agent's folder's .orchestra/run/, which a line typed
// there attaches (@), as Claude Code attaches a file named after @.
func (t Terminal) Prompt(ctx context.Context, name, prompt string) error {
	id, e, ok, err := t.agentSurface(name)
	switch {
	case err != nil:
		return err
	case !ok:
		return missing("prompt", name, "agent")
	}
	if err := t.givePrompt(ctx, name, id, e, prompt); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(pasteSettle):
	}
	if err := t.pressKey(ctx, e.Workspace, id, "enter"); err != nil {
		return err
	}
	return t.waitFor(ctx, "prompt", name, 30*time.Second, dispatch.StateWorking, dispatch.StateBlocked)
}

// givePrompt puts the prompt in the input box of the agent name in surface id, as Prompt says, without
// pressing Enter.
func (t Terminal) givePrompt(ctx context.Context, name, id string, e surfaceEntry, prompt string) error {
	if t.features(ctx).paste {
		return t.paste(ctx, e.Workspace, id, prompt)
	}
	line := prompt
	if !typeable(prompt) || len(prompt) > longestTyped {
		rel, err := writePrompt(e.Dir, id, prompt)
		if err != nil {
			return fmt.Errorf("cannot give agent %s its prompt: %w", name, err)
		}
		line = "My message to you is in @" + rel + " : read it, and take it as if I had typed it here."
	}
	return t.typeText(ctx, e.Workspace, id, line)
}

// writePrompt writes prompt to the run file of surface id in the checkout dir, and returns its path
// relative to dir.
func writePrompt(dir, id, prompt string) (string, error) {
	if dir == "" {
		return "", errors.New("no folder to write it in")
	}
	root, err := project.OpenRun(dir)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := project.RunPath("cmux-prompt-" + key(id) + ".md")
	if err := project.WriteRun(root, dir, rel, []byte(prompt), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// SendKeys presses keys in the agent's surface, by Herdr's names (enter, esc, ctrl+c, up…); anything
// else is typed as it is.
func (t Terminal) SendKeys(ctx context.Context, name string, keys ...string) error {
	id, e, ok, err := t.agentSurface(name)
	switch {
	case err != nil:
		return err
	case !ok:
		return missing("send-key", name, "agent")
	}
	for _, k := range keys {
		if name, isKey := cmuxKey(k); isKey {
			err = t.pressKey(ctx, e.Workspace, id, name)
		} else {
			err = t.typeText(ctx, e.Workspace, id, k)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// WaitReady waits up to a minute for an agent that is already present to become idle.
func (t Terminal) WaitReady(ctx context.Context, name string) bool {
	return t.waitFor(ctx, "wait", name, time.Minute, dispatch.StateIdle, dispatch.StateDone) == nil
}

// WaitStarted waits up to 20 seconds for an agent to start working (or block).
func (t Terminal) WaitStarted(ctx context.Context, name string) bool {
	return t.waitFor(ctx, "wait", name, 20*time.Second, dispatch.StateWorking, dispatch.StateBlocked) == nil
}

// waitFor reads the agent's state until it is one of want, for up to limit; the error says it went
// away, or what it was when the time ran out. A state cmux can't give is read again.
func (t Terminal) waitFor(ctx context.Context, op, name string, limit time.Duration,
	want ...dispatch.AgentState) error {
	deadline := time.Now().Add(limit)
	for {
		st, err := t.Status(ctx, name)
		switch {
		case err == nil && slices.Contains(want, st):
			return nil
		case err == nil && st == dispatch.StateGone:
			return &Error{Op: op, Target: name, Missing: true, Err: errors.New("the agent is gone")}
		case time.Now().After(deadline) && err != nil:
			return fmt.Errorf("agent %s was not %s within %s: %w", name, states(want),
				command.ShortDuration(limit), err)
		case time.Now().After(deadline):
			return &Error{Op: op, Target: name, Err: fmt.Errorf("the agent was not %s within %s, but %s",
				states(want), command.ShortDuration(limit), st)}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// states is "idle or done", for a list of states.
func states(sts []dispatch.AgentState) string {
	s := make([]string, len(sts))
	for i, st := range sts {
		s[i] = string(st)
	}
	return strings.Join(s, " or ")
}
