package organ

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scoutRepo is a repository root for the scout, with scripts/check.sh when check is true.
func scoutRepo(t *testing.T, check bool) string {
	t.Helper()
	root := t.TempDir()
	if check {
		if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "scripts", "check.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// scoutFailureOf is the kind of err, a *ScoutError, or fails the test.
func scoutFailureOf(t *testing.T, err error) ScoutFailure {
	t.Helper()
	e, ok := errors.AsType[*ScoutError](err)
	if !ok {
		t.Fatalf("error %v (%T), want a *ScoutError", err, err)
	}
	return e.Failure
}

const scoutFound = `{"type":"result","is_error":false,"structured_output":{"suites":[` +
	`{"name":" unit tests ","kind":"unit","command":"npm test","found_in":"package.json:7","tier":"fast",` +
	`"parallel_safe":true,"needs":[]},` +
	`{"name":"e2e","kind":"e2e","command":"npx playwright test","found_in":"playwright.config.ts:3",` +
	`"tier":"full","parallel_safe":false,"needs":["a browser"," ","postgres"]},` +
	`{"name":"check","kind":"other","command":"./scripts/check.sh","found_in":"Makefile:2","tier":"full",` +
	`"parallel_safe":true,"needs":[]}],"note":""}}`

func TestScoutFindsSuitesWithTheCheckScriptFirst(t *testing.T) {
	bin, record := fakeClaude(t, scoutFound)
	root := scoutRepo(t, true)
	s, err := Client{Bin: bin}.Scout(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Suites) != 3 {
		t.Fatalf("got %d suites, want 3: %+v", len(s.Suites), s.Suites)
	}
	if c := s.Suites[0]; c.Command != "./scripts/check.sh" || c.Tier != TierFast || c.FoundIn != "Makefile:2" {
		t.Errorf("first suite %+v, want scripts/check.sh as a fast suite", c)
	}
	if u := s.Suites[1]; u.Name != "unit tests" || u.Kind != SuiteUnit || u.Command != "npm test" ||
		u.FoundIn != "package.json:7" || u.Tier != TierFast || !u.ParallelSafe || u.Needs == nil || len(u.Needs) != 0 {
		t.Errorf("unit suite %+v", u)
	}
	if e := s.Suites[2]; e.Kind != SuiteE2E || e.Tier != TierFull || e.ParallelSafe ||
		strings.Join(e.Needs, ",") != "a browser,postgres" {
		t.Errorf("e2e suite %+v", e)
	}
	b, _ := os.ReadFile(record)
	got := string(b)
	wd, _ := filepath.EvalSymlinks(root)
	if dir, _, _ := strings.Cut(got, "\n"); dir != wd {
		t.Errorf("the scout ran in %s, want the repository's root %s", dir, wd)
	}
	for _, want := range []string{"[--effort]\n[medium]\n", "[--json-schema]", "[--no-session-persistence]",
		"--- stdin\n" + scoutInput} {
		if !strings.Contains(got, want) {
			t.Errorf("claude was not called with %q:\n%s", want, got)
		}
	}
}

// The scout may read and search, and do nothing else: no other tool and no MCP server.
func TestScoutHasOnlyReadOnlyTools(t *testing.T) {
	bin, record := fakeClaude(t, `{"is_error":false,"structured_output":{"suites":[],"note":"n"}}`)
	if _, err := (Client{Bin: bin}).Scout(context.Background(), scoutRepo(t, false)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(record)
	got := string(b)
	if !strings.Contains(got, "\n[--tools]\n[Read,Glob,Grep]\n") || strings.Count(got, "[--tools]") != 1 {
		t.Errorf("want --tools Read,Glob,Grep alone:\n%s", got)
	}
	if !strings.Contains(got, "\n[--strict-mcp-config]\n") || strings.Contains(got, "--mcp-config") {
		t.Errorf("want --strict-mcp-config with no --mcp-config, for no MCP servers:\n%s", got)
	}
	args, _, _ := strings.Cut(got, "[--system-prompt]") // the prompt itself names tools it hasn't
	for _, forbidden := range []string{"Bash", "Edit", "Write", "--allowed", "--add-dir", "--dangerously",
		"--permission-mode", "bypassPermissions"} {
		if strings.Contains(args, forbidden) {
			t.Errorf("the scout's call mentions %q:\n%s", forbidden, args)
		}
	}
}

func TestScoutFindsNoSuites(t *testing.T) {
	bin, _ := fakeClaude(t, `{"is_error":false,"structured_output":{"suites":[],`+
		`"note":" Looked at package.json, the Makefile and .github/workflows: none runs tests. "}}`)
	s, err := Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, false))
	if err != nil {
		t.Fatal(err)
	}
	if s.Suites == nil || len(s.Suites) != 0 ||
		s.Note != "Looked at package.json, the Makefile and .github/workflows: none runs tests." {
		t.Errorf("got %+v, want an empty list and the note", s)
	}
	// Older CLIs return the JSON as the result text.
	if s, err := parseScouting(Result{Result: `{"suites":null,"note":"Nothing."}`}); err != nil ||
		s.Suites == nil || len(s.Suites) != 0 {
		t.Errorf("result fallback: %+v, %v", s, err)
	}
}

// An existing scripts/check.sh the scout left out is added, first and fast; without one, nothing is.
func TestScoutAddsTheCheckScriptItLeftOut(t *testing.T) {
	out := `{"is_error":false,"structured_output":{"suites":[{"name":"vet","kind":"lint","command":"go vet ./...",` +
		`"found_in":"Makefile:4","tier":"fast","parallel_safe":true,"needs":[]}],"note":""}}`
	bin, _ := fakeClaude(t, out)
	s, err := Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Suites) != 2 || s.Suites[0].Command != "scripts/check.sh" || s.Suites[0].Tier != TierFast ||
		s.Suites[0].FoundIn != "scripts/check.sh:1" || s.Suites[1].Command != "go vet ./..." {
		t.Errorf("got %+v, want scripts/check.sh first, then vet", s.Suites)
	}
	s, err = Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, false))
	if err != nil || len(s.Suites) != 1 {
		t.Errorf("without scripts/check.sh: %+v, %v", s.Suites, err)
	}
}

