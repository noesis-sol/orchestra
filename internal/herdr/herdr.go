// Package herdr is orchestra's adapter for Herdr: the tabs workers run in, starting and naming
// agents, reading their status and screen, and the workspace orchestra runs in.
package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Terminal is Herdr as orchestra uses it: tabs for workers, starting and naming agents, and reading
// their status and screen.
type Terminal struct{}

// CreateTab opens a tab labelled label in workspace, starting in cwd, without switching to it, and
// returns the tab's ID and its first pane's. When Herdr's answer can't be read or lacks either ID,
// or the call fails without Herdr refusing it (stopped at its time limit or by Ctrl+C, say), the
// tab it may have opened is closed again (see closeUnread), so a retry doesn't leave another
// behind. Finding that tab asks Herdr for its tabs, with the time limit of any Herdr call, even
// after the call ran into that limit: a Herdr that didn't answer then may well not answer now, which
// costs the limit again, while a tab that was opened is closed.
func (t Terminal) CreateTab(ctx context.Context, workspace, cwd, label string) (tab, pane string, err error) {
	// The tabs labelled label already, such as an earlier worker's left for review, from which a
	// tab this call opens is told should Herdr's answer not say which it is.
	before, berr := labelledTabs(ctx, workspace, label)
	out, err := run(ctx, command.ReadLimit,
		"tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		if !mayHaveOpened(err) {
			return "", "", err
		}
		return "", "", t.closeUnread(context.WithoutCancel(ctx), err, workspace, label, "", before, berr)
	}
	var r struct {
		Result struct {
			Tab struct {
				TabID string `json:"tab_id"`
			} `json:"tab"`
			RootPane struct {
				PaneID string `json:"pane_id"`
			} `json:"root_pane"`
		} `json:"result"`
	}
	// Output that is JSON but not of this shape still gives the IDs read before the mismatch.
	jerr := json.Unmarshal([]byte(out), &r)
	tab, pane = r.Result.Tab.TabID, r.Result.RootPane.PaneID
	switch {
	case jerr != nil:
		err = fmt.Errorf("unexpected 'herdr tab create' output (%w): %s", jerr, strings.TrimSpace(out))
	case tab == "" || pane == "":
		err = fmt.Errorf("unexpected 'herdr tab create' output: %s", strings.TrimSpace(out))
	default:
		return tab, pane, nil
	}
	// Even once the run is stopped, the tab is closed, as a worker's tab is (each call has its limit).
	return "", "", t.closeUnread(context.WithoutCancel(ctx), err, workspace, label, tab, before, berr)
}

// mayHaveOpened reports whether a 'herdr tab create' that failed with err may have opened the tab
// all the same: it was stopped, or it exited without Herdr's own error. Herdr refusing the call
// opened none, and nor did a herdr that never started.
func mayHaveOpened(err error) bool {
	var he *Error
	var ce *command.Error
	var ee *exec.ExitError
	return !errors.As(err, &he) && errors.As(err, &ce) && (ce.Stopped || errors.As(ce.Err, &ee))
}

// closeUnread closes the tab that 'herdr tab create' may have opened in workspace, though the call
// failed or its answer can't be used, as err says, and adds to err what became of the tab. The tab
// is the one the answer named, if it named one; otherwise the one tab labelled label that isn't
// among before, the tabs so labelled before the call (unknown if berr says Herdr couldn't list
// them). err stays the error underneath: a failure to find or close the tab is only told.
func (t Terminal) closeUnread(ctx context.Context, err error, workspace, label, tab string,
	before []string, berr error) error {
	if tab == "" {
		after, aerr := labelledTabs(ctx, workspace, label)
		if aerr != nil {
			return fmt.Errorf("%w; a tab labelled %s may be left open: Herdr couldn't list the tabs (%v)",
				err, label, aerr)
		}
		opened := slices.DeleteFunc(after, func(id string) bool { return slices.Contains(before, id) })
		switch {
		case len(opened) == 0:
			return fmt.Errorf("%w; Herdr has no new tab labelled %s", err, label)
		case berr != nil:
			return fmt.Errorf("%w; %s, labelled %s, left open: Herdr couldn't list the tabs before, "+
				"to tell the one it opened from earlier ones (%v)", err, tabList(opened), label, berr)
		case len(opened) > 1:
			return fmt.Errorf("%w; %s, labelled %s, opened meanwhile and left open, as which is the one "+
				"Herdr opened can't be told", err, tabList(opened), label)
		}
		tab = opened[0]
	}
	if cerr := t.CloseTab(ctx, tab); cerr != nil {
		return fmt.Errorf("%w; tab %s, which Herdr opened, could not be closed (%v)", err, tab, cerr)
	}
	return fmt.Errorf("%w; tab %s, which Herdr opened, is closed again", err, tab)
}

