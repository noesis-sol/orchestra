package herdr

import (
	"os"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
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
	if got := LastActivity(screen); !strings.HasPrefix(got, "✻ Running checks") {
		t.Errorf("got %q", got)
	}
	if got := LastActivity("❯ \n  status bar"); got != "" {
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
		if got := InputHolds(screen, prompt); got != want {
			t.Errorf("InputHolds(%q) = %v, want %v", screen, got, want)
		}
	}
}

func TestParsePaneAgent(t *testing.T) {
	unnamed := `{"id":"cli:agent:get","result":{"agent":{"name":null,"agent":"claude","agent_status":"working","pane_id":"w2B:p1D"}}}`
	if n, k, s := parsePaneAgent([]byte(unnamed)); n != "" || k != "claude" || s != "working" {
		t.Errorf("unnamed: %q %q %q", n, k, s)
	}
	named := `{"result":{"agent":{"name":"kinieta-9g6","agent":"claude","agent_status":"idle"}}}`
	if n, _, s := parsePaneAgent([]byte(named)); n != "kinieta-9g6" || s != "idle" {
		t.Errorf("named: %q %q", n, s)
	}
	if _, _, s := parsePaneAgent([]byte(`{"error":{"code":"agent_not_found"}}`)); s != "gone" {
		t.Errorf("no agent: %q", s)
	}
}

func TestShellQuoteMakesOneWord(t *testing.T) {
	for _, in := range []string{"Your instructions for ticket k-1 are in .orchestra/run/prompt.md.", "it's `a` $test \"q\""} {
		out, err := command.Output("", "sh", "-c", "printf '%s' "+shellQuote(in))
		if err != nil || out != in {
			t.Errorf("shellQuote(%q) round-trips to %q (%v)", in, out, err)
		}
	}
}

func TestCurrentWorkspaceComesFromHerdr(t *testing.T) {
	t.Setenv("HERDR_WORKSPACE_ID", "w9Z")
	if got := CurrentWorkspace(os.Getenv); got != "w9Z" {
		t.Errorf("got %q", got)
	}
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_ENV", "")
	if got := CurrentWorkspace(os.Getenv); got != "" {
		t.Errorf("outside Herdr there is no current workspace, got %q", got)
	}
}
