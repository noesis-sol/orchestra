package tmux

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The launch contract (dispatch.Starter): tmux replaces what runs in the pane (respawn-pane -k) rather
// than type into it.
func TestLaunchInPaneRunsTheAgentInPlaceOfTheShell(t *testing.T) {
	f := newFakeTmux(t)
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+"/opt/claude/bin")
	args := []string{"--settings", "/w/.orchestra/run/hooks.json", "--append-system-prompt-file", "/w/rules.md",
		"two\nlines and 'quotes';"}
	if err := (Terminal{}).LaunchInPane(bg, "%5", "claude", args); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-option", "-p", "-t", "%5", "@orchestra-kind", "claude", ";",
		"set-option", "-p", "-t", "%5", "remain-on-exit", "on", ";",
		"set-option", "-p", "-t", "%5", "@orchestra-records", "/w/.orchestra/run"}
	if got := f.call("set-option"); !slices.Equal(got, want) {
		t.Errorf("set-option got:\n%q\nwant:\n%q", got, want)
	}
	respawn := f.call("respawn-pane")
	if len(respawn) != 15 || !slices.Equal(respawn[:6], []string{"respawn-pane", "-k", "-t", "%5", "/bin/sh", "-c"}) {
		t.Fatalf("respawn-pane got %q", respawn)
	}
	// The pane keeps the process ID of what it launched.
	if got := respawn[7:]; !slices.Equal(got, []string{";", "set-option", "-p", "-F", "-t", "%5", "@orchestra-pid",
		"#{pane_pid}"}) {
		t.Errorf("after respawn-pane got %q", got)
	}
	// The command line gives the agent its arguments exactly.
	script := strings.Replace(respawn[6], "exec 'claude'", `printf '%s\0'`, 1)
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !slices.Equal(got, args) {
		t.Errorf("the agent would get %q, want %q", got, args)
	}
	if slices.Contains(f.ops(), "send-keys") {
		t.Error("nothing is typed into a shell")
	}

	f = newFakeTmux(t)
	if err := (Terminal{}).LaunchInPane(bg, "%5", "claude", []string{"--", "--settings", "/x/hooks.json"}); err != nil {
		t.Fatal(err)
	}
	if got := f.call("set-option"); !slices.Contains(got, "-u") || slices.Contains(got, "/x") {
		t.Errorf("an agent launched without hooks has no records folder: %q", got)
	}

	if err := (Terminal{}).LaunchInPane(bg, "", "claude", nil); !Missing(err) {
		t.Errorf("no pane: %v", err)
	}
}

func TestStartAgentNamesItAndWaitsForItToBeReady(t *testing.T) {
	f := newFakeTmux(t)
	idle := idleRecords(t)
	f.listPanes(paneLine{id: "%1", agent: "x-1", command: "zsh"}, paneLine{id: "%5", command: "zsh"})
	// Once launched, the pane holds the agent, named.
	f.after("respawn-pane", `printf '%s\n' '`+paneLine{id: "%5", agent: "x-1", kind: "claude", records: idle,
		command: "2.1.0", dir: "/w"}.String()+`' > "$d/out-list-panes"`)
	if err := (Terminal{}).StartAgent(bg, "x-1", "claude", "%5", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.ops(); !slices.Equal(got, []string{"set-option", "list-panes", "set-option", "respawn-pane", "list-panes",
		"capture-pane"}) {
		t.Errorf("tmux got %v", got)
	}
	// The name moves to the new pane from one that held it.
	want := []string{"set-option", "-p", "-u", "-t", "%1", "@orchestra-agent", ";",
		"set-option", "-p", "-t", "%5", "@orchestra-agent", "x-1"}
	if got := f.calls()[2]; !slices.Equal(got, want) {
		t.Errorf("naming got:\n%q\nwant:\n%q", got, want)
	}

	f = newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idle, dead: true})
	if err := (Terminal{}).StartAgent(bg, "x-1", "claude", "%5", nil); !Missing(err) {
		t.Errorf("an agent that exited at once: %v", err)
	}
}
