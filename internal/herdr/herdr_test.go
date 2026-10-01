package herdr

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
	// What Herdr really does: the answer goes to stderr, which command.Output puts in the error.
	onStderr := errors.New(`herdr agent get x: exit status 1: ` + notFound)
	if _, _, s, err := readAgent("", onStderr); s != "gone" || err != nil {
		t.Errorf("no agent, answered on stderr: %q %v", s, err)
	}
	busy := errors.New(`herdr agent get x: exit status 1: {"error":{"code":"server_busy"}}`)
	if _, _, s, err := readAgent("", busy); s != "unreadable" || err != busy {
		t.Errorf("Herdr busy, answered on stderr: %q %v", s, err)
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

func TestAgentName(t *testing.T) {
	cases := map[string]string{
		"orchestra-aix":                    "orchestra-aix",
		"CalendarView-bl0":                 "calendarview-bl0",
		"CalendarView-bl0.1":               "calendarview-bl0_1",
		"orchestra-abc.1.2":                "orchestra-abc_1_2",
		"9lives-x1":                        "t9lives-x1",
		"_x":                               "t_x",
		"":                                 "t",
		"émile-q2":                         "t_mile-q2",
		"a2345678901234567890123456789012": "a2345678901234567890123456789012",
	}
	for id, want := range cases {
		if got := AgentName(id); got != want {
			t.Errorf("AgentName(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestAgentNameIsAlwaysValid(t *testing.T) {
	for _, id := range []string{"CalendarView-bl0.12.3", "platform-backend-services-a3f.12.3", "Ω", "x y/z:w", strings.Repeat("A", 100)} {
		name := AgentName(id)
		if !validName(name) {
			t.Errorf("AgentName(%q) = %q, which Herdr would refuse", id, name)
		}
		if AgentName(name) != name {
			t.Errorf("AgentName(%q) = %q changes a valid name", name, AgentName(name))
		}
	}
}

func TestLongAgentNamesDoNotCollide(t *testing.T) {
	a, b := "platform-backend-services-a3f.12.3", "platform-backend-services-a3f.12.4" // 34 characters, alike for 33
	na, nb := AgentName(a), AgentName(b)
	if len(na) != 32 || len(nb) != 32 {
		t.Errorf("names should use the whole 32 characters: %q %q", na, nb)
	}
	if na == nb {
		t.Errorf("%q and %q both became %q", a, b, na)
	}
	if !strings.HasPrefix(na, "platform-backend-services") {
		t.Errorf("a cut name should keep the start of the ID: %q", na)
	}
}

func TestIsNameRefused(t *testing.T) {
	var term Terminal
	if !term.IsNameRefused(errors.New(`herdr agent start X.1: exit status 1: {"error":{"code":"invalid_agent_name"}}`)) {
		t.Error("invalid_agent_name should count as refused")
	}
	if term.IsNameRefused(errors.New("agent_not_ready")) || term.IsNameRefused(nil) {
		t.Error("only invalid_agent_name counts as refused")
	}
}

// validName is Herdr's rule for agent names.
func validName(s string) bool {
	if len(s) < 1 || len(s) > 32 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// fakeHerdr puts a herdr on PATH that logs each call's arguments and fails any 'agent read' of
// scrollback, as Herdr does while the agent works. It returns the log's path.
func fakeHerdr(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake herdr is a shell script")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n" +
		"case \"$*\" in *recent-unwrapped*) exit 1;; esac\necho screen\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func TestScreenReadsABusyAgentsVisibleScreenAtOnce(t *testing.T) {
	for status, want := range map[string]string{
		"working": "agent read a --source visible\n",
		"blocked": "agent read a --source visible\n",
		"idle":    "agent read a --source recent-unwrapped --lines 60\nagent read a --source visible\n",
		"":        "agent read a --source recent-unwrapped --lines 60\nagent read a --source visible\n",
	} {
		calls := fakeHerdr(t)
		if got := (Terminal{}).Screen("a", status); got != "screen\n" {
			t.Errorf("Screen(%q) = %q", status, got)
		}
		if got, _ := os.ReadFile(calls); string(got) != want {
			t.Errorf("Screen(%q) ran herdr:\n%s\nwant:\n%s", status, got, want)
		}
	}
}