// tabList is "tab <id>" for one tab, and "tabs <id>, <id>, …" for several.
func tabList(ids []string) string {
	if len(ids) == 1 {
		return "tab " + ids[0]
	}
	return "tabs " + strings.Join(ids, ", ")
}

// labelledTabs returns the IDs of the tabs in workspace that are labelled label.
func labelledTabs(ctx context.Context, workspace, label string) ([]string, error) {
	out, err := run(ctx, command.ReadLimit, "tab", "list", "--workspace", workspace)
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Tabs []struct {
				TabID string `json:"tab_id"`
				Label string `json:"label"`
			} `json:"tabs"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.Result.Tabs == nil {
		return nil, fmt.Errorf("unexpected 'herdr tab list' output: %s", out)
	}
	var ids []string
	for _, tb := range r.Result.Tabs {
		if tb.Label == label {
			ids = append(ids, tb.TabID)
		}
	}
	return ids, nil
}

// CloseTab closes a Herdr tab.
func (t Terminal) CloseTab(ctx context.Context, tab string) error {
	_, err := run(ctx, command.ReadLimit, "tab", "close", tab)
	return err
}

// FocusTab switches Herdr to the tab, as the maintainer would by clicking it.
func (t Terminal) FocusTab(ctx context.Context, tab string) error {
	_, err := run(ctx, command.ReadLimit, "tab", "focus", tab)
	return err
}

// TabLabel returns the tab's label, and false if Herdr has no such tab (tab_not_found), which is no
// error. Herdr numbers tabs afresh when it starts without restoring its last session, so an ID
// recorded before may by then name another tab: its label tells them apart.
func (t Terminal) TabLabel(ctx context.Context, tab string) (label string, open bool, err error) {
	out, err := run(ctx, command.ReadLimit, "tab", "get", tab)
	if HasCode(err, TabNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var r struct {
		Result struct {
			Tab struct {
				TabID string `json:"tab_id"`
				Label string `json:"label"`
			} `json:"tab"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.Result.Tab.TabID == "" {
		return "", false, fmt.Errorf("unexpected 'herdr tab get' output: %s", out)
	}
	return r.Result.Tab.Label, true, nil
}

// SplitPane splits the pane, opening a new one to its right that starts in cwd and has the keyboard
// focus, and returns the new pane's ID.
func (t Terminal) SplitPane(ctx context.Context, pane, cwd string) (string, error) {
	out, err := run(ctx, command.ReadLimit, "pane", "split", pane, "--direction", "right", "--cwd", cwd, "--focus")
	if err != nil {
		return "", err
	}
	var r struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.Result.Pane.PaneID == "" {
		return "", fmt.Errorf("unexpected 'herdr pane split' output: %s", out)
	}
	return r.Result.Pane.PaneID, nil
}

// ClosePane closes a Herdr pane, ending what runs in it. A pane that is already gone
// (pane_not_found), closed by the user say, is no error.
func (t Terminal) ClosePane(ctx context.Context, pane string) error {
	_, err := run(ctx, command.ReadLimit, "pane", "close", pane)
	if HasCode(err, PaneNotFound) {
		return nil
	}
	return err
}

