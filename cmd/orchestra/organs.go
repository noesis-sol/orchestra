package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/herdr"
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

type organ struct {
	bin   string // "claude"; tests substitute a fake
	model string // "" uses the claude CLI's default
}

type organResult struct {
	IsError    bool            `json:"is_error"`
	Result     string          `json:"result"`
	Structured json.RawMessage `json:"structured_output"`
}

// args keeps an organ read-only and small: no built-in tools, no MCP servers (their tool lists
// alone are ~180k tokens), a short system prompt in place of Claude Code's, and no saved session.
func (g organ) args(system, schema string) []string {
	a := []string{"-p", "--tools", "", "--strict-mcp-config", "--no-session-persistence",
		"--system-prompt", system, "--output-format", "json"}
	if schema != "" {
		a = append(a, "--json-schema", schema)
	}
	if g.model != "" {
		a = append(a, "--model", g.model)
	}
	return a
}

func (g organ) ask(ctx context.Context, timeout time.Duration, system, input, schema string) (organResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.bin, g.args(system, schema)...)
	cmd.Dir = os.TempDir() // outside the project: no CLAUDE.md, project settings or hooks
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return organResult{}, fmt.Errorf("%s: %w: %s", g.bin, err, strings.TrimSpace(stderr.String()))
	}
	var r organResult
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return r, fmt.Errorf("unreadable %s output: %w", g.bin, err)
	}
	if r.IsError {
		return r, fmt.Errorf("%s reported an error: %s", g.bin, r.Result)
	}
	return r, nil
}

// section formats one piece of evidence for an organ's input.
func section(title, body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		body = "(none)"
	}
	return "## " + title + "\n\n" + body + "\n\n"
}

// lastLines keeps the end of a long text, where a worker's conclusion is.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ---- Triage --------------------------------------------------------------------------

const triageSystem = `You triage tickets that an automated coding pipeline set aside. An orchestrator hands each Beads ticket to a coding agent (a "worker") in its own git worktree; when the worker cannot finish, the ticket is deferred. Decide where the cause lies:

- environment: the machine, tools or services the worker ran on. Examples: a missing SDK, simulator or platform; a permission prompt or safety check that failed or refused commands; network, credentials, flaky infrastructure.
- instructions: the worker prompt or the ticket's wording. Examples: unclear or contradictory acceptance criteria, missing information, a decision only a human can make, a rule that forced deferral (such as "awaits CI").
- problem: the task itself. Examples: too large for one ticket, blocked on a design question or on other work, failing tests the worker could not fix.

Use only the evidence given. Recommend the single most useful next step for the maintainer, concretely (a command to run, a question to answer, how to split or reword the ticket). summary is at most 15 words. recommendation is at most 3 sentences.`

const triageSchema = `{"type":"object","properties":{"cause":{"type":"string","enum":["environment","instructions","problem"]},"confidence":{"type":"string","enum":["high","medium","low"]},"summary":{"type":"string"},"recommendation":{"type":"string"}},"required":["cause","confidence","summary","recommendation"]}`

type Triage struct {
	Cause          string `json:"cause"`
	Confidence     string `json:"confidence"`
	Summary        string `json:"summary"`
	Recommendation string `json:"recommendation"`
}

// deferral is the evidence gathered when a ticket is set aside, while its tab is still open.
type deferral struct {
	ID, Title, How string // How: who deferred it and why, as logged
	Ticket         string // bd show
	Screen         string // the end of the worker's terminal
	Worktree       string // status, commits and diff stat
}

func triageInput(d deferral) string {
	return "Ticket " + d.ID + " (" + d.Title + ") was set aside: " + d.How + "\n\n" +
		section("Ticket (bd show)", d.Ticket) +
		section("End of the worker's terminal", d.Screen) +
		section("Worktree state", d.Worktree)
}

func parseTriage(r organResult) (Triage, error) {
	var t Triage
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
func (t Triage) note() string {
	return fmt.Sprintf("Triage (orchestra): cause = %s (%s confidence). %s Recommendation: %s",
		t.Cause, t.Confidence, strings.TrimSpace(t.Summary), strings.TrimSpace(t.Recommendation))
}

// gatherDeferral collects the evidence for one deferred ticket.
func (o *Orch) gatherDeferral(id, title, how, wt string) deferral {
	c := o.cfg
	show, _ := command.Output(c.Repo, "bd", "show", id)
	status, _ := command.Output("", "git", "-C", wt, "status", "--short")
	commits, _ := command.Output("", "git", "-C", wt, "log", "--oneline", c.Base+"..HEAD")
	stat, _ := command.Output("", "git", "-C", wt, "diff", "--stat", "HEAD")
	return deferral{ID: id, Title: title, How: how, Ticket: show,
		Screen: lastLines(herdr.Screen(id), 80),
		Worktree: "Uncommitted changes:\n" + orNone(status) + "\n\nCommits on the ticket branch:\n" +
			orNone(commits) + "\n\nDiff against its last commit:\n" + orNone(stat)}
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return strings.TrimSpace(s)
}

// startTriage runs triage in the background, one ticket at a time, so the loop never waits for it.
func (o *Orch) startTriage() {
	o.triageQ = make(chan deferral, 64)
	o.triageDone = make(chan struct{})
	go func() {
		defer close(o.triageDone)
		for d := range o.triageQ {
			o.triage(d)
		}
	}()
}

func (o *Orch) queueTriage(d deferral) {
	if o.triageQ != nil {
		o.triageQ <- d
	}
}

// finishTriage waits for queued triage to finish, or for ctx to be cancelled.
func (o *Orch) finishTriage(ctx context.Context) {
	if o.triageQ == nil {
		return
	}
	close(o.triageQ)
	select {
	case <-o.triageDone:
	case <-ctx.Done():
	}
}

func (o *Orch) triage(d deferral) {
	r, err := o.organ.ask(o.organCtx, 3*time.Minute, triageSystem, triageInput(d), triageSchema)
	var t Triage
	if err == nil {
		t, err = parseTriage(r)
	}
	if err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: d.ID, Text: fmt.Sprintf("  TRIAGE_FAILED for %s: %v", d.ID, firstLine(err.Error()))})
		return
	}
	beads.AppendNotes(o.cfg.Repo, d.ID, t.note())
	o.emit(Event{Kind: EvTriage, Ticket: d.ID, Title: t.Summary, Detail: t.Cause + " · " + t.Confidence, Text: fmt.Sprintf(
		"  triage %s: %s (%s confidence) - %s", d.ID, t.Cause, t.Confidence, t.Summary)})
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// ---- Reviewer ------------------------------------------------------------------------

