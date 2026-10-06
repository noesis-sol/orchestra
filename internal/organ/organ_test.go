package organ

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// fakeClaude writes a stand-in for the claude CLI that records its arguments, working directory
// and stdin, then prints the given JSON.
func fakeClaude(t *testing.T, output string) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	script := "#!/bin/sh\n" +
		"{ pwd; for a in \"$@\"; do printf '[%s]\\n' \"$a\"; done; echo '--- stdin'; cat; } > " + record + "\n" +
		"cat <<'JSON'\n" + output + "\nJSON\n"
	return faketool.Write(t, dir, "claude", script), record
}

func TestOrganCallsAreReadOnlyAndSmall(t *testing.T) {
	bin, record := fakeClaude(t, `{"type":"result","is_error":false,"result":"ok"}`)
	r, err := Client{Bin: bin, Model: "opus"}.Ask(context.Background(), "test", time.Minute, "low", "SYSTEM", "EVIDENCE", `{"type":"object"}`)
	if err != nil || r.Result != "ok" {
		t.Fatalf("ask: %v %+v", err, r)
	}
	b, _ := os.ReadFile(record)
	got := string(b)
	for _, want := range []string{
		"[-p]", "[--tools]\n[]\n", // every built-in tool disabled
		"[--strict-mcp-config]", // no MCP servers
		"[--no-session-persistence]", "[--system-prompt]\n[SYSTEM]", "[--output-format]\n[json]",
		"[--json-schema]", "[--model]\n[opus]", "[--effort]\n[low]", "--- stdin\nEVIDENCE",
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
	if _, err := (Client{Bin: bin}).Ask(context.Background(), "test", time.Minute, "low", "s", "i", ""); err == nil || !strings.Contains(err.Error(), "usage limit") {
		t.Errorf("want the CLI's error, got %v", err)
	}
	bin, _ = fakeClaude(t, `not json`)
	if _, err := (Client{Bin: bin}).Ask(context.Background(), "test", time.Minute, "low", "s", "i", ""); err == nil {
		t.Error("garbage output should be an error")
	}
}

func TestParseTriage(t *testing.T) {
	good := Result{Structured: []byte(`{"cause":"environment","confidence":"high","summary":"visionOS runtime missing.","recommendation":"Run xcodebuild -downloadPlatform visionOS."}`)}
	tr, err := parseTriage(good)
	if err != nil || tr.Cause != "environment" {
		t.Fatalf("%v %+v", err, tr)
	}
	if n := tr.Note(); n != "Triage (orchestra): cause = environment (high confidence). visionOS runtime missing. Recommendation: Run xcodebuild -downloadPlatform visionOS." {
		t.Errorf("note = %q", n)
	}
	// Older CLIs return the JSON as the result text.
	if tr, err := parseTriage(Result{Result: `{"cause":"problem","confidence":"low","summary":"s","recommendation":"r"}`}); err != nil || tr.Cause != "problem" {
		t.Errorf("result fallback: %v %+v", err, tr)
	}
	if _, err := parseTriage(Result{Structured: []byte(`{"cause":"weather"}`)}); err == nil {
		t.Error("an unknown cause should be rejected")
	}
}

func TestTriageInputCarriesTheEvidence(t *testing.T) {
	in := triageInput(Deferral{ID: "k-1", How: "the worker deferred it",
		Ticket: "k-1 · Support visionOS", Screen: "⏺ The visionOS runtime is not installed.", Worktree: ""})
	for _, want := range []string{"Ticket k-1 was set aside: the worker deferred it\n\n## Ticket (bd show)",
		"\">\nk-1 · Support visionOS\n</evidence id=\"", "## End of the worker's terminal", "visionOS runtime is not installed", "## Worktree state\n\n<evidence id=\"",
		"\n(none)\n</evidence id=\""} {
		if !strings.Contains(in, want) {
			t.Errorf("triage input lacks %q:\n%s", want, in)
		}
	}
}

func TestParsePredictionKeepsRepositoryFiles(t *testing.T) {
	tracked := []string{"a.go", "internal/b.go", "c.go"}
	r := Result{Structured: []byte(`{"files":["./internal/b.go","nowhere.go","a.go","internal/b.go"]}`)}
	if got, err := parsePrediction(r, tracked); err != nil || strings.Join(got, ",") != "internal/b.go,a.go" {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := parsePrediction(Result{Result: `{"files":["c.go"]}`}, tracked); err != nil || strings.Join(got, ",") != "c.go" {
		t.Errorf("result fallback: %v, %v", got, err)
	}
	if got, err := parsePrediction(Result{Structured: []byte(`{"files":[]}`)}, tracked); err != nil || got == nil || len(got) != 0 {
		t.Errorf("no clue: %v, %v", got, err)
	}
	if _, err := parsePrediction(Result{Result: "a.go"}, tracked); err == nil {
		t.Error("unreadable output should be an error")
	}
	var many []string
	for i := range 20 {
		many = append(many, fmt.Sprintf("f%d.go", i))
	}
	all, _ := json.Marshal(map[string][]string{"files": many})
	if got, _ := parsePrediction(Result{Structured: all}, many); len(got) != MaxPredicted {
		t.Errorf("kept %d files, want %d", len(got), MaxPredicted)
	}
}

func TestPredictFilesAsksWithTheTicketAndTheFiles(t *testing.T) {
	bin, record := fakeClaude(t, `{"type":"result","is_error":false,"structured_output":{"files":["internal/b.go"]}}`)
	got, err := Client{Bin: bin}.PredictFiles(context.Background(), Footprint{ID: "k-2",
		Ticket: "k-2 · Faster picks", Files: []string{"a.go", "internal/b.go"}})
	if err != nil || strings.Join(got, ",") != "internal/b.go" {
		t.Fatalf("got %v, %v", got, err)
	}
	b, _ := os.ReadFile(record)
	for _, want := range []string{"Predict the files ticket k-2 will change.\n\n## Ticket (bd show)\n\n<evidence id=\"",
		"\nk-2 · Faster picks\n</evidence id=\"", "## Repository files (git ls-files)\n\n<evidence id=\"",
		"\na.go\ninternal/b.go\n</evidence id=\"", "[--json-schema]"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("claude was not given %q:\n%s", want, b)
		}
	}
}
