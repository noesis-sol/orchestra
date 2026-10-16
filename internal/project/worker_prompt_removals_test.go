package project

import (
	"strings"
	"testing"
)

// Workers and the verifier are told to leave their throwaway files and never rm a variable or glob
// target, which Claude Code's safety check holds for a person, stalling the ticket.
func TestPromptsKeepRemovalsOffVariablesAndGlobs(t *testing.T) {
	for name, text := range map[string]string{
		"worker prompt": fillTemplate(promptTemplate, FastRunner),
		"verifier":      VerifierText(nil, nil, nil),
	} {
		for _, want := range []string{"leave them there", "Never `rm` a target", "safety check"} {
			if !strings.Contains(strings.Join(strings.Fields(text), " "), want) {
				t.Errorf("the %s lacks %q:\n%s", name, want, text)
			}
		}
	}
}
