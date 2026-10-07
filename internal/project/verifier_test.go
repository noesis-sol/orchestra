package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// verifierRepo is a project as init leaves it before ApplyVerifier: the worker prompt from the
// template, and runners for fast and full.
func verifierRepo(t *testing.T, fast, full []Suite) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := []byte(fillTemplate(promptTemplate, FastRunner))
	if err := os.WriteFile(filepath.Join(repo, Dir, promptName), prompt, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyRunners(repo, Choice{Fast: &fast, Full: &full}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func readRepoFile(t *testing.T, repo, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheVerifierKnowsTheStackAndTheChecks(t *testing.T) {
	repo := verifierRepo(t, []Suite{{Name: "check", Command: "scripts/check.sh"}},
		[]Suite{{Name: "e2e", Command: "npm run e2e", Serial: true}})
	c := Choice{Verifier: true, Agent: "claude", Stack: []string{"Go 1.26", "golangci-lint"}}
	s, ok, err := ApplyVerifier(repo, c)
	if err != nil || !ok || s.Kind != StepDone || len(s.Commit) != 1 || s.Commit[0] != VerifierPath {
		t.Fatalf("step %+v, %v, %v", s, ok, err)
	}
	agent := readRepoFile(t, repo, VerifierPath)
	for _, want := range []string{
		"---\nname: verifier\n", "tools: Read, Grep, Glob, Bash\n",
		"- Go 1.26\n- golangci-lint\n",
		"runs:\n  - `scripts/check.sh`\n", "then:\n  - `npm run e2e`\n",
		"Start your answer with PASS or FAIL",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("the verifier lacks %q:\n%s", want, agent)
		}
	}
	if strings.Contains(agent, "lock ") || strings.Contains(agent, "exit 0") {
		t.Errorf("the verifier lists the runners' plumbing:\n%s", agent)
	}
	prompt := readRepoFile(t, repo, Dir+"/"+promptName)
	if !strings.HasSuffix(prompt, verifierSection) || !strings.Contains(prompt, "the ticket's ID, TICKET_ID,") {
		t.Errorf("the worker prompt doesn't end with the Verify section:\n%s", prompt)
	}
}

// Run again, init neither writes over the verifier there nor adds the section twice.
func TestTheVerifierIsAddedOnce(t *testing.T) {
	repo := verifierRepo(t, []Suite{}, []Suite{})
	c := Choice{Verifier: true}
	if _, _, err := ApplyVerifier(repo, c); err != nil {
		t.Fatal(err)
	}
	if OffersVerifier(repo, "claude") {
		t.Error("init offers the verifier the project has")
	}
	p := filepath.Join(repo, filepath.FromSlash(VerifierPath))
	if err := os.WriteFile(p, []byte("the project's own\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _, err := ApplyVerifier(repo, c)
	if err != nil || s.Kind != StepKept || len(s.Commit) != 0 {
		t.Fatalf("again: %+v, %v", s, err)
	}
	if got := readRepoFile(t, repo, VerifierPath); got != "the project's own\n" {
		t.Errorf("the verifier was replaced: %q", got)
	}
	if n := strings.Count(readRepoFile(t, repo, Dir+"/"+promptName), "\n## Verify\n"); n != 1 {
		t.Errorf("the Verify section is there %d times", n)
	}
}

func TestTheVerifierWithNothingToGoOn(t *testing.T) {
	agent := VerifierText(nil, nil, nil)
	for _, want := range []string{"scout didn't describe the stack", "runs:\n  - nothing yet.\n",
		"then:\n  - nothing more.\n"} {
		if !strings.Contains(agent, want) {
			t.Errorf("lacks %q:\n%s", want, agent)
		}
	}
}

func TestTheVerifierDeclinedOrUnasked(t *testing.T) {
	repo := verifierRepo(t, []Suite{}, []Suite{})
	if _, ok, err := ApplyVerifier(repo, Choice{}); ok || err != nil {
		t.Errorf("declined: ok %v, %v", ok, err)
	}
	if s, ok, _ := ApplyVerifier(repo, Choice{VerifierUnasked: true}); !ok || !strings.Contains(s.Detail, "--verifier") {
		t.Errorf("unasked: %+v", s)
	}
	if s, _, _ := ApplyVerifier(repo, Choice{Verifier: true, Agent: "codex"}); s.Kind != StepCaution {
		t.Errorf("codex: %+v", s)
	}
	if fileExists(filepath.Join(repo, filepath.FromSlash(VerifierPath))) {
		t.Error("wrote the verifier")
	}
	if OffersVerifier(repo, "codex") || !OffersVerifier(repo, "claude") {
		t.Error("offers it to the wrong agents")
	}
}
