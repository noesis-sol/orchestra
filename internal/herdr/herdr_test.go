package herdr

import (
	"errors"
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

func TestReadAgent(t *testing.T) {
	unnamed := `{"id":"cli:agent:get","result":{"agent":{"name":null,"agent":"claude","agent_status":"working","pane_id":"w2B:p1D"}}}`
	if n, k, s, err := readAgent(unnamed, nil); n != "" || k != "claude" || s != "working" || err != nil {
		t.Errorf("unnamed: %q %q %q %v", n, k, s, err)
	}
	named := `{"result":{"agent":{"name":"kinieta-9g6","agent":"claude","agent_status":"idle"}}}`
	if n, _, s, err := readAgent(named, nil); n != "kinieta-9g6" || s != "idle" || err != nil {
		t.Errorf("named: %q %q %v", n, s, err)
	}
	failed := errors.New("exit status 1")
	notFound := `{"error":{"code":"agent_not_found","message":"agent target x not found"},"id":"cli:agent:get"}`
	if _, _, s, err := readAgent(notFound, failed); s != "gone" || err != nil {
		t.Errorf("no agent: %q %v", s, err)
	}
	// A failed call that doesn't say the agent is missing tells nothing about it.
	for _, out := range []string{"", `{"error":{"code":"server_busy"}}`} {
		if _, k, s, err := readAgent(out, failed); s != "unreadable" || k != "" || err != failed {
			t.Errorf("failed call with %q: %q %q %v", out, k, s, err)
		}
	}
	if _, _, s, err := readAgent("not json", nil); s != "unreadable" || err == nil {
		t.Errorf("garbled output: %q %v", s, err)
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
