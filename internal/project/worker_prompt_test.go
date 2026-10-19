package project

import (
	"strings"
	"testing"
)

// The prompt init writes names the runners, asks for tests with behaviour changes, and gives back
// the fast runner as its check, which the next init keeps as it is.
func TestWorkerPromptNamesTheRunners(t *testing.T) {
	prompt := fillTemplate(promptTemplate, PromptChecks{Check: FastRunner})
	for _, want := range []string{
		"Check your own change before you close the ticket",
		"The suites are the commands `" + FastRunner + "` and `" + FullRunner + "` list",
		"its merge check, `" + FastRunner + "`, runs on every ticket",
		"the full check, `" + FullRunner + "`, runs every suite",
		"Close the ticket only when your checks pass",
		"adds or updates tests for it",
		"Never add a new test framework",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt lacks %q:\n%s", want, prompt)
		}
	}
	if checkPlaceholder(prompt) {
		t.Errorf("a placeholder is left:\n%s", prompt)
	}
	if got := DetectCheck(prompt); got != FastRunner {
		t.Errorf("DetectCheck = %q, want %s", got, FastRunner)
	}
	if c := DefaultChoice(Settings{}, prompt); c.Fast != nil || c.CheckFrom != "found in the worker prompt" {
		t.Errorf("the runner found in the prompt should be kept as it is: %+v", c)
	}

	// Another check is named as it is, its suites unknown.
	prompt = fillTemplate(promptTemplate, PromptChecks{Check: "make check"})
	if DetectCheck(prompt) != "make check" || !strings.Contains(prompt, "Run what `make check` runs on part") ||
		checkPlaceholder(prompt) {
		t.Errorf("make check:\n%s", prompt)
	}
}

// checkPlaceholder reports whether one of the template's placeholders for the check is left.
func checkPlaceholder(prompt string) bool {
	return strings.Contains(prompt, "<check command>") || strings.Contains(prompt, suitesPlaceholder)
}
