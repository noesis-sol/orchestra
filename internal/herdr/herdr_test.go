package herdr

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

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
