package project

import (
	"strings"
	"testing"
)

// The worker gives the verifier the commit at which its own checks passed and the commands it ran
// there, and the verifier runs the suites on what the change touches only where that commit isn't
// HEAD or the worktree has changes, or for a suite the worker left out that covers the change: in
// most rounds the worker had just passed its checks and changed nothing since. The whole suite is the
// merge check's either way.
func TestTheVerifierUsesTheWorkersPassingChecks(t *testing.T) {
	agent := VerifierText(nil, []string{"scripts/check.sh"}, []string{"npm run e2e"}, nil, nil)
	for _, want := range []string{
		"Give it the ticket's ID, the commit at which your checks passed and the commands you ran there.\n",
		"3. The worker tells you the commit at which its own checks passed, the commands it ran there, and\n" +
			"   the file their output is in where it kept one.",
		// The commit is HEAD and the worktree clean: no check, unless the worker left a suite out.
		"When `git rev-parse HEAD` is that commit and\n   `git status --porcelain` prints nothing, those " +
			"results stand: quote them (the end of the file,\n   where it gave one) rather than run them again, " +
			"and run only a suite they leave out that covers\n   what the change touches.",
		// Another commit, changes, or no commit given: the suites on what the change touches.
		"Otherwise, or when it gave no commit, run the suites on what the change\n   touches, as under Its checks",
		"Quote\n   each command and the end of its output.",
		// Never the whole suite.
		"Leave the whole suite to orchestra: `" + FastRunner + "` runs\n   on the ticket before it merges, and `" +
			FullRunner + "` at the end of the run.\n",
		// init doesn't know the suites: each runner command on part of the project where it can.
		"- Run each of those commands on what a change touches where its tool allows it\n",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("the verifier lacks %q:\n%s", want, agent)
		}
	}
	for _, gone := range []string{"in the foreground, and quote", "the worker didn't run"} {
		if strings.Contains(agent, gone) {
			t.Errorf("the verifier still runs the whole check (%q):\n%s", gone, agent)
		}
	}
	for _, want := range []string{
		"once your checks pass after your last commit",
		"give it the ticket's ID, TICKET_ID, the\n  commit at which your checks passed (`git rev-parse HEAD`), " +
			"the commands you ran there, and the\n  file their output is in where you kept one, so that it " +
			"needn't run them again; nothing of your\n  reasoning.",
		"run your\n  checks again, and ask it again with the new commit and commands.",
	} {
		if !strings.Contains(verifierSection, want) {
			t.Errorf("the Verify section lacks %q:\n%s", want, verifierSection)
		}
	}
	if strings.Contains(verifierSection, FastRunner) || strings.Contains(verifierSection, FullRunner) {
		t.Errorf("the Verify section has the worker run a whole runner:\n%s", verifierSection)
	}
}

// Where init chose the suites, the verifier is told how to run each on part of the project, with the
// lines the worker prompt gives.
func TestTheVerifierRunsTheSuitesOnWhatTheChangeTouches(t *testing.T) {
	fast := []Suite{{Name: "unit tests", Command: "go test ./...", Targeted: "go test ./<package>/..."}}
	full := []Suite{{Name: "build", Command: "make build"}, {Name: "e2e", Command: "npm run e2e", Serial: true}}
	repo := verifierRepo(t, fast, full)
	if _, _, err := oneStep(t, ApplyVerifier, repo, Choice{Verifier: true, Fast: &fast, Full: &full}); err != nil {
		t.Fatal(err)
	}
	agent := readRepoFile(t, repo, VerifierPath)
	want := "- How to run each suite on what a change touches, as the worker prompt has workers run them:\n" +
		suiteBullets(&fast, &full) + "\n## How to verify\n"
	if !strings.Contains(agent, want) || !strings.Contains(want, "  - unit tests: `go test ./<package>/...`\n") ||
		!strings.Contains(want, "  - build (slower): `make build`, whole, where the change touches what it covers\n") ||
		!strings.Contains(want, "  - e2e (slower): leave it to the merge check") {
		t.Errorf("the verifier lacks\n%s\nin:\n%s", want, agent)
	}
	if strings.Contains(agent, "Run each of those commands") {
		t.Errorf("the verifier gives the fallback as well:\n%s", agent)
	}
	prompt := fillTemplate(promptTemplate, PromptChecks{Check: FastRunner, Fast: &fast, Full: &full})
	if !strings.Contains(prompt, suiteBullets(&fast, &full)) {
		t.Errorf("the worker prompt's suites differ from the verifier's:\n%s", prompt)
	}
}
