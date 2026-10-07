package project

import (
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// A worker checks its own change with the checks before it closes its ticket, but it grades work it
// wrote. init offers a verifier: a Claude Code subagent, in VerifierPath, that checks the change
// against the ticket in a fresh context, seeing the ticket and the diff but not the reasoning behind
// them. init writes it with the stack the scout found and the suites the runners run, and appends to
// the worker prompt the section that has workers run it before they close a ticket.

// VerifierPath is the verifier subagent, from the repository's top: committed, since a ticket's
// worktree has only what is committed.
const VerifierPath = ".claude/agents/verifier.md"

//go:embed verifier.md
var verifierTemplate string

var verifierText = template.Must(template.New("verifier").Parse(verifierTemplate))

// verifierSection is what init appends to the worker prompt: TICKET_ID is filled in per ticket.
const verifierSection = "\n## Verify\n" +
	"- Before you close the ticket, once `" + FastRunner + "` passes after your last commit, have the\n" +
	"  verifier subagent (" + VerifierPath + ") check your change: give it the ticket's ID, TICKET_ID,\n" +
	"  and nothing of your reasoning. Run it in the foreground and wait for its answer.\n" +
	"- Fix each gap it reports that affects correctness or the ticket's requirements, commit, run the\n" +
	"  check again, and ask it again. The rest is optional: note on the ticket any you leave.\n" +
	"- After three rounds that still end in FAIL, note its findings on the ticket. Close the ticket only\n" +
	"  when you judge them outside the ticket's requirements; otherwise defer it, as under Close.\n"

// usesVerifier reports whether a worker prompt has workers run the verifier.
func usesVerifier(prompt string) bool { return strings.Contains(prompt, VerifierPath) }

// writesSubagents reports whether init can write subagents for the workers' agent kind: Claude Code's.
func writesSubagents(agent string) bool { return agent == "" || agent == "claude" }

// OffersVerifier reports whether init should offer the verifier: the workers are Claude Code's, and
// the project lacks the subagent or a worker prompt that runs it.
func OffersVerifier(repo, agent string) bool {
	if !writesSubagents(agent) {
		return false
	}
	prompt, _ := os.ReadFile(Locate(repo).Prompt) // no prompt yet: init writes one
	return !fileExists(filepath.Join(repo, VerifierPath)) || !usesVerifier(string(prompt))
}

// VerifierText is the verifier subagent for a project built with stack, whose runners run fast and
// full (check-full's own suites, after check-fast).
func VerifierText(stack, fast, full []string) string {
	var b strings.Builder
	// The template is fixed and its data plain strings: it can't fail.
	_ = verifierText.Execute(&b, struct {
		Stack, Fast, Full      []string
		FastRunner, FullRunner string
	}{stack, fast, full, FastRunner, FullRunner})
	return b.String()
}

// runnerSuites are the commands of the runner at path, from the repository's top, as written: for
// check-full, those after check-fast. None when it isn't there.
func runnerSuites(repo, path string) ([]string, error) {
	b, err := os.ReadFile(filepath.Join(repo, path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var commands []string
	for _, c := range RunnerCommands(string(b)) {
		if c != "exit 0" && !namesPathIn(c, FastRunner) {
			commands = append(commands, c)
		}
	}
	return commands, nil
}

// ApplyVerifier writes the verifier subagent where the choice adds it, keeping one that is there, and
// appends the worker prompt's section that runs it where the prompt lacks one. It runs after Init and
// ApplyRunners, reading the worker prompt and the runners they wrote. ok is false where init has
// nothing to say: the verifier neither chosen nor asked about.
func ApplyVerifier(repo string, c Choice) (s Step, ok bool, err error) {
	const label = "verifier"
	switch {
	case c.VerifierUnasked:
		return Step{Kind: StepKept, Label: label, Detail: "not asked (no terminal): --verifier writes " +
			VerifierPath + ", a subagent that checks each ticket's change in a fresh context"}, true, nil
	case !c.Verifier:
		return Step{}, false, nil
	case !writesSubagents(c.Agent):
		return Step{Kind: StepCaution, Label: label, Detail: "the workers' agent is " + c.Agent +
			", and orchestra writes subagents for Claude Code's only: " + VerifierPath + " isn't written"}, true, nil
	}
	var did []string
	var commit []string
	p := filepath.Join(repo, filepath.FromSlash(VerifierPath))
	if fileExists(p) {
		did = append(did, VerifierPath+" is there; left as it is")
	} else {
		fast, err := runnerSuites(repo, FastRunner)
		if err != nil {
			return Step{}, true, err
		}
		full, err := runnerSuites(repo, FullRunner)
		if err != nil {
			return Step{}, true, err
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Step{}, true, err
		}
		if err := writeFile(p, []byte(VerifierText(c.Stack, fast, full)), 0o644); err != nil {
			return Step{}, true, err
		}
		did = append(did, "wrote "+VerifierPath)
		commit = append(commit, VerifierPath)
	}
	prompt := filepath.Join(repo, Dir, promptName)
	text, err := os.ReadFile(prompt)
	if err != nil {
		return Step{}, true, err // Init wrote it, or kept the one there
	}
	if usesVerifier(string(text)) {
		did = append(did, "the worker prompt runs it already")
	} else {
		t := string(text)
		if !strings.HasSuffix(t, "\n") {
			t += "\n"
		}
		if err := writeFile(prompt, []byte(t+verifierSection), 0o644); err != nil {
			return Step{}, true, err
		}
		did = append(did, "the worker prompt has workers run it before they close a ticket")
	}
	kind := StepDone
	if len(commit) == 0 && usesVerifier(string(text)) {
		kind = StepKept
	}
	return Step{Kind: kind, Label: label, Detail: strings.Join(did, "; "), Commit: commit}, true, nil
}
