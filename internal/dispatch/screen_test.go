package dispatch

import (
	"strings"
	"testing"
)

func TestLastActivitySkipsTheInputBoxAndStatusBar(t *testing.T) {
	screen := strings.Join([]string{
		"⏺ Read(Package.swift)",
		"  ⎿  Read 40 lines",
		"⏺ Bash(scripts/ci-local.sh lint ios)",
		"  ⎿  lint passed",
		"✻ Running checks… (1m 12s · ↓ 2.1k tokens)",
		"",
		"────────────────",
		"❯ ",
		"────────────────",
		"  ⏵⏵ auto mode on (shift+tab to cycle)",
	}, "\n")
	if got := lastActivity(screen); !strings.HasPrefix(got, "✻ Running checks") {
		t.Errorf("got %q", got)
	}
	if got := lastActivity("❯ \n  status bar"); got != "" {
		t.Errorf("no activity should give empty, got %q", got)
	}
}

func TestInputHoldsOnlyAnUnsentPrompt(t *testing.T) {
	prompt := "You are responsible for exactly one Beads ticket: kinieta-vzg. Do not work on any other ticket.\n" +
		"- If you cannot finish: note why on the ticket, defer it, and stop.\n\nWhen you are finished, say DONE and stop.\n"
	chrome := "\n─────────\n  ⏵⏵ auto mode on (shift+tab to cycle)"
	cases := map[string]bool{
		// Unsent, as Claude Code shows a long paste: only its last lines.
		"────────\n❯ - If you cannot finish: note why on the ticket, defer it, and stop.\n  When you are finished, say DONE and stop.\n────────\n  paste again to expand": true,
		// Unsent: the opening words, or the placeholder for a long paste.
		"❯ You are responsible for exactly one Beads ticket: kinieta-vzg. Do not" + chrome: true,
		"❯ [Pasted text #1 +20 lines]" + chrome:                                            true,
		// Sent: the transcript echoes the prompt above an empty input box.
		"❯ You are responsible for exactly one Beads ticket: kinieta-vzg.\n⏺ Claiming it.\n─────────\n❯ " + chrome: false,
		// Empty box, or Claude Code's suggestion placeholder.
		"❯ " + chrome: false,
		"❯ Try \"how do I log an error?\"" + chrome: false,
		// A dialog's selected option is not the input box's prompt.
		"❯ No, exit\n   Yes, I trust this folder": false,
		"": false,
	}
	for screen, want := range cases {
		if got := inputHolds(screen, prompt); got != want {
			t.Errorf("inputHolds(%q) = %v, want %v", screen, got, want)
		}
	}
}
