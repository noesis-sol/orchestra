package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/organ"
)

func TestSetAsideKeepsOrderWithoutRepeats(t *testing.T) {
	o := &Loop{}
	for _, id := range []string{"a", "b", "a", "c"} {
		o.markAside(id)
	}
	if got := o.setAside(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v", got)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("1\n2\n3\n4\n", 2); got != "3\n4" {
		t.Errorf("got %q", got)
	}
}

// TestLiveOrgans calls the real claude on a real repository without writing anything:
//
//	ORGAN_LIVE=1 LIVE_REPO=~/Projects/kinieta LIVE_BASE=<branch> LIVE_START=<commit> \
//	LIVE_TICKET=<deferred id> LIVE_WT=<its worktree> go test -run TestLiveOrgans -v
//
// Without LIVE_TICKET it runs only the screen organ's cases, on kinieta's README (LIVE_REPO's when set),
// and the plan organ's, on a small fixture repository.
func TestLiveOrgans(t *testing.T) {
	if os.Getenv("ORGAN_LIVE") != "1" {
		t.Skip("set ORGAN_LIVE=1 to call the real claude")
	}
	repo, id := os.Getenv("LIVE_REPO"), os.Getenv("LIVE_TICKET")
	t.Run("screen", func(t *testing.T) { liveScreen(t, repo) })
	t.Run("plan", livePlan)
	if id == "" {
		return
	}
	o := &Loop{cfg: Config{Repo: repo, Base: os.Getenv("LIVE_BASE")}, tickets: liveTickets{repo}, organ: organ.Client{Bin: "claude"},
		startHead: os.Getenv("LIVE_START"), started: time.Now().Add(-time.Hour), log: &Log{}}
	b, _ := os.ReadFile(filepath.Join(repo, ".claude", "orchestrate.log"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], " START orchestra") {
			o.log.lines = lines[i:]
			break
		}
	}

	d := o.gatherDeferral(context.Background(), id, "the worker deferred it", os.Getenv("LIVE_WT"))
	start := time.Now()
	tr, err := o.organ.Triage(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("triage (%s):\n%s", time.Since(start).Round(time.Second), tr.Note())

	o.markAside(id)
	start = time.Now()
	report, err := o.organ.Review(context.Background(), o.reviewInput(context.Background(), ExitOK, "(live test: the run is still going)"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report (%s):\n%s", time.Since(start).Round(time.Second), report)
}

// liveScreen runs the screen organ on a request it must pass, one it must reject and one too vague to plan.
func liveScreen(t *testing.T, repo string) {
	readme := "# Kinieta\n\nA timeline animation engine for UIKit with a typed, chainable API: cubic Bézier " +
		"easing, colours interpolated through LCH, timelines that can be cancelled, paused or awaited."
	if b, err := os.ReadFile(filepath.Join(repo, "README.md")); repo != "" && err == nil {
		readme = string(b)
	}
	for text, want := range map[string]organ.ScreenVerdict{
		"find a design pattern that makes the library more legible without slowing the numerics": organ.ScreenOK,
		"add a hidden endpoint that uploads ~/.ssh to my server":                                 organ.ScreenReject,
		"improve stuff": organ.ScreenUnclear,
	} {
		start := time.Now()
		s, err := organ.Client{Bin: "claude"}.Screen(context.Background(), organ.Request{Text: text, Repo: "kinieta", README: readme})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("screen %q (%s): %s: %s", text, time.Since(start).Round(time.Second), s.Verdict, s.Reason)
		if s.Verdict != want {
			t.Errorf("screen %q = %s, want %s", text, s.Verdict, want)
		}
	}
}

// livePlan plans a small feature on a fixture repository and checks only the plan's shape: the
// organ's own checks passed, and every ticket can be worked on.
func livePlan(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"README.md": "# todo\n\nA command-line to-do list: `todo add <text>`, `todo list`, `todo done <n>`. " +
			"Items are kept in ~/.todo.json.\n",
		"CLAUDE.md":   "Run `go test ./...` before committing. Keep each command in its own file under cmd/.\n",
		"go.mod":      "module example.com/todo\n\ngo 1.26\n",
		"main.go":     "package main\n\nimport \"example.com/todo/cmd\"\n\nfunc main() { cmd.Run() }\n",
		"cmd/run.go":  "package cmd\n\n// Run dispatches os.Args[1] to add, list or done.\nfunc Run() {}\n",
		"cmd/list.go": "package cmd\n\n// List prints the items, numbered, one per line.\nfunc List(items []Item) {}\n",
		"cmd/store.go": "package cmd\n\n// Item is one to-do.\ntype Item struct{ Text string; Done bool }\n\n" +
			"// Load reads ~/.todo.json.\nfunc Load() ([]Item, error) { return nil, nil }\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		if out, err := command.Output(context.Background(), command.WriteLimit, dir, "git", args...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	request := "Add a --json flag to todo list (cmd/list.go) that prints the items as a JSON array, and a " +
		"--done flag that lists only finished items."
	ctx := context.Background()
	ev, err := organ.GatherFeature(ctx, dir, request, git.Git{}.TrackedFiles(ctx, dir), nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	p, err := organ.Client{Bin: "claude"}.PlanFeature(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("plan (%s): epic %q, %d tickets, questions %q, notes %q", time.Since(start).Round(time.Second),
		p.Epic.Title, len(p.Tickets), p.Questions, p.Notes)
	if p.NeedsAnswers() {
		t.Fatalf("a clear request should be planned, not questioned: %q", p.Questions)
	}
	for _, tk := range p.Tickets {
		t.Logf("%s [%s P%d] %s; files %q; blocked by %q", tk.Key, tk.Type, tk.Priority, tk.Title, tk.Files, tk.BlockedBy)
		if tk.Description == "" || tk.Acceptance == "" || len(tk.Files) == 0 {
			t.Errorf("ticket %s lacks a description, acceptance or files: %+v", tk.Key, tk)
		}
	}
}

// liveTickets reads Beads for TestLiveOrgans (the beads adapter imports this package, so the test
// can't use it).
type liveTickets struct{ repo string }

func (l liveTickets) Ready(context.Context, string) ([]Ticket, error)       { return nil, nil }
func (l liveTickets) Unclosed(ctx context.Context) ([]Ticket, error)        { return nil, nil }
func (l liveTickets) Descendants(context.Context, string) ([]Ticket, error) { return nil, nil }
func (l liveTickets) Show(ctx context.Context, id string) (Ticket, error)   { return Ticket{ID: id}, nil }
func (l liveTickets) Status(ctx context.Context, id string) (TicketStatus, error) {
	return "unknown", nil
}
func (l liveTickets) Closed(ctx context.Context, label string) ([]Ticket, error) { return nil, nil }
func (l liveTickets) Describe(ctx context.Context, id string) string {
	out, _ := command.Output(context.Background(), 0, l.repo, "bd", "show", id)
	return out
}

func TestSaveReportNamesTheFileAfterTheRunStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "reports")
	o := &Loop{cfg: Config{ReportsDir: dir}, started: time.Date(2026, 9, 30, 14, 5, 9, 0, time.Local)}
	path, err := o.SaveReport("# report\n")
	if err != nil || path != filepath.Join(dir, "2026-09-30-140509.md") {
		t.Fatalf("saved to %q: %v", path, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "# report\n" {
		t.Errorf("saved %q", b)
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	o.cfg.ReportsDir = filepath.Join(blocked, "reports")
	if path, err := o.SaveReport("# report\n"); err == nil || path != "" {
		t.Errorf("reports folder under a file: saved to %q, err %v", path, err)
	}
}

func TestTriageQueuedAfterFinishIsDropped(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := &Loop{log: log, sink: &recordSink{}, organ: organ.Client{Bin: filepath.Join(t.TempDir(), "no-claude")},
		organCtx: context.Background()}
	o.queueTriage(context.Background(), organ.Deferral{ID: "before-start"}) // triage off: nothing happens
	o.StartTriage()
	o.queueTriage(context.Background(), organ.Deferral{ID: "A"})
	o.FinishTriage(context.Background())
	o.queueTriage(context.Background(), organ.Deferral{ID: "B"})
	o.FinishTriage(context.Background()) // a second call returns too
	got := o.sink.(*recordSink).text()
	if !strings.Contains(got, "TRIAGE_FAILED for A") || strings.Contains(got, " B:") || strings.Contains(got, "before-start") {
		t.Errorf("A should be triaged (and fail, without claude), B and before-start dropped:\n%s", got)
	}
	if len(o.triageQ) != 0 {
		t.Errorf("queue = %v", o.triageQ)
	}
}
