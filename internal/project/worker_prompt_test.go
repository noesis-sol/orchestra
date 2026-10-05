package project

import (
	"strings"
	"testing"
)

// The prompt init writes names the runners, asks for tests with behaviour changes, and gives back
// the fast runner as its check, which the next init keeps as it is.
func TestWorkerPromptNamesTheRunners(t *testing.T) {
	prompt := fillTemplate(promptTemplate, FastRunner)
	for _, want := range []string{
		"Check your work with `" + FastRunner + "`",
		"a suite's command from `" + FastRunner + "`",
		"also run `" + FullRunner + "`",
		"Close the ticket only when `" + FastRunner + "` passes",
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

	// Another check is named as it is, with the sentence on what it runs left to the project.
	prompt = fillTemplate(promptTemplate, "make check")
	if DetectCheck(prompt) != "make check" || strings.Contains(prompt, "a command a line") ||
		checkPlaceholder(prompt) {
		t.Errorf("make check:\n%s", prompt)
	}
}

// checkPlaceholder reports whether one of the template's placeholders for the check is left.
func checkPlaceholder(prompt string) bool {
	return strings.Contains(prompt, "<check command>") || strings.Contains(prompt, "<What it runs") ||
		strings.Contains(prompt, "<a quicker subset>")
}
