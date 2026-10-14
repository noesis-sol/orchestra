package cmux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/hookterm"
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
// hook); otherwise what its hooks tell, read with its visible screen (the AgentUI of its kind). A process
// cmux can't list, or hasn't yet, is taken to be there.
func (t Terminal) state(ctx context.Context, id string, e surfaceEntry, l layout) dispatch.AgentState {
	ws, open := l.workspaces[key(id)]
	if !open || e.Kind == "" {
		return dispatch.StateGone
	}
	told, settle := hookterm.UI(e.Kind).Read(e.Records, e.Dir)
	if told == dispatch.StateGone {
		return told
	}
	if pid, at, ok := t.launched(id); ok {
		if alive, err := t.running(ctx, ws, pid, at); err == nil && !alive {
			return dispatch.StateGone
		}
	}
	return settle(func() (string, error) { return t.readScreen(ctx, ws, id, true) })
}

// running reports whether process pid, launched at since, runs in the workspace, as cmux's top lists
// its processes; one that top's sample can't have listed is taken to run as long as it exists. A
// process ID names what a launch started from the script's start (which writes it) to its command's
// end, as the command replaces the script; the workspace is orchestra's, so a process given the same
// ID since isn't found there.
func (t Terminal) running(ctx context.Context, workspace string, pid int, since time.Time) (bool, error) {
	out, err := t.run(ctx, "top", workspace, true, "--processes", "--workspace", workspace)
	if err != nil {
		return false, err
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return false, odd("top", workspace, out)
	}
	if hasPID(v, pid) {
		return true, nil
	}
	return !sampledSince(v, since) && exists(pid), nil
}

// exists reports whether a process pid exists, on this machine, where cmux runs too.
func exists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// sampledSince reports whether the JSON value v, cmux top's answer, lists every process cmux had
// after since. From 0.65.0 top answers from a sample of the processes, taken every few seconds, which
// says when it was taken (sampled_at, to the second) and whether it found them all
// (enumeration_complete): a process started after the sample, or missed by it, isn't listed. An
// answer that says nothing of its sample, as before 0.65.0, lists the processes cmux has.
func sampledSince(v any, since time.Time) bool {
	m, _ := v.(map[string]any)
	sample, ok := m["sample"].(map[string]any)
	if !ok {
		return true
	}
	if complete, ok := sample["enumeration_complete"].(bool); ok && !complete {
		return false
	}
	at, ok := sample["sampled_at"].(string)
	if !ok {
		return true
	}
	taken, err := time.Parse(time.RFC3339, at)
	return err != nil || taken.After(since)
}

// hasPID reports whether the JSON value v lists process pid, at any depth: an object whose pid is
// pid, or pid in a list of process IDs (pids, tty_process_pids…) other than missing_pids, those of
// processes gone. cmux 0.65.0 lists a terminal's processes it can't put down to one program
// (tty-collision, unattributed) only in such lists.
func hasPID(v any, pid int) bool {
	switch v := v.(type) {
	case map[string]any:
		if n, ok := v["pid"].(float64); ok && int(n) == pid {
			return true
		}
		for k, c := range v {
			if l, ok := c.([]any); ok && strings.HasSuffix(k, "pids") && k != "missing_pids" &&
				slices.ContainsFunc(l, func(n any) bool { f, ok := n.(float64); return ok && int(f) == pid }) {
				return true
			}
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
	return hookterm.FreeName(name, func(free string) bool {
		return slices.ContainsFunc(entries(b), func(e surfaceEntry) bool { return e.Agent == free })
	})
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

// Screen returns the end of the agent's terminal, given its state as just read ("" if not known), as
// dispatch.Agents gives it: the last dispatch.ScreenLines lines, only of the visible screen while
// it works or is blocked. "" when cmux can't read it.
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
	return t.agents().Prompt(ctx, name, func() error { return t.givePrompt(ctx, name, id, e, prompt) },
		func() error { return t.pressKey(ctx, e.Workspace, id, "enter") })
}

// givePrompt puts the prompt in the input box of the agent name in surface id, as Prompt says, without
// pressing Enter.
func (t Terminal) givePrompt(ctx context.Context, name, id string, e surfaceEntry, prompt string) error {
	switch {
	case t.features(ctx).paste:
		return t.paste(ctx, e.Workspace, id, prompt)
	case !throughFile(prompt):
		return t.typeText(ctx, e.Workspace, id, prompt)
	}
	if err := writePrompt(e.Dir, id, prompt); err != nil {
		return fmt.Errorf("cannot give agent %s its prompt: %w", name, err)
	}
	return t.typeText(ctx, e.Workspace, id, fileLine(id))
}

// TypedPrompt is what Prompt puts in the agent's input box for prompt: the line it types to attach
// the file it writes the prompt to, where it writes one, and otherwise the prompt itself.
func (t Terminal) TypedPrompt(ctx context.Context, name, prompt string) string {
	id, _, ok, err := t.agentSurface(name)
	if err != nil || !ok || t.features(ctx).paste || !throughFile(prompt) {
		return prompt
	}
	return fileLine(id)
}

// throughFile reports whether a cmux that doesn't paste gives the prompt through a file: one it
// can't type as it is, or too long to.
func throughFile(prompt string) bool { return !typeable(prompt) || len(prompt) > longestTyped }

// promptFile is the path, relative to its checkout, of the file a prompt for surface id is written
// to.
func promptFile(id string) string { return project.RunPath("cmux-prompt-" + key(id) + ".md") }

// fileLine is the line typed into surface id's input box to attach the file its prompt is in.
func fileLine(id string) string {
	return "My message to you is in @" + promptFile(id) + " : read it, and take it as if I had typed it here."
}

// writePrompt writes prompt to the run file of surface id in the checkout dir (promptFile).
func writePrompt(dir, id, prompt string) error {
	if dir == "" {
		return errors.New("no folder to write it in")
	}
	root, err := project.OpenRun(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	return project.WriteRun(root, dir, promptFile(id), []byte(prompt), 0o644)
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
		if name, isKey := keyNames.Name(k); isKey {
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