// PaneOpen reports whether Herdr still has the pane; false (pane_not_found) is no error. PaneAgent
// cannot tell: Herdr answers agent_not_found both for a pane whose agent has left and for a pane
// that is gone.
func (t Terminal) PaneOpen(ctx context.Context, pane string) (bool, error) {
	out, err := run(ctx, command.ReadLimit, "pane", "get", pane)
	if HasCode(err, PaneNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var r struct {
		Result struct {
			Pane struct {
				PaneID string `json:"pane_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.Result.Pane.PaneID == "" {
		return false, fmt.Errorf("unexpected 'herdr pane get' output: %s", out)
	}
	return true, nil
}

// StartAgent starts an agent in the pane, passing it args; a prompt among them starts it with the
// prompt already submitted. Herdr types the command into the pane's shell and refuses arguments
// with line breaks, so each must be one line.
func (t Terminal) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	const wait = time.Minute
	a := []string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", millis(wait)}
	if len(args) > 0 {
		a = append(append(a, "--"), args...)
	}
	_, err := run(ctx, wait+command.ReadLimit, a...)
	return err
}

// Codes Herdr gives its errors, the ones orchestra reacts to.
const (
	AgentNotFound        = "agent_not_found"        // no agent by that name or in that pane
	TabNotFound          = "tab_not_found"          // no tab by that ID
	PaneNotFound         = "pane_not_found"         // no pane by that ID
	InvalidAgentName     = "invalid_agent_name"     // a name outside Herdr's rule for agent names
	InvalidAgentArgument = "invalid_agent_argument" // an argument Herdr can't type into a shell
)

// Error is Herdr refusing a call, as it answers on stderr:
// {"error":{"code":"agent_not_found","message":"…"},"id":"cli:agent:get"}. Err is the failed
// command, so the text stays the command's own.
type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// HasCode reports whether err is Herdr refusing a call with code.
func HasCode(err error, code string) bool {
	var he *Error
	return errors.As(err, &he) && he.Code == code
}

// run runs herdr with args and returns its stdout. When Herdr refuses the call with an error of its
// own, that is an *Error wrapping the command's.
func run(ctx context.Context, limit time.Duration, args ...string) (string, error) {
	out, err := command.Output(ctx, limit, "", "herdr", args...)
	return out, decode(err)
}

// decode turns a failed herdr call whose stderr holds Herdr's JSON error into an *Error, and leaves
// any other error as it is.
func decode(err error) error {
	var ce *command.Error
	if !errors.As(err, &ce) {
		return err
	}
	var r struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(ce.Stderr), &r) != nil || r.Error.Code == "" {
		return err
	}
	return &Error{Code: r.Error.Code, Message: r.Error.Message, Err: err}
}

// millis is d in milliseconds, as Herdr's --timeout takes it. Herdr's own wait gets
// command.ReadLimit to spare before the call to it is stopped.
func millis(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) }

// IsArgumentRefused reports Herdr refusing agent arguments it cannot pass through the shell; a
// retry cannot succeed.
func (t Terminal) IsArgumentRefused(err error) bool { return HasCode(err, InvalidAgentArgument) }

// IsNameRefused reports Herdr refusing an agent name; a retry under the same name cannot succeed.
func (t Terminal) IsNameRefused(err error) bool { return HasCode(err, InvalidAgentName) }

// maxName is the longest agent name Herdr accepts.
const maxName = 32

// AgentName returns the Herdr agent name for ticket id. Herdr takes names of 1-32 characters from
// [a-z0-9_-] starting with a letter, while bd IDs can hold capitals (a prefix taken from the folder
// name) and dots (every child ID), and can be longer. Capitals are lowered, anything else becomes
// '_', a name not starting with a letter gets a 't' in front, and a name that is too long is cut
// and ends in a hash of the whole ID, so two long IDs sharing a start still get different names.
func AgentName(id string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name := b.String()
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		name = "t" + name
	}
	if len(name) > maxName {
		sum := sha256.Sum256([]byte(id))
		hash := hex.EncodeToString(sum[:3])
		name = name[:maxName-len(hash)-1] + "-" + hash
	}
	return name
}

// AgentName returns the Herdr agent name for ticket id; see the function AgentName.
func (t Terminal) AgentName(id string) string { return AgentName(id) }

// LaunchInPane types '<kind> <args…>' into the pane's shell, as 'herdr agent start' would, and
// returns at once. Each argument must be one line.
func (t Terminal) LaunchInPane(ctx context.Context, pane, kind string, args []string) error {
	var line strings.Builder
	line.WriteString(kind)
	for _, a := range args {
		line.WriteString(" " + command.ShellQuote(a))
	}
	_, err := run(ctx, command.ReadLimit, "pane", "run", pane, line.String())
	return err
}

// AdoptAgent waits up to a minute for Herdr to recognise an agent of kind in the pane, names it
// name, and returns its state. The error says why it could not, such as Herdr refusing the name.
func (t Terminal) AdoptAgent(ctx context.Context, pane, kind, name string) (dispatch.AgentState, error) {
	deadline := time.Now().Add(time.Minute)
	for {
		n, k, st, err := t.PaneAgent(ctx, pane)
		if err == nil && st != dispatch.StateGone && k == kind {
			if n != name {
				if err := t.RenameAgent(ctx, pane, name); err != nil {
					return st, err
				}
			}
			return st, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", fmt.Errorf("no %s agent appeared in pane %s within a minute; "+
					"Herdr could not say what it holds: %w", kind, pane, err)
			}
			return "", fmt.Errorf("no %s agent appeared in pane %s within a minute", kind, pane)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// PaneAgent returns the agent in a pane: its name ("" if Herdr gave it none), kind and state, with
// state StateGone if the pane holds no agent, and an error if Herdr could not be asked.
func (t Terminal) PaneAgent(
	ctx context.Context, pane string,
) (name, kind string, state dispatch.AgentState, err error) {
	return readAgent(run(ctx, command.ReadLimit, "agent", "get", pane))
}

// readAgent reads the output of 'herdr agent get': the agent's name, kind and state. Herdr answers
// a missing agent with agent_not_found (and fails), which is StateGone, as is a result with no
// agent in it; any other failure (Herdr busy or restarting, say) says nothing about the agent, so
// it is the error, with no state. So is an agent without a status: a Herdr that renamed or dropped
// agent_status would otherwise make every live worker look gone.
func readAgent(out string, err error) (name, kind string, state dispatch.AgentState, _ error) {
	if HasCode(err, AgentNotFound) {
		return "", "", dispatch.StateGone, nil
	}
	var r struct {
		Result *struct {
			Agent *struct {
				Name        *string `json:"name"`
				Agent       string  `json:"agent"`
				AgentStatus string  `json:"agent_status"`
			} `json:"agent"`
		} `json:"result"`
	}
	jerr := json.Unmarshal([]byte(out), &r)
	switch {
	case err != nil:
		return "", "", "", err
	case jerr != nil || r.Result == nil:
		return "", "", "", fmt.Errorf("unexpected 'herdr agent get' output: %s", out)
	case r.Result.Agent == nil:
		return "", "", dispatch.StateGone, nil
	case r.Result.Agent.AgentStatus == "":
		return "", "", "", fmt.Errorf("unexpected 'herdr agent get' output: %s", out)
	}
	a := r.Result.Agent
	if a.Name != nil {
		name = *a.Name
	}
	return name, a.Agent, dispatch.AgentState(a.AgentStatus), nil
}

// RenameAgent gives the agent (by name or pane) a new name.
func (t Terminal) RenameAgent(ctx context.Context, name, to string) error {
	_, err := run(ctx, command.ReadLimit, "agent", "rename", name, to)
	return err
}

// FreeName returns an unused agent name for an earlier worker that holds name: name-1, name-2, …,
// within Herdr's 32-character limit, or "" if none is free.
func (t Terminal) FreeName(ctx context.Context, name string) string {
	for n := 1; n <= 20; n++ {
		suffix := fmt.Sprintf("-%d", n)
		base := name
		if len(base)+len(suffix) > maxName {
			base = base[:maxName-len(suffix)]
		}
		// Not unreadable: that name may be taken.
		if st, err := t.Status(ctx, base+suffix); err == nil && st == dispatch.StateGone {
			return base + suffix
		}
	}
	return ""
}

// WaitReady waits up to a minute for an agent that is already present to become idle.
func (t Terminal) WaitReady(ctx context.Context, name string) bool {
	const wait = time.Minute
	_, err := run(ctx, wait+command.ReadLimit, "agent", "wait", name,
		"--until", string(dispatch.StateIdle), "--until", string(dispatch.StateDone),
		"--timeout", millis(wait))
	return err == nil
}

// Prompt submits the prompt and returns once Herdr sees the agent start on it: working, or blocked
// (on a permission dialog, say). It does not wait for the turn to end; the caller watches the agent
// from there. Herdr fails the call (agent_prompt_stalled) if neither state shows within 5 seconds
// of submitting, as when the paste is lost or Enter doesn't register; the 30 seconds are a limit
// on Herdr itself.
func (t Terminal) Prompt(ctx context.Context, name, prompt string) error {
	const wait = 30 * time.Second
	_, err := run(ctx, wait+command.ReadLimit, "agent", "prompt", name, prompt, "--wait",
		"--until", string(dispatch.StateWorking), "--until", string(dispatch.StateBlocked), "--timeout", millis(wait))
	return err
}

// SendKeys presses keys in the agent's terminal.
func (t Terminal) SendKeys(ctx context.Context, name string, keys ...string) error {
	_, err := run(ctx, command.ReadLimit, append([]string{"agent", "send-keys", name}, keys...)...)
	return err
}

// WaitStarted waits up to 20 seconds for an agent to start working (or block).
func (t Terminal) WaitStarted(ctx context.Context, name string) bool {
	const wait = 20 * time.Second
	_, err := run(ctx, wait+command.ReadLimit, "agent", "wait", name,
		"--until", string(dispatch.StateWorking), "--until", string(dispatch.StateBlocked),
		"--timeout", millis(wait))
	return err == nil
}

// Status returns the agent's state, StateGone if Herdr has no such agent. If Herdr cannot be asked
// it returns the error and no state: the agent may well be there.
func (t Terminal) Status(ctx context.Context, name string) (dispatch.AgentState, error) {
	_, _, st, err := readAgent(run(ctx, command.ReadLimit, "agent", "get", name))
	return st, err
}

// Screen returns the end of the agent's terminal, given its state as just read ("" if not known).
// Herdr can capture scrollback only while the agent is idle, so for a working or blocked agent this
// reads the visible screen at once, and otherwise falls back to it when scrollback fails.
func (t Terminal) Screen(ctx context.Context, name string, state dispatch.AgentState) string {
	if state != dispatch.StateWorking && state != dispatch.StateBlocked {
		out, err := run(ctx, command.ReadLimit,
			"agent", "read", name, "--source", "recent-unwrapped", "--lines", "60")
		if err == nil {
			return out
		}
	}
	out, _ := run(ctx, command.ReadLimit, "agent", "read", name, "--source", "visible") // "" when Herdr can't read it
	return out
}

// CurrentWorkspace returns the Herdr workspace orchestra runs in: from HERDR_WORKSPACE_ID, which
// Herdr sets in its panes, or else from Herdr itself. "" if neither knows.
func CurrentWorkspace(ctx context.Context, getenv func(string) string) string {
	if ws := getenv("HERDR_WORKSPACE_ID"); ws != "" {
		return ws
	}
	if getenv("HERDR_ENV") != "1" {
		return ""
	}
	out, err := run(ctx, command.ReadLimit, "pane", "current", "--current")
	if err != nil {
		return ""
	}
	var r struct {
		Result struct {
			Pane struct {
				WorkspaceID string `json:"workspace_id"`
			} `json:"pane"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &r) != nil {
		return ""
	}
	return r.Result.Pane.WorkspaceID
}
