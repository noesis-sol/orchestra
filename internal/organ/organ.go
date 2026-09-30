// Package organ holds orchestra's organs: LLM-powered steps that run 'claude -p' with no tools and
// no MCP servers on evidence orchestra gathers, and only advise.
package organ

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Organs are LLM-powered steps. The orchestrator gathers the evidence itself and hands it to
// 'claude -p' with every tool and MCP server disabled, so an organ can only read what it is
// given and answer. Organs advise: the orchestrator writes their output down, and no organ
// changes a ticket's status.
//
//   triage    each deferred ticket: is the cause the environment, the instructions or the
//             problem itself? The recommendation is appended to the ticket's notes.
//   reviewer  when the loop stops, for any reason: a short report of what finished, what was
//             set aside and why, and what needs the maintainer.

type Client struct {
	Bin   string // "claude"; tests substitute a fake
	Model string // "" uses the claude CLI's default
}

type Result struct {
	IsError    bool            `json:"is_error"`
	Result     string          `json:"result"`
	Structured json.RawMessage `json:"structured_output"`
}

// args keeps an organ read-only and small: no built-in tools, no MCP servers (their tool lists
// alone are ~180k tokens), a short system prompt in place of Claude Code's, and no saved session.
func (g Client) args(system, schema string) []string {
	a := []string{"-p", "--tools", "", "--strict-mcp-config", "--no-session-persistence",
		"--system-prompt", system, "--output-format", "json"}
	if schema != "" {
		a = append(a, "--json-schema", schema)
	}
	if g.Model != "" {
		a = append(a, "--model", g.Model)
	}
	return a
}

func (g Client) Ask(ctx context.Context, timeout time.Duration, system, input, schema string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.Bin, g.args(system, schema)...)
	cmd.Dir = os.TempDir() // outside the project: no CLAUDE.md, project settings or hooks
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("%s: %w: %s", g.Bin, err, strings.TrimSpace(stderr.String()))
	}
	var r Result
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return r, fmt.Errorf("unreadable %s output: %w", g.Bin, err)
	}
	if r.IsError {
		return r, fmt.Errorf("%s reported an error: %s", g.Bin, r.Result)
	}
	return r, nil
}

// Section formats one piece of evidence for an organ's input.
func Section(title, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "(none)"
	}
	return "## " + title + "\n\n" + body + "\n\n"
}

// ---- Triage --------------------------------------------------------------------------

const triageSystem = `You triage tickets that an automated coding pipeline set aside. An orchestrator hands each Beads ticket to a coding agent (a "worker") in its own git worktree; when the worker cannot finish, the ticket is deferred. Decide where the cause lies:

- environment: the machine, tools or services the worker ran on. Examples: a missing SDK, simulator or platform; a permission prompt or safety check that failed or refused commands; network, credentials, flaky infrastructure.
- instructions: the worker prompt or the ticket's wording. Examples: unclear or contradictory acceptance criteria, missing information, a decision only a human can make, a rule that forced deferral (such as "awaits CI").
- problem: the task itself. Examples: too large for one ticket, blocked on a design question or on other work, failing tests the worker could not fix.

Use only the evidence given. Recommend the single most useful next step for the maintainer, concretely (a command to run, a question to answer, how to split or reword the ticket). summary is at most 15 words. recommendation is at most 3 sentences.`

const triageSchema = `{"type":"object","properties":{"cause":{"type":"string","enum":["environment","instructions","problem"]},"confidence":{"type":"string","enum":["high","medium","low"]},"summary":{"type":"string"},"recommendation":{"type":"string"}},"required":["cause","confidence","summary","recommendation"]}`

type Verdict struct {
	Cause          string `json:"cause"`
	Confidence     string `json:"confidence"`
	Summary        string `json:"summary"`
	Recommendation string `json:"recommendation"`
}

// Deferral is the evidence gathered when a ticket is set aside, while its tab is still open.
type Deferral struct {
	ID, Title, How string // How: who deferred it and why, as logged
	Ticket         string // bd show
	Screen         string // the end of the worker's terminal
	Worktree       string // status, commits and diff stat
}

func triageInput(d Deferral) string {
	return "Ticket " + d.ID + " (" + d.Title + ") was set aside: " + d.How + "\n\n" +
		Section("Ticket (bd show)", d.Ticket) +
		Section("End of the worker's terminal", d.Screen) +
		Section("Worktree state", d.Worktree)
}

func parseTriage(r Result) (Verdict, error) {
	var t Verdict
	raw := r.Structured
	if len(raw) == 0 {
		raw = json.RawMessage(r.Result) // older CLIs put the JSON in result
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return t, fmt.Errorf("unreadable triage: %w", err)
	}
	switch t.Cause {
	case "environment", "instructions", "problem":
	default:
		return t, fmt.Errorf("unknown triage cause %q", t.Cause)
	}
	return t, nil
}

// note is what goes into the ticket: advice only, the status is untouched.
func (t Verdict) Note() string {
	return fmt.Sprintf("Triage (orchestra): cause = %s (%s confidence). %s Recommendation: %s",
		t.Cause, t.Confidence, strings.TrimSpace(t.Summary), strings.TrimSpace(t.Recommendation))
}

// ---- Reviewer ------------------------------------------------------------------------

const reviewSystem = `You write the end-of-run report for an automated coding pipeline. An orchestrator hands Beads tickets to coding agents ("workers") one at a time, each in its own git worktree and Herdr tab, and merges finished tickets into one branch. The maintainer reads your report when they come back.

Use only the evidence given; never invent tickets, commits or causes. Write Markdown, at most 20 lines in all:

- First, one sentence: how the run ended and why.
- ## Finished: one bullet per ticket merged in this run: the ID, what changed in a few words, the commit hash.
- ## Set aside: one bullet per ticket deferred or left unmerged: the ID, why, and the triage cause when a triage note gives one.
- ## Needs you: concrete actions for the maintainer, most urgent first: questions to answer (a ticket waiting on a question labelled "human" is answered with: bd human respond <question id> --response "…"; it then returns to the queue by itself), a worker waiting in a tab (name the tab), an environment fix, whatever stopped the run.

Each bullet is one line: no nested bullets, no sub-lists, no bold labels. Write "Nothing." under a section with no entries. No preamble and no closing remarks.`

// Unavailable explains why the organs can't run, or returns "".
func Unavailable(bin string) string {
	if _, err := exec.LookPath(bin); err != nil {
		return bin + " not found"
	}
	return ""
}

// Triage asks where the cause of a deferral lies: the environment, the instructions or the problem.
func (g Client) Triage(ctx context.Context, d Deferral) (Verdict, error) {
	r, err := g.Ask(ctx, 3*time.Minute, triageSystem, triageInput(d), triageSchema)
	if err != nil {
		return Verdict{}, err
	}
	return parseTriage(r)
}

// Review writes the end-of-run report from the evidence.
func (g Client) Review(ctx context.Context, evidence string) (string, error) {
	r, err := g.Ask(ctx, 5*time.Minute, reviewSystem, evidence, "")
	if err != nil {
		return "", err
	}
	return r.Result, nil
}
