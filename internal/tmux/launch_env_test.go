package tmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A worker launched in a pane of a tmux server started from a Claude Code session, which gives every
// pane CLAUDECODE, runs without that session's markers and with orchestra's PATH, not the server's.
func TestLaunchInPaneLeavesOutAClaudeSessionAndSetsPath(t *testing.T) {
	f := newFakeTmux(t)
	path := os.Getenv("PATH") + string(os.PathListSeparator) + "/opt/orchestra/bin"
	t.Setenv("PATH", path)
	if err := (Terminal{}).LaunchInPane(bg, "%5", "claude", []string{"--x"}); err != nil {
		t.Fatal(err)
	}
	respawn := f.call("respawn-pane")
	if len(respawn) < 7 {
		t.Fatalf("respawn-pane got %q", respawn)
	}
	// The pane's /bin/sh inherits the server's environment: a Claude Code session's, and another PATH.
	script := strings.Replace(respawn[6], "exec 'claude' '--x'", "exec env", 1)
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = []string{"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "CLAUDE_CODE_CHILD_SESSION=1",
		"PATH=/usr/bin:/bin", "HOME=/h"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	env := "\n" + string(out)
	for _, v := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION"} {
		if strings.Contains(env, "\n"+v+"=") {
			t.Errorf("the agent is given %s:\n%s", v, out)
		}
	}
	if !strings.Contains(env, "\nPATH="+path+"\n") || !strings.Contains(env, "\nHOME=/h\n") {
		t.Errorf("the agent should have orchestra's PATH and the rest of the pane's environment:\n%s", out)
	}
}
