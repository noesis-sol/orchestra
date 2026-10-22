package cmux

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A worker launched in a cmux terminal whose shell has a Claude Code session's markers, as one opened
// from a Claude Code session's Bash tool would, runs without them and with orchestra's PATH, not the
// shell's.
func TestLaunchInPaneLeavesOutAClaudeSessionAndSetsPath(t *testing.T) {
	newFakeCmux(t)
	term := bookOf(t)
	path := os.Getenv("PATH") + string(os.PathListSeparator) + "/opt/orchestra/bin"
	t.Setenv("PATH", path)
	if err := term.LaunchInPane(bg, workSF, "claude", []string{"--x"}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(term.launchPath(workSF, ".sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", strings.Replace(string(body), "exec 'claude' '--x'", "exec env", 1))
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
		t.Errorf("the agent should have orchestra's PATH and the rest of the shell's environment:\n%s", out)
	}
}
