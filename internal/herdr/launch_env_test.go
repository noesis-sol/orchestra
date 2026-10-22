package herdr

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A worker launched in a Herdr pane runs with orchestra's PATH, not the pane's shell's, and without the
// markers of a Claude Code session that shell may have.
func TestLaunchInPaneLeavesOutAClaudeSessionAndSetsPath(t *testing.T) {
	dir := t.TempDir()
	typed := filepath.Join(dir, "typed")
	herdrScript(t, "#!/bin/sh\n"+
		`[ "$1 $2" = "pane run" ] && { printf '%s' "$4" > '`+typed+`'; exit 0; }`+"\n"+
		"echo '"+processInfoJSON(7, 7, "zsh")+"'\n")
	t.Setenv("TMPDIR", dir)
	path := os.Getenv("PATH") + string(os.PathListSeparator) + "/opt/orchestra/bin"
	t.Setenv("PATH", path)
	if err := (Terminal{}).LaunchInPane(context.Background(), "w2E:p3T", "claude", []string{"--x"}); err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(typed)
	if err != nil {
		t.Fatal(err)
	}
	script := strings.TrimPrefix(string(line), " /bin/sh ")
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("the typed line %q runs no script: %v", line, err)
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
