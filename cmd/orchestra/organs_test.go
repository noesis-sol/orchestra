package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// fakeClaude writes a stand-in for the claude CLI that records its arguments, working directory
// and stdin, then prints the given JSON.
func fakeClaude(t *testing.T, output string) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	bin = filepath.Join(dir, "claude")
	script := "#!/bin/sh\n" +
		"{ pwd; for a in \"$@\"; do printf '[%s]\\n' \"$a\"; done; echo '--- stdin'; cat; } > " + record + "\n" +
		"cat <<'JSON'\n" + output + "\nJSON\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

func TestOrganCallsAreReadOnlyAndSmall(t *testing.T) {
	bin, record := fakeClaude(t, `{"type":"result","is_error":false,"result":"ok"}`)
	r, err := organ{bin: bin, model: "opus"}.ask(context.Background(), time.Minute, "SYSTEM", "EVIDENCE", `{"type":"object"}`)
	if err != nil || r.Result != "ok" {
		t.Fatalf("ask: %v %+v", err, r)
	}
	b, _ := os.ReadFile(record)
	got := string(b)
	for _, want := range []string{
		"[-p]", "[--tools]\n[]\n", // every built-in tool disabled
		"[--strict-mcp-config]", // no MCP servers
		"[--no-session-persistence]", "[--system-prompt]\n[SYSTEM]", "[--output-format]\n[json]",
		"[--json-schema]", "[--model]\n[opus]", "--- stdin\nEVIDENCE",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("claude was not called with %q:\n%s", want, got)
		}
	}
	wd, _ := os.Getwd()
	if strings.HasPrefix(got, wd) {
		t.Errorf("organ ran inside the project (%s); it should run outside it", wd)
	}
	for _, forbidden := range []string{"Bash", "Edit", "Write", "--dangerously", "bypassPermissions"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("organ call mentions %q", forbidden)
		}
	}
}

func TestOrganErrorsAreReported(t *testing.T) {
	bin, _ := fakeClaude(t, `{"type":"result","is_error":true,"result":"usage limit reached"}`)
	if _, err := (organ{bin: bin}).ask(context.Background(), time.Minute, "s", "i", ""); err == nil || !strings.Contains(err.Error(), "usage limit") {
		t.Errorf("want the CLI's error, got %v", err)
	}
	bin, _ = fakeClaude(t, `not json`)
	if _, err := (organ{bin: bin}).ask(context.Background(), time.Minute, "s", "i", ""); err == nil {
		t.Error("garbage output should be an error")
	}
}

func TestParseTriage(t *testing.T) {
	good := organResult{Structured: []byte(`{"cause":"environment","confidence":"high","summary":"visionOS runtime missing.","recommendation":"Run xcodebuild -downloadPlatform visionOS."}`)}
	tr, err := parseTriage(good)
	if err != nil || tr.Cause != "environment" {
		t.Fatalf("%v %+v", err, tr)
	}
	if n := tr.note(); n != "Triage (orchestra): cause = environment (high confidence). visionOS runtime missing. Recommendation: Run xcodebuild -downloadPlatform visionOS." {
		t.Errorf("note = %q", n)
	}
	// Older CLIs return the JSON as the result text.
	if tr, err := parseTriage(organResult{Result: `{"cause":"problem","confidence":"low","summary":"s","recommendation":"r"}`}); err != nil || tr.Cause != "problem" {
		t.Errorf("result fallback: %v %+v", err, tr)
	}
	if _, err := parseTriage(organResult{Structured: []byte(`{"cause":"weather"}`)}); err == nil {
		t.Error("an unknown cause should be rejected")
	}
}

func TestTriageInputCarriesTheEvidence(t *testing.T) {
	in := triageInput(deferral{ID: "k-1", Title: "Support visionOS", How: "the worker deferred it",
		Ticket: "k-1 · Support visionOS", Screen: "⏺ The visionOS runtime is not installed.", Worktree: ""})
	for _, want := range []string{"k-1 (Support visionOS) was set aside: the worker deferred it",
		"## Ticket (bd show)", "## End of the worker's terminal", "visionOS runtime is not installed", "## Worktree state\n\n(none)"} {
		if !strings.Contains(in, want) {
			t.Errorf("triage input lacks %q:\n%s", want, in)
		}
	}
}

func TestSetAsideKeepsOrderWithoutRepeats(t *testing.T) {
	o := &Orch{}
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

func TestTriageLineIsPurpleDiamondWithCause(t *testing.T) {
	at := time.Date(2026, 9, 28, 17, 0, 0, 0, time.Local)
	line := ansi.Strip(renderEvent(Event{Time: at, Kind: EvTriage, Ticket: "kinieta-jqm", Detail: "environment · high", Title: "visionOS runtime missing"}))
	if line != "17:00:00 ◆ kinieta-jqm triage: environment · high  visionOS runtime missing" {
		t.Errorf("line = %q", line)
	}
}

// TestLiveOrgans calls the real claude on a real repository without writing anything:
//
//	ORGAN_LIVE=1 LIVE_REPO=~/Projects/kinieta LIVE_BASE=<branch> LIVE_START=<commit> \
//	LIVE_TICKET=<deferred id> LIVE_WT=<its worktree> go test -run TestLiveOrgans -v
func TestLiveOrgans(t *testing.T) {
	if os.Getenv("ORGAN_LIVE") != "1" {
		t.Skip("set ORGAN_LIVE=1 to call the real claude")
	}
	repo, id := os.Getenv("LIVE_REPO"), os.Getenv("LIVE_TICKET")
	o := &Orch{cfg: Config{Repo: repo, Base: os.Getenv("LIVE_BASE")}, organ: organ{bin: "claude"},
		startHead: os.Getenv("LIVE_START"), started: time.Now().Add(-time.Hour), log: &Logger{}}
	b, _ := os.ReadFile(filepath.Join(repo, ".claude", "orchestrate.log"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], " START orchestra") {
			o.log.lines = lines[i:]
			break
		}
	}

	d := o.gatherDeferral(id, "", "the worker deferred it", os.Getenv("LIVE_WT"))
	start := time.Now()
	r, err := o.organ.ask(context.Background(), 3*time.Minute, triageSystem, triageInput(d), triageSchema)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := parseTriage(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("triage (%s):\n%s", time.Since(start).Round(time.Second), tr.note())

	o.markAside(id)
	start = time.Now()
	r, err = o.organ.ask(context.Background(), 5*time.Minute, reviewSystem, o.reviewInput(exitOK, "(live test: the run is still going)"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report (%s):\n%s", time.Since(start).Round(time.Second), r.Result)
}

func TestShortArgsCutsLongArguments(t *testing.T) {
	got := shortArgs([]string{"agent", "start", strings.Repeat("x", 100) + "\nmore"})
	if len([]rune(got)) > 80 || strings.Contains(got, "\n") {
		t.Errorf("got %q", got)
	}
}
