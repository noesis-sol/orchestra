package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// ---- Beads ---------------------------------------------------------------------------

// ---- Herdr ---------------------------------------------------------------------------

func tabCreate(workspace, cwd, label string) (tab, pane string, err error) {
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

// tabClose closes a Herdr tab; a variable so tests can replace it.
var tabClose = func(tab string) { command.Output("", "herdr", "tab", "close", tab) }

// agentStart starts an agent in the pane. A non-empty prompt is passed to the agent itself, so it
// starts with the prompt already submitted. Herdr types the command into the pane's shell and
// refuses arguments with line breaks, so the prompt must be one line.
func agentStart(ctx context.Context, name, kind, pane, prompt string) error {
	args := []string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", "60000"}
	if prompt != "" {
		args = append(args, "--", prompt)
	}
	_, err := command.OutputContext(ctx, "", "herdr", args...)
	return err
}

// isArgumentRefused reports Herdr refusing agent arguments it cannot pass through the shell; a
// retry cannot succeed.
func isArgumentRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_agent_argument")
}

// writeLaunchPrompt puts the worker prompt in the worktree at .orchestra/run/prompt.md, which
// ensureRunExcluded keeps out of git, and returns the one-line instruction to start the worker with.
func writeLaunchPrompt(wt, ticket, prompt string) (string, error) {
	dir := filepath.Join(wt, orchDir, runName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Your instructions for ticket %s are in %s/%s/prompt.md in this directory. Read that file and follow it exactly.", ticket, orchDir, runName), nil
}

// paneLaunch types '<kind> <instruction>' into the pane's shell, as 'herdr agent start' would,
// and returns at once. The instruction must be one line.
func paneLaunch(pane, kind, instruction string) error {
	_, err := command.Output("", "herdr", "pane", "run", pane, kind+" "+shellQuote(instruction))
	return err
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// adoptPaneAgent waits up to a minute for Herdr to recognise an agent of kind in the pane, names it
// name, and returns its status.
func adoptPaneAgent(ctx context.Context, pane, kind, name string) (string, bool) {
	deadline := time.Now().Add(time.Minute)
	for {
		if n, k, st := paneAgent(pane); st != "gone" && k == kind {
			if n != name && agentRename(pane, name) != nil {
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

// paneAgent returns the agent in a pane: its name ("" if Herdr gave it none), kind and status, with
// status "gone" if the pane holds no agent.
func paneAgent(pane string) (name, kind, status string) {
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

func agentRename(name, to string) error {
	_, err := command.Output("", "herdr", "agent", "rename", name, to)
	return err
}

// freeName returns an unused agent name for an earlier worker of ticket id: id-1, id-2, …,
// within Herdr's 32-character limit, or "" if none is free.
func freeName(id string) string {
	for n := 1; n <= 20; n++ {
		suffix := fmt.Sprintf("-%d", n)
		base := id
		if len(base)+len(suffix) > 32 {
			base = base[:32-len(suffix)]
		}
		if agentStatus(base+suffix) == "gone" {
			return base + suffix
		}
	}
	return ""
}

// agentReady waits up to a minute for an agent that is already present to become idle.
func agentReady(ctx context.Context, name string) bool {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "wait", name, "--until", "idle", "--until", "done", "--timeout", "60000")
	return err == nil
}

// agentPrompt submits the prompt and waits (up to 10 minutes) for the agent to first settle.
func agentPrompt(ctx context.Context, name, prompt string) error {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "prompt", name, prompt, "--wait", "--timeout", "600000")
	return err
}

func agentSendKeys(name string, keys ...string) error {
	_, err := command.Output("", "herdr", append([]string{"agent", "send-keys", name}, keys...)...)
	return err
}

// agentWaitStarted waits up to 20 seconds for an agent to start working (or block).
func agentWaitStarted(ctx context.Context, name string) bool {
	_, err := command.OutputContext(ctx, "", "herdr", "agent", "wait", name, "--until", "working", "--until", "blocked", "--timeout", "20000")
	return err == nil
}

// inputHolds reports whether the agent's input box still holds the prompt, unsent. The box runs
// from the last line starting with ❯ to the rule below it; a long paste shows only its last lines
// there, or a "[Pasted text …]" placeholder, so any substantial line of the prompt counts.
func inputHolds(screen, prompt string) bool {
	lines := strings.Split(screen, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "❯") {
			start = i
		}
	}
	if start < 0 {
		return false
	}
	var box []string
	for i, l := range lines[start:] {
		t := strings.TrimSpace(l)
		if i > 0 && strings.HasPrefix(t, "─") {
			break
		}
		box = append(box, t)
	}
	box[0] = strings.TrimSpace(strings.TrimPrefix(box[0], "❯"))
	text := strings.Join(box, " ")
	if strings.Contains(text, "[Pasted text") {
		return true
	}
	for _, l := range strings.Split(prompt, "\n") {
		r := []rune(strings.TrimSpace(l))
		if len(r) < 20 {
			continue // too short to tell apart from anything else on screen
		}
		if len(r) > 40 {
			r = r[:40]
		}
		if strings.Contains(text, string(r)) {
			return true
		}
	}
	return false
}

// agentStatus returns idle, working, blocked, done or unknown, or "gone" if the agent cannot be read.
func agentStatus(name string) string {
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

// agentScreen returns the end of the agent's terminal. Herdr can capture scrollback only while
// the agent is idle, so while it works this falls back to the visible screen.
func agentScreen(name string) string {
	out, err := command.Output("", "herdr", "agent", "read", name, "--source", "recent-unwrapped", "--lines", "60")
	if err != nil {
		out, _ = command.Output("", "herdr", "agent", "read", name, "--source", "visible")
	}
	return out
}

// Claude Code marks tool calls with ⏺ and its working spinner with one of these glyphs.
var activityMarks = []string{"⏺", "✻", "✶", "✳", "✢", "✽"}

// lastActivity returns the worker's most recent action or spinner line, skipping the input box
// and status bar at the bottom of its screen.
func lastActivity(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		for _, m := range activityMarks {
			if strings.HasPrefix(l, m) {
				return l
			}
		}
	}
	return ""
}

// currentWorkspace returns the Herdr workspace orchestra runs in: from HERDR_WORKSPACE_ID, which
// Herdr sets in its panes, or else from Herdr itself. "" if neither knows.
func currentWorkspace(getenv func(string) string) string {
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
