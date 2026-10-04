package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
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
	notFound := &Error{Code: AgentNotFound, Err: errors.New("herdr agent get x: exit status 1")}
	if _, _, s, err := readAgent("", notFound); s != "gone" || err != nil {
		t.Errorf("no agent: %q %v", s, err)
	}
	// A failed call that doesn't say the agent is missing tells nothing about it.
	for _, failed := range []error{errors.New("exit status 1"), &Error{Code: "server_busy", Err: errors.New("exit status 1")}} {
		if _, k, s, err := readAgent("", failed); s != "" || k != "" || err != failed {
			t.Errorf("failed call %v: %q %q %v", failed, k, s, err)
		}
	}
	if _, _, s, err := readAgent("not json", nil); s != "" || err == nil {
		t.Errorf("garbled output: %q %v", s, err)
	}
}

// What Herdr really answers, on stderr with exit status 1.
const (
	notFoundStderr = `{"error":{"code":"agent_not_found","message":"agent target x not found"},"id":"cli:agent:get"}`
	badNameStderr  = `{"error":{"code":"invalid_agent_name","message":"agent name must start with a lowercase letter and contain only lowercase letters, digits, '-' or '_' (1-32 characters)"},"id":"cli:agent:start"}`
)

// failingHerdr puts a herdr on PATH that writes stderr to stderr and exits 1.
func failingHerdr(t *testing.T, stderr string) {
	t.Helper()
	herdrScript(t, "#!/bin/sh\nprintf '%s\\n' "+command.ShellQuote(stderr)+" >&2\nexit 1\n")
}

func TestHerdrErrorsAreDecodedFromStderr(t *testing.T) {
	failingHerdr(t, notFoundStderr)
	err := (Terminal{}).RenameAgent(context.Background(), "x", "y")
	var he *Error
	if !errors.As(err, &he) || he.Code != AgentNotFound || he.Message != "agent target x not found" {
		t.Fatalf("got %#v", err)
	}
	var ce *command.Error
	if !errors.As(err, &ce) || err.Error() != ce.Error() {
		t.Errorf("the command's error should be underneath, with its text: %v", err)
	}
	if st, err := (Terminal{}).Status(context.Background(), "x"); st != "gone" || err != nil {
		t.Errorf("a missing agent is gone: %q %v", st, err)
	}

	failingHerdr(t, `{"error":{"code":"server_busy","message":"try again"}}`)
	if st, err := (Terminal{}).Status(context.Background(), "x"); st != "" || !HasCode(err, "server_busy") {
		t.Errorf("Herdr busy: %q %v", st, err)
	}

	failingHerdr(t, "panic: not JSON")
	err = (Terminal{}).RenameAgent(context.Background(), "x", "y")
	if errors.As(err, &he) || !errors.As(err, &ce) || ce.Stderr != "panic: not JSON" {
		t.Errorf("stderr that isn't Herdr's error stays a command error: %#v", err)
	}
	if st, err := (Terminal{}).Status(context.Background(), "x"); st != "" || err == nil {
		t.Errorf("unexplained failure: %q %v", st, err)
	}
}

func TestCurrentWorkspaceComesFromHerdr(t *testing.T) {
	t.Setenv("HERDR_WORKSPACE_ID", "w9Z")
	if got := CurrentWorkspace(context.Background(), os.Getenv); got != "w9Z" {
		t.Errorf("got %q", got)
	}
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_ENV", "")
	if got := CurrentWorkspace(context.Background(), os.Getenv); got != "" {
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
	failingHerdr(t, badNameStderr)
	err := term.StartAgent(context.Background(), "X.1", "claude", "p1", nil)
	if !term.IsNameRefused(err) || term.IsArgumentRefused(err) {
		t.Errorf("invalid_agent_name should count as a refused name: %v", err)
	}
	failingHerdr(t, notFoundStderr)
	err = term.StartAgent(context.Background(), "x", "claude", "p1", nil)
	if term.IsNameRefused(err) || term.IsNameRefused(errors.New("invalid_agent_name")) || term.IsNameRefused(nil) {
		t.Error("only Herdr's invalid_agent_name counts as refused")
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
	calls := filepath.Join(t.TempDir(), "calls")
	herdrScript(t, "#!/bin/sh\necho \"$*\" >> '"+calls+"'\n"+
		"case \"$*\" in *recent-unwrapped*) exit 1;; esac\necho screen\n")
	return calls
}

// herdrScript puts a herdr on PATH that runs script.
func herdrScript(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake herdr is a shell script")
	}
	dir := t.TempDir()
	faketool.Write(t, dir, "herdr", script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestScreenReadsABusyAgentsVisibleScreenAtOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: starts a fake herdr per case")
	}
	for status, want := range map[dispatch.AgentState]string{
		"working": "agent read a --source visible\n",
		"blocked": "agent read a --source visible\n",
		"idle":    "agent read a --source recent-unwrapped --lines 60\nagent read a --source visible\n",
		"":        "agent read a --source recent-unwrapped --lines 60\nagent read a --source visible\n",
	} {
		calls := fakeHerdr(t)
		if got := (Terminal{}).Screen(context.Background(), "a", status); got != "screen\n" {
			t.Errorf("Screen(%q) = %q", status, got)
		}
		if got, _ := os.ReadFile(calls); string(got) != want {
			t.Errorf("Screen(%q) ran herdr:\n%s\nwant:\n%s", status, got, want)
		}
	}
}

// Prompt returns once Herdr sees the agent start on the prompt, working or blocked, rather than at
// the end of its turn (Herdr's default for --wait), so the caller can watch the turn.
func TestPromptWaitsOnlyForTheAgentToStart(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+args+"'\n")
	if err := (Terminal{}).Prompt(context.Background(), "a", "do the ticket"); err != nil {
		t.Fatal(err)
	}
	want := "agent\nprompt\na\ndo the ticket\n--wait\n--until\nworking\n--until\nblocked\n--timeout\n30000\n"
	if got, _ := os.ReadFile(args); string(got) != want {
		t.Errorf("Prompt ran herdr with:\n%s\nwant:\n%s", got, want)
	}
}
