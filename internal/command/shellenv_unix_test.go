//go:build unix

package command

import (
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// The lines of ShellEnv give what /bin/sh runs after them that environment: a name alone unset, a
// value set as it is, quotes and line breaks and all.
func TestShellEnvGivesTheScriptsCommandsTheEnvironment(t *testing.T) {
	script := ShellEnv([]string{"GONE", "SET=it's two\nlines", "EMPTY="}) + "env"
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = []string{"GONE=1", "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "GONE=") {
		t.Errorf("GONE is still set:\n%s", out)
	}
	lines := strings.Split(string(out), "\n")
	if !strings.Contains(string(out), "SET=it's two\nlines\n") || !slices.Contains(lines, "EMPTY=") {
		t.Errorf("SET and EMPTY aren't as given:\n%s", out)
	}
}
