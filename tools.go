package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// run executes a command and returns its stdout. The error carries stderr, so callers can log it.
func run(dir, name string, args ...string) (string, error) {
	return runCtx(context.Background(), dir, name, args...)
}

func runCtx(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BD_JSON_ENVELOPE=0") // pin the bd --json shape
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// ---- Beads ---------------------------------------------------------------------------

type Ticket struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority *int   `json:"priority"`
}

// unwrap accepts either bd JSON shape: the bare payload, or the v2 envelope {schema_version, data}.
func unwrap(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Data != nil {
			return env.Data
		}
	}
	return raw
}

// parseReady returns the open tickets from 'bd ready --json', highest priority (lowest number) first.
func parseReady(raw []byte) ([]Ticket, error) {
	var all []Ticket
	if err := json.Unmarshal(unwrap(raw), &all); err != nil {
		return nil, err
	}
	var open []Ticket
	for _, t := range all {
		if t.Status == "open" {
			open = append(open, t)
		}
	}
	prio := func(t Ticket) int {
		if t.Priority == nil {
			return 9
		}
		return *t.Priority
	}
	sort.SliceStable(open, func(i, j int) bool { return prio(open[i]) < prio(open[j]) })
	return open, nil
}

// parseStatus returns the status from 'bd show --json', or "unknown" if it cannot be read.
func parseStatus(raw []byte) string {
	data := unwrap(raw)
	var list []Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0].Status
		}
		return "unknown"
	}
	var t Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t.Status
	}
	return "unknown"
}

func readyTickets(repo string) ([]Ticket, error) {
	out, _ := run(repo, "bd", "ready", "--json") // like the bash version, judge by the output
	return parseReady([]byte(out))
}

func ticketStatus(repo, id string) string {
	out, _ := run(repo, "bd", "show", id, "--json")
	return parseStatus([]byte(out))
}

func appendNotes(repo, id, note string) {
	run(repo, "bd", "update", id, "--append-notes", note)
}

func deferTicket(repo, id, reason string) {
	run(repo, "bd", "defer", id, "--reason="+reason)
}

// ---- Herdr ---------------------------------------------------------------------------

func tabCreate(workspace, cwd, label string) (tab, pane string, err error) {
	out, err := run("", "herdr", "tab", "create", "--workspace", workspace, "--cwd", cwd, "--label", label, "--no-focus")
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

func tabClose(tab string) { run("", "herdr", "tab", "close", tab) }

func agentStart(ctx context.Context, name, kind, pane string) error {
	_, err := runCtx(ctx, "", "herdr", "agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", "60000")
	return err
}

// agentReady waits up to a minute for an agent that is already present to become idle.
func agentReady(ctx context.Context, name string) bool {
	_, err := runCtx(ctx, "", "herdr", "agent", "wait", name, "--until", "idle", "--until", "done", "--timeout", "60000")
	return err == nil
}

// agentPrompt submits the prompt and waits (up to 10 minutes) for the agent to first settle.
func agentPrompt(ctx context.Context, name, prompt string) error {
	_, err := runCtx(ctx, "", "herdr", "agent", "prompt", name, prompt, "--wait", "--timeout", "600000")
	return err
}

// agentStatus returns idle, working, blocked, done or unknown, or "gone" if the agent cannot be read.
func agentStatus(name string) string {
	out, err := run("", "herdr", "agent", "get", name)
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

func agentScreen(name string) string {
	out, _ := run("", "herdr", "agent", "read", name, "--source", "recent-unwrapped", "--lines", "60")
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

// ---- Git -----------------------------------------------------------------------------

// dirtyTree lists uncommitted work in checkout dir outside .claude/ and .beads/ (those hold the
// prompts, log, local agent settings and tracker data). A failed git call counts as dirty.
func dirtyTree(dir string) string {
	out, err := run("", "git", "-C", dir, "status", "--porcelain", "--", ".", ":(exclude).claude", ":(exclude).beads")
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(out)
}

func currentBranch(repo string) string {
	out, _ := run(repo, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	return strings.TrimSpace(out)
}

// parseWorktreeOf returns the path of the worktree that has branch checked out, from
// 'git worktree list --porcelain'.
func parseWorktreeOf(porcelain, branch string) string {
	path := ""
	for _, line := range strings.Split(porcelain, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == "branch refs/heads/"+branch {
			return path
		}
	}
	return ""
}

func worktreeOf(repo, branch string) string {
	out, _ := run(repo, "git", "worktree", "list", "--porcelain")
	return parseWorktreeOf(out, branch)
}

func hasBranch(repo, branch string) bool {
	_, err := run(repo, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// commitNaming returns the latest commit on branch (not on base) whose message names the ticket,
// as "<hash> <subject>" cut to 70 characters, or "".
func commitNaming(repo, base, branch, ticket string) string {
	out, _ := run(repo, "git", "log", "--oneline", "-1", "--grep="+ticket, base+".."+branch)
	c := strings.TrimSpace(out)
	if r := []rune(c); len(r) > 70 {
		c = string(r[:70])
	}
	return c
}
