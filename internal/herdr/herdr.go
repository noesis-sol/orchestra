// Package herdr is orchestra's adapter for Herdr: the tabs workers run in, starting and naming
// agents, reading their status and screen, and the workspace orchestra runs in.
package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// ---- Beads ---------------------------------------------------------------------------

// ---- Herdr ---------------------------------------------------------------------------

func CreateTab(workspace, cwd, label string) (tab, pane string, err error) {
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
func CloseTab(tab string) { command.Output("", "herdr", "tab", "close", tab) }

// StartAgent starts an agent in the pane. A non-empty prompt is passed to the agent itself, so it
// starts with the prompt already submitted. Herdr types the command into the pane's shell and
// refuses arguments with line breaks, so the prompt must be one line.
func StartAgent(ctx context.Context, name, kind, pane, prompt string) error {
	args := []string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", "60000"}
	if prompt != "" {
		args = append(args, "--", prompt)
	}
	_, err := command.OutputContext(ctx, "", "herdr", args...)
	return err
}

// IsArgumentRefused reports Herdr refusing agent arguments it cannot pass through the shell; a
// retry cannot succeed.
func IsArgumentRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_agent_argument")
}

// LaunchInPane types '<kind> <instruction>' into the pane's shell, as 'herdr agent start' would,
// and returns at once. The instruction must be one line.
func LaunchInPane(pane, kind, instruction string) error {
	_, err := command.Output("", "herdr", "pane", "run", pane, kind+" "+shellQuote(instruction))
	return err
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// AdoptAgent waits up to a minute for Herdr to recognise an agent of kind in the pane, names it
// name, and returns its status.
func AdoptAgent(ctx context.Context, pane, kind, name string) (string, bool) {
	deadline := time.Now().Add(time.Minute)
	for {
		if n, k, st := PaneAgent(pane); st != "gone" && k == kind {
			if n != name && RenameAgent(pane, name) != nil {
				return st, false
			}
			return st, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		select {
		case <-ctx.Done():
			return "", false
		case <-time.After(time.Second):
		}
	}
}

// PaneAgent returns the agent in a pane: its name ("" if Herdr gave it none), kind and status, with
// status "gone" if the pane holds no agent.
func PaneAgent(pane string) (name, kind, status string) {
	out, err := command.Output("", "herdr", "agent", "get", pane)
	if err != nil {
		return "", "", "gone"
	}
	return parsePaneAgent([]byte(out))
}

func parsePaneAgent(raw []byte) (name, kind, status string) {
	var r struct {
		Result struct {
			Agent struct {
				Name        *string `json:"name"`
				Agent       string  `json:"agent"`
				AgentStatus string  `json:"agent_status"`
			} `json:"agent"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Result.Agent.AgentStatus == "" {
		return "", "", "gone"
	}
	a := r.Result.Agent
	if a.Name != nil {
		name = *a.Name
	}
	return name, a.Agent, a.AgentStatus
}

// RenameAgent gives the agent (by name or pane) a new name.
func RenameAgent(name, to string) error {
	_, err := command.Output("", "herdr", "agent", "rename", name, to)
	return err
}

// FreeName returns an unused agent name for an earlier worker of ticket id: id-1, id-2, …,
// within Herdr's 32-character limit, or "" if none is free.
func FreeName(id string) string {
	for n := 1; n <= 20; n++ {
		suffix := fmt.Sprintf("-%d", n)
		base := id
		if len(base)+len(suffix) > 32 {
			base = base[:32-len(suffix)]
		}
		if Status(base+suffix) == "gone" {
			return base + suffix
		}
	}
	return ""
}

// WaitReady waits up to a minute for an agent that is already present to become idle.
func WaitReady(ctx context.Context, name string) bool {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "wait", name, "--until", "idle", "--until", "done", "--timeout", "60000")
	return err == nil
}

// Prompt submits the prompt and waits (up to 10 minutes) for the agent to first settle.
func Prompt(ctx context.Context, name, prompt string) error {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "prompt", name, prompt, "--wait", "--timeout", "600000")
	return err
}

// SendKeys presses keys in the agent's terminal.
func SendKeys(name string, keys ...string) error {
	_, err := command.Output("", "herdr", append([]string{"agent", "send-keys", name}, keys...)...)
	return err
}

// WaitStarted waits up to 20 seconds for an agent to start working (or block).
func WaitStarted(ctx context.Context, name string) bool {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "wait", name, "--until", "working", "--until", "blocked", "--timeout", "20000")
	return err == nil
}

// Status returns idle, working, blocked, done or unknown, or "gone" if the agent cannot be read.
func Status(name string) string {
	out, err := command.Output("", "herdr", "agent", "get", name)
	if err != nil {
		return "gone"
	}
	var r struct {
		Result struct {
			Agent struct {
				AgentStatus string `json:"agent_status"`
			} `json:"agent"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(out), &r) != nil || r.Result.Agent.AgentStatus == "" {
		return "gone"
	}
	return r.Result.Agent.AgentStatus
}

// Screen returns the end of the agent's terminal. Herdr can capture scrollback only while
// the agent is idle, so while it works this falls back to the visible screen.
func Screen(name string) string {
	out, err := command.Output("", "herdr", "agent", "read", name, "--source", "recent-unwrapped", "--lines", "60")
	if err != nil {
		out, _ = command.Output("", "herdr", "agent", "read", name, "--source", "visible")
	}
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

// ---- Git -----------------------------------------------------------------------------