const reviewSystem = `You write the end-of-run report for an automated coding pipeline. An orchestrator hands Beads tickets to coding agents ("workers") one at a time, each in its own git worktree and Herdr tab, and merges finished tickets into one branch. The maintainer reads your report when they come back.

Use only the evidence given; never invent tickets, commits or causes. Write Markdown, at most 20 lines in all:

- First, one sentence: how the run ended and why.
- ## Finished: one bullet per ticket merged in this run: the ID, what changed in a few words, the commit hash.
- ## Set aside: one bullet per ticket deferred or left unmerged: the ID, why, and the triage cause when a triage note gives one.
- ## Needs you: concrete actions for the maintainer, most urgent first: questions to answer (a ticket waiting on a question labelled "human" is answered with: bd human respond <question id> --response "…"; it then returns to the queue by itself), a worker waiting in a tab (name the tab), an environment fix, whatever stopped the run.

Each bullet is one line: no nested bullets, no sub-lists, no bold labels. Write "Nothing." under a section with no entries. No preamble and no closing remarks.`

// reviewInput gathers the evidence for the reviewer.
func (o *Orch) reviewInput(code int, final string) string {
	c := o.cfg
	commits, _ := command.Output(c.Repo, "git", "log", "--format=%h %s", o.startHead+".."+c.Base)
	var setAside strings.Builder
	for _, id := range o.setAside() {
		show, _ := command.Output(c.Repo, "bd", "show", id)
		setAside.WriteString(show + "\n")
	}
	var stopped strings.Builder
	for _, st := range o.activeList() {
		show, _ := command.Output(c.Repo, "bd", "show", st.Ticket)
		fmt.Fprintf(&stopped, "%s was in progress in Herdr tab %s when the run stopped.\n\n%s\n\nEnd of its worker's terminal:\n%s\n\n",
			st.Ticket, st.Tab, show, lastLines(herdr.Screen(st.Ticket), 60))
	}
	ready, _ := beads.Ready(c.Repo)
	return fmt.Sprintf("Run on branch %s of %s, from %s to %s. Exit code %d (%s). Final line: %s\n\n",
		c.Base, c.Repo, o.started.Format("15:04"), time.Now().Format("15:04"), code, exitMeaning(code), final) +
		section("Orchestrator log for this run", strings.Join(o.log.RunLines(), "\n")) +
		section("Commits merged into "+c.Base+" in this run", commits) +
		section("Tickets set aside in this run (bd show, including triage notes)", setAside.String()) +
		section("Tickets in progress when the run stopped", stopped.String()) +
		section("Tickets still ready", fmt.Sprintf("%d", len(ready)))
}

// review writes the run report and returns it with the path it was saved to.
func (o *Orch) review(ctx context.Context, code int, final string) (string, string, error) {
	r, err := o.organ.ask(ctx, 5*time.Minute, reviewSystem, o.reviewInput(code, final), "")
	if err != nil {
		return "", "", err
	}
	report := fmt.Sprintf("# Orchestra run · %s %s–%s · %s\n\n%s\n", o.started.Format("2006-01-02"),
		o.started.Format("15:04"), time.Now().Format("15:04"), o.cfg.Base, strings.TrimSpace(r.Result))
	dir := o.cfg.ReportsDir
	path := filepath.Join(dir, o.started.Format("2006-01-02-150405")+".md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return report, "", err
	}
	return report, path, os.WriteFile(path, []byte(report), 0o644)
}

func exitMeaning(code int) string {
	switch code {
	case exitOK:
		return "the queue was empty or the limit was reached"
	case exitStuck:
		return "a worker was blocked or paused and needs an answer"
	case exitTool:
		return "a Herdr, Beads or git command failed"
	case exitDirty:
		return "the main checkout had uncommitted changes or left its branch"
	case exitMerge:
		return "a finished ticket's branch did not fast-forward"
	case exitInterrupted:
		return "stopped with Ctrl+C"
	}
	return "unknown"
}

// setAside lists tickets deferred or left unmerged in this run, in order, without repeats.
func (o *Orch) setAside() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.asideIDs...)
}

func (o *Orch) markAside(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, x := range o.asideIDs {
		if x == id {
			return
		}
	}
	o.asideIDs = append(o.asideIDs, id)
}

// organsOff explains why organs cannot run, or returns "".
func organsOff(bin string) string {
	if _, err := exec.LookPath(bin); err != nil {
		return bin + " not found"
	}
	return ""
}