func TestRunsCheckScript(t *testing.T) {
	for command, want := range map[string]bool{
		"scripts/check.sh": true, "./scripts/check.sh": true, "sh scripts/check.sh": true,
		"bash ./scripts/check.sh -v": true, "scripts/check.sh.bak": false, "other/scripts/check.sh": false,
		"npm test": false,
	} {
		if got := runsCheckScript(command); got != want {
			t.Errorf("runsCheckScript(%q) = %v, want %v", command, got, want)
		}
	}
}

func TestScoutStopsAtItsTimeLimit(t *testing.T) {
	bin, dir := fakeScript(t, "[ \"$1\" = --help ] && exit 0\necho $$ > \"$(dirname \"$0\")/pid\"\nexec sleep 600\n")
	t.Cleanup(func() { killFake(dir) })
	_, err := Client{Bin: bin}.scout(context.Background(), 200*time.Millisecond, scoutRepo(t, false))
	if f := scoutFailureOf(t, err); f != ScoutTimedOut {
		t.Errorf("failure %v, want ScoutTimedOut", f)
	}
	if err.Error() != "the scout timed out after 200ms" {
		t.Errorf("error %q, want it to name the time limit", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("errors.Is(%v, context.DeadlineExceeded) = false", err)
	}
	if ScoutLimit != 5*time.Minute {
		t.Errorf("ScoutLimit = %v, want 5m", ScoutLimit)
	}
}

func TestScoutSaysWhyItWasStopped(t *testing.T) {
	bin, dir := fakeScript(t, "[ \"$1\" = --help ] && exit 0\necho $$ > \"$(dirname \"$0\")/pid\"\nexec sleep 600\n")
	t.Cleanup(func() { killFake(dir) })
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(errors.New("skipped with Esc")) })
	_, err := Client{Bin: bin}.Scout(ctx, scoutRepo(t, false))
	if f := scoutFailureOf(t, err); f != ScoutStopped || err.Error() != "the scout was stopped: skipped with Esc" {
		t.Errorf("got %v (%v), want ScoutStopped with the cause", f, err)
	}
}

func TestScoutRejectsAnAnswerThatIsntTheJSON(t *testing.T) {
	for name, output := range map[string]string{
		"not json":     `not json`,
		"prose":        `{"is_error":false,"result":"I found npm test."}`,
		"unknown kind": `{"is_error":false,"structured_output":{"suites":[{"name":"t","kind":"smoke","command":"c","tier":"fast"}],"note":""}}`,
		"unknown tier": `{"is_error":false,"structured_output":{"suites":[{"name":"t","kind":"unit","command":"c","tier":"slow"}],"note":""}}`,
		"no command":   `{"is_error":false,"structured_output":{"suites":[{"name":"t","kind":"unit","command":" ","tier":"fast"}],"note":""}}`,
		"no name":      `{"is_error":false,"structured_output":{"suites":[{"kind":"unit","command":"c","tier":"fast"}],"note":""}}`,
	} {
		bin, _ := fakeClaude(t, output)
		_, err := Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, false))
		if f := scoutFailureOf(t, err); f != ScoutUnreadable || !strings.HasPrefix(err.Error(), "the scout's answer is unreadable: ") {
			t.Errorf("%s: got %v (%v), want ScoutUnreadable", name, f, err)
		}
	}
}

func TestScoutReportsClaudesFailures(t *testing.T) {
	bin, _ := fakeClaude(t, `{"is_error":true,"result":"usage limit reached"}`)
	_, err := Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, false))
	if f := scoutFailureOf(t, err); f != ScoutFailed || err.Error() != "the scout failed: "+bin+" reported an error: usage limit reached" {
		t.Errorf("got %v (%v), want ScoutFailed with claude's error", f, err)
	}
	missing := filepath.Join(t.TempDir(), "claude")
	_, err = Client{Bin: missing}.Scout(context.Background(), scoutRepo(t, false))
	if f := scoutFailureOf(t, err); f != ScoutNoClaude || err.Error() != "the scout can't run: "+missing+" not found" {
		t.Errorf("got %v (%v), want ScoutNoClaude", f, err)
	}
}

func TestScoutEffortIsOverridable(t *testing.T) {
	bin, record := fakeClaude(t, `{"is_error":false,"structured_output":{"suites":[],"note":"n"}}`)
	if _, err := (Client{Bin: bin, Effort: "high"}).Scout(context.Background(), scoutRepo(t, false)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), "[--effort]\n[high]\n") {
		t.Errorf("want --effort high:\n%s", b)
	}
}
