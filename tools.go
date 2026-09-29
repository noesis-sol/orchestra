package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, shortArgs(args), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// shortArgs renders arguments for an error message, cutting long ones (a whole prompt, say).
func shortArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		a = strings.ReplaceAll(a, "\n", " ")
		if r := []rune(a); len(r) > 60 {
			a = string(r[:60]) + "…"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}

// ---- Beads ---------------------------------------------------------------------------

type Ticket struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Priority     *int     `json:"priority"`
	Labels       []string `json:"labels"`
	Dependencies []Ticket `json:"dependencies"` // from bd show; each carries its status and labels
}

// humanLabel marks a question for the maintainer (bd human list / respond). Workers ask one as
// its own ticket that blocks theirs; the orchestrator never dispatches it.
const humanLabel = "human"

func hasLabel(t Ticket, label string) bool {
	for _, l := range t.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// openQuestion returns the unanswered question the ticket waits on, if any.
func openQuestion(t Ticket) *Ticket {
	for i, d := range t.Dependencies {
		if d.Status != "closed" && hasLabel(d, humanLabel) {
			return &t.Dependencies[i]
		}
	}
	return nil
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
		if t.Status == "open" && !hasLabel(t, humanLabel) { // questions are for the maintainer
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

// parseTicket reads one ticket from 'bd show --json'; ok is false if it cannot be read.
func parseTicket(raw []byte) (Ticket, bool) {
	data := unwrap(raw)
	var list []Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0], true
		}
		return Ticket{}, false
	}
	var t Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t, true
	}
	return Ticket{}, false
}

// ticketInfo returns the ticket with its dependencies; Status is "unknown" if it cannot be read.
func ticketInfo(repo, id string) Ticket {
	out, _ := run(repo, "bd", "show", id, "--json")
	t, ok := parseTicket([]byte(out))
	if !ok {
		return Ticket{ID: id, Status: "unknown"}
	}
	return t
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

// agentStart starts an agent in the pane. A non-empty prompt is passed to the agent itself, so it
// starts with the prompt already submitted. Herdr types the command into the pane's shell and
// refuses arguments with line breaks, so the prompt must be one line.
func agentStart(ctx context.Context, name, kind, pane, prompt string) error {
	args := []string{"agent", "start", name, "--kind", kind, "--pane", pane, "--timeout", "60000"}
	if prompt != "" {
		args = append(args, "--", prompt)
	}
	_, err := runCtx(ctx, "", "herdr", args...)
	return err
}

// isArgumentRefused reports Herdr refusing agent arguments it cannot pass through the shell; a
// retry cannot succeed.
func isArgumentRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "invalid_agent_argument")
}

// writeLaunchPrompt puts the worker prompt in the worktree at .orchestra/prompt.md, kept out of git
// through the repository's info/exclude (shared by all worktrees, never committed), and returns
// the one-line instruction to start the worker with.
func writeLaunchPrompt(repo, wt, ticket, prompt string) (string, error) {
	common, err := run(repo, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	exclude := filepath.Join(strings.TrimSpace(common), "info", "exclude")
	b, _ := os.ReadFile(exclude)
	if !strings.Contains(string(b), launchDir+"/") {
		if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
			return "", err
		}
		f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return "", err
		}
		prefix := ""
		if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
			prefix = "\n"
		}
		fmt.Fprintf(f, "%s# worker prompts written by orchestra\n/%s/\n", prefix, launchDir)
		f.Close()
	}
	dir := filepath.Join(wt, launchDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Your instructions for ticket %s are in %s/prompt.md in this directory. Read that file and follow it exactly.", ticket, launchDir), nil
}

const launchDir = ".orchestra"

func agentRename(name, to string) error {
	_, err := run("", "herdr", "agent", "rename", name, to)
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
	_, err := runCtx(ctx, "", "herdr", "agent", "wait", name, "--until", "idle", "--until", "done", "--timeout", "60000")
	return err == nil
}

// agentPrompt submits the prompt and waits (up to 10 minutes) for the agent to first settle.
func agentPrompt(ctx context.Context, name, prompt string) error {
	_, err := runCtx(ctx, "", "herdr", "agent", "prompt", name, prompt, "--wait", "--timeout", "600000")
	return err
}

func agentSendKeys(name string, keys ...string) error {
	_, err := run("", "herdr", append([]string{"agent", "send-keys", name}, keys...)...)
	return err
}

// agentWaitStarted waits up to 20 seconds for an agent to start working (or block).
func agentWaitStarted(ctx context.Context, name string) bool {
	_, err := runCtx(ctx, "", "herdr", "agent", "wait", name, "--until", "working", "--until", "blocked", "--timeout", "20000")
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

// agentScreen returns the end of the agent's terminal. Herdr can capture scrollback only while
// the agent is idle, so while it works this falls back to the visible screen.
func agentScreen(name string) string {
	out, err := run("", "herdr", "agent", "read", name, "--source", "recent-unwrapped", "--lines", "60")
	if err != nil {
		out, _ = run("", "herdr", "agent", "read", name, "--source", "visible")
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
