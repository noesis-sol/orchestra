package organ

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	r, err := Client{Bin: bin, Model: "opus"}.Ask(context.Background(), time.Minute, "SYSTEM", "EVIDENCE", `{"type":"object"}`)
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
	if _, err := (Client{Bin: bin}).Ask(context.Background(), time.Minute, "s", "i", ""); err == nil || !strings.Contains(err.Error(), "usage limit") {
		t.Errorf("want the CLI's error, got %v", err)
	}
	bin, _ = fakeClaude(t, `not json`)
	if _, err := (Client{Bin: bin}).Ask(context.Background(), time.Minute, "s", "i", ""); err == nil {
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
	in := triageInput(Deferral{ID: "k-1", Title: "Support visionOS", How: "the worker deferred it",
		Ticket: "k-1 · Support visionOS", Screen: "⏺ The visionOS runtime is not installed.", Worktree: ""})
	for _, want := range []string{"k-1 (Support visionOS) was set aside: the worker deferred it",
		"## Ticket (bd show)", "## End of the worker's terminal", "visionOS runtime is not installed", "## Worktree state\n\n(none)"} {
		if !strings.Contains(in, want) {
			t.Errorf("triage input lacks %q:\n%s", want, in)
		}
	}
}
