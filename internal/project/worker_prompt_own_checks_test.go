package project

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A worker checks its own change, each suite on what the change touches where the scout said how,
// whole where it didn't, and leaves the whole suite to the merge check and the full check.
func TestWorkerPromptChecksTheWorkersOwnChange(t *testing.T) {
	fast := []Suite{
		{Name: "unit tests", Command: "go test ./...", Targeted: "go test ./<package>/..."},
		{Name: "eslint", Command: "npm run lint", Targeted: "npx eslint <files>"},
		{Name: "the project's own check", Command: CheckScript},
		{Command: "make vet"},
		{Name: "runs itself", Command: "sh " + FullRunner}, // left out of the runners, and so of the prompt
	}
	full := []Suite{
		{Name: "e2e", Command: "npx playwright test", Targeted: "npx playwright test <paths>"},
		{Name: "build", Command: "make build"},
		{Name: "integration", Command: "make integration", Targeted: "go test -tags integration <paths>",
			Serial: true},
	}
	prompt := fillTemplate(promptTemplate, PromptChecks{Check: FastRunner, Fast: &fast, Full: &full})
	want := "  The suites, and how to run each on part of the project:\n" +
		"  - unit tests: `go test ./<package>/...`\n" +
		"  - eslint: `npx eslint <files>`\n" +
		"  - the project's own check: `" + CheckScript + "`, whole, as it doesn't run on part of the project\n" +
		"  - `make vet`: whole, as it doesn't run on part of the project\n" +
		"  - e2e (slower): `npx playwright test <paths>`\n" +
		"  - build (slower): `make build`, whole, where the change touches what it covers\n" +
		"  - integration (slower): leave it to the merge check; two copies at once, in two worktrees, collide\n" +
		"- Leave the whole suite to orchestra: its merge check, `" + FastRunner + "`, runs on every ticket\n"
	if !strings.Contains(prompt, "only the checks you need.\n"+want) {
		t.Errorf("the prompt lacks\n%s\nin:\n%s", want, prompt)
	}
	if strings.Contains(prompt, "runs itself") || checkPlaceholder(prompt) {
		t.Errorf("a runner's suite or a placeholder is left:\n%s", prompt)
	}
	for _, want := range []string{"lint and type checks on the files you changed",
		"hands the ticket back to you when your change breaks something you\n  didn't consider"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q", want)
		}
	}
	if DetectCheck(prompt) != FastRunner {
		t.Errorf("DetectCheck = %q", DetectCheck(prompt))
	}

	// A runner whose suites init doesn't know, kept as it is, or that checks nothing: where the
	// suites are, and how to run them.
	none := []Suite{}
	for name, checks := range map[string]PromptChecks{
		"kept":    {Check: FastRunner, Full: &full},
		"nothing": {Check: FastRunner, Fast: &none, Full: &none},
	} {
		prompt := fillTemplate(promptTemplate, checks)
		if !strings.Contains(prompt, "The suites are the commands `"+FastRunner+"` and `"+FullRunner+"` list") ||
			strings.Contains(prompt, "e2e") || checkPlaceholder(prompt) {
			t.Errorf("%s:\n%s", name, prompt)
		}
	}
}

// init writes the suites it chose into the worker prompt.
func TestInitWritesTheChosenSuitesIntoThePrompt(t *testing.T) {
	repo, _ := gitRepo(t)
	fast := []Suite{{Name: "pytest", Command: "make test", Targeted: "pytest <paths>"}}
	if _, err := initPrompt(false)(context.Background(), repo, Choice{Fast: &fast, Full: &[]Suite{}}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(repo, Dir, promptName)); !strings.Contains(got, "  - pytest: `pytest <paths>`\n") {
		t.Errorf("the prompt lacks the suite:\n%s", got)
	}
}

// A prompt written before workers checked only their own change still gives its check command.
func TestDetectCheckInAnEarlierPrompt(t *testing.T) {
	if got := DetectCheck("- Check your work with `make check`, the check orchestra runs"); got != "make check" {
		t.Errorf("DetectCheck = %q", got)
	}
}
