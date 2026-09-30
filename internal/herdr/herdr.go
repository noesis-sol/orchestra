// Package herdr is orchestra's adapter for Herdr: the tabs workers run in, starting and naming
// agents, reading their status and screen, and the workspace orchestra runs in.
package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Terminal is Herdr as orchestra uses it: tabs for workers, starting and naming agents, and reading
// their status and screen.
type Terminal struct{}

// CreateTab opens a tab labelled label in workspace, starting in cwd, without switching to it, and
// returns the tab's ID and its first pane's.
func (t Terminal) CreateTab(workspace, cwd, label string) (tab, pane string, err error) {
	out, err := command.Output("", "herdr", "tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label, "--no-focus")
	if err != nil {
		return "", "", err
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
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return "", "", err
	}
	if r.Result.Tab.TabID == "" || r.Result.RootPane.PaneID == "" {
		return "", "", fmt.Errorf("unexpected 'herdr tab create' output: %s", out)
	}
	return r.Result.Tab.TabID, r.Result.RootPane.PaneID, nil
}

// CloseTab closes a Herdr tab.
func (t Terminal) CloseTab(tab string) { command.Output("", "herdr", "tab", "close", tab) }

// StartAgent starts an agent in the pane, passing it args; a prompt among them starts it with the
// prompt already submitted. Herdr types the command into the pane's shell and refuses arguments
// with line breaks, so each must be one line.
func (t Terminal) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	a := []string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", "60000"}
	if len(args) > 0 {
		a = append(append(a, "--"), args...)
	}
	_, err := command.OutputContext(ctx, "", "herdr", a...)
	return err
}

// IsArgumentRefused reports Herdr refusing agent arguments it cannot pass through the shell; a
// retry cannot succeed.
func (t Terminal) IsArgumentRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_agent_argument")
}

// IsNameRefused reports Herdr refusing an agent name; a retry under the same name cannot succeed.
func (t Terminal) IsNameRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_agent_name")
}

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
func (t Terminal) LaunchInPane(pane, kind string, args []string) error {
	line := kind
	for _, a := range args {
		line += " " + command.ShellQuote(a)
	}
	_, err := command.Output("", "herdr", "pane", "run", pane, line)
	return err
}

// AdoptAgent waits up to a minute for Herdr to recognise an agent of kind in the pane, names it
// name, and returns its status. The error says why it could not, such as Herdr refusing the name.
func (t Terminal) AdoptAgent(ctx context.Context, pane, kind, name string) (string, error) {
	deadline := time.Now().Add(time.Minute)
	for {
		if n, k, st := t.PaneAgent(pane); st != "gone" && k == kind {
			if n != name {
				if err := t.RenameAgent(pane, name); err != nil {
					return st, err
				}
			}
			return st, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("no %s agent appeared in pane %s within a minute", kind, pane)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// PaneAgent returns the agent in a pane: its name ("" if Herdr gave it none), kind and status, with
// status "gone" if the pane holds no agent and "unreadable" if Herdr could not be asked.
func (t Terminal) PaneAgent(pane string) (name, kind, status string) {
	name, kind, status, _ = readAgent(command.Output("", "herdr", "agent", "get", pane))
	return name, kind, status
}

// readAgent reads the output of 'herdr agent get': the agent's name, kind and status. Herdr answers
// a missing agent with agent_not_found on stderr (and fails), which is "gone"; any other failure
// (Herdr busy or restarting, say) says nothing about the agent, so it is "unreadable" with the
// error.
func readAgent(out string, err error) (name, kind, status string, _ error) {
	if err != nil && strings.Contains(err.Error(), `"code":"agent_not_found"`) { // stderr is in err
		return "", "", "gone", nil
	}
	var r struct {
		Result struct {
			Agent struct {
				Name        *string `json:"name"`
				Agent       string  `json:"agent"`
				AgentStatus string  `json:"agent_status"`
			} `json:"agent"`
		} `json:"result"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	jerr := json.Unmarshal([]byte(out), &r)
	switch {
	case jerr == nil && r.Error.Code == "agent_not_found":
		return "", "", "gone", nil
	case err != nil:
		return "", "", "unreadable", err
	case jerr != nil:
		return "", "", "unreadable", fmt.Errorf("unexpected 'herdr agent get' output: %s", out)
	case r.Result.Agent.AgentStatus == "":
		return "", "", "gone", nil
	}
	a := r.Result.Agent
	if a.Name != nil {
		name = *a.Name
	}
	return name, a.Agent, a.AgentStatus, nil
}

// RenameAgent gives the agent (by name or pane) a new name.
func (t Terminal) RenameAgent(name, to string) error {
	_, err := command.Output("", "herdr", "agent", "rename", name, to)
	return err
}

// FreeName returns an unused agent name for an earlier worker that holds name: name-1, name-2, …,
// within Herdr's 32-character limit, or "" if none is free.
func (t Terminal) FreeName(name string) string {
	for n := 1; n <= 20; n++ {
		suffix := fmt.Sprintf("-%d", n)
		base := name
		if len(base)+len(suffix) > maxName {
			base = base[:maxName-len(suffix)]
		}
		if st, _ := t.Status(base + suffix); st == "gone" { // not "unreadable": that name may be taken
			return base + suffix
		}
	}
	return ""
}

// WaitReady waits up to a minute for an agent that is already present to become idle.
func (t Terminal) WaitReady(ctx context.Context, name string) bool {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "wait", name, "--until", "idle", "--until", "done", "--timeout", "60000")
	return err == nil
}

// Prompt submits the prompt and waits (up to 10 minutes) for the agent to first settle.
func (t Terminal) Prompt(ctx context.Context, name, prompt string) error {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "prompt", name, prompt, "--wait", "--timeout", "600000")
	return err
}

// SendKeys presses keys in the agent's terminal.
func (t Terminal) SendKeys(name string, keys ...string) error {
	_, err := command.Output("", "herdr", append([]string{"agent", "send-keys", name}, keys...)...)
	return err
}

// WaitStarted waits up to 20 seconds for an agent to start working (or block).
func (t Terminal) WaitStarted(ctx context.Context, name string) bool {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "wait", name, "--until", "working", "--until", "blocked", "--timeout", "20000")
	return err == nil
}

// Status returns idle, working, blocked, done or unknown, or "gone" if Herdr has no such agent. If
// Herdr cannot be asked it returns "unreadable" and the error: the agent may well be there.
func (t Terminal) Status(name string) (string, error) {
	_, _, st, err := readAgent(command.Output("", "herdr", "agent", "get", name))
	return st, err
}

// Screen returns the end of the agent's terminal, given its status as just read ("" if not known).
// Herdr can capture scrollback only while the agent is idle, so for a working or blocked agent this
// reads the visible screen at once, and otherwise falls back to it when scrollback fails.
func (t Terminal) Screen(name, status string) string {
	if status != "working" && status != "blocked" {
		if out, err := command.Output("", "herdr", "agent", "read", name, "--source", "recent-unwrapped", "--lines", "60"); err == nil {
			return out
		}
	}
	out, _ := command.Output("", "herdr", "agent", "read", name, "--source", "visible")
	return out
}

// CurrentWorkspace returns the Herdr workspace orchestra runs in: from HERDR_WORKSPACE_ID, which
// Herdr sets in its panes, or else from Herdr itself. "" if neither knows.
func CurrentWorkspace(getenv func(string) string) string {
	if ws := getenv("HERDR_WORKSPACE_ID"); ws != "" {
		return ws
	}
	if getenv("HERDR_ENV") != "1" {
		return ""
	}
	out, err := command.Output("", "herdr", "pane", "current", "--current")
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
