package gittest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

func TestMain(m *testing.M) { os.Exit(Main(m).Run()) }

// git runs git in dir, failing t if it fails.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := command.Output(context.Background(), command.WriteLimit, dir, "git",
		append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A commit under Main starts no maintenance, which would go on in the background.
func TestMainKeepsGitFromMaintainingInTheBackground(t *testing.T) {
	repo := t.TempDir()
	trace := filepath.Join(t.TempDir(), "trace")
	git(t, repo, "init", "-q")
	t.Setenv("GIT_TRACE2", trace)
	git(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	got, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "cmd_name commit") {
		t.Fatalf("the trace doesn't show the commit; the test proves nothing:\n%s", got)
	}
	if strings.Contains(string(got), "git maintenance run") {
		t.Errorf("the commit started maintenance:\n%s", got)
	}
}

// setConfig adds to the settings the environment already holds, which git keeps reading.
func TestSetConfigKeepsTheEnvironmentsSettings(t *testing.T) {
	next := os.Getenv("GIT_CONFIG_COUNT") // Main's setting is the last; setConfig's goes after it
	for _, name := range []string{"GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_" + next, "GIT_CONFIG_VALUE_" + next} {
		t.Setenv(name, os.Getenv(name)) // put back as the test ends
	}
	if err := setConfig("gittest.added", "yes"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, want := range map[string]string{"maintenance.auto": "false", "gittest.added": "yes"} {
		if got := strings.TrimSpace(git(t, dir, "config", "--get", name)); got != want {
			t.Errorf("git config --get %s = %q, want %q", name, got, want)
		}
	}
}
