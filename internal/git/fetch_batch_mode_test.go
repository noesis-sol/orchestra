package git

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// noSSHEnv clears the variables that choose ssh's command, which the machine running the tests may set.
func noSSHEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH", "GIT_SSH_VARIANT"} {
		t.Setenv(name, "")
	}
}

// The fetch runs ssh in batch mode, so that an ssh that would ask for a key's passphrase or about an
// unknown host key fails at once rather than waits on a terminal it can't read: the ssh command git
// would run, the one configured kept, with -o BatchMode=yes after it.
func TestFetchRunsSSHInBatchMode(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, env, value, config string
		want                     string // GIT_SSH_COMMAND, "" for none
	}{
		{name: "none configured", want: "ssh -o BatchMode=yes"},
		{name: "core.sshCommand", config: "ssh -i ~/.ssh/deploy", want: "ssh -i ~/.ssh/deploy -o BatchMode=yes"},
		{name: "GIT_SSH_COMMAND over core.sshCommand", env: "GIT_SSH_COMMAND", value: "ssh -p 2222",
			config: "ssh -i ~/.ssh/deploy", want: "ssh -p 2222 -o BatchMode=yes"},
		{name: "GIT_SSH", env: "GIT_SSH", value: "/opt/it's ssh", want: `'/opt/it'\''s ssh' -o BatchMode=yes`},
		{name: "core.sshCommand over GIT_SSH", env: "GIT_SSH", value: "/opt/ssh", config: "ssh -4",
			want: "ssh -4 -o BatchMode=yes"},
		{name: "plink", config: `"C:\Program Files\PuTTY\plink.exe" -batch`},
		{name: "GIT_SSH_VARIANT simple", env: "GIT_SSH_VARIANT", value: "simple", config: "my-ssh"},
	} {
		t.Run(c.name, func(t *testing.T) {
			noSSHEnv(t)
			repo, git := newRepo(t)
			if c.env != "" {
				t.Setenv(c.env, c.value)
			}
			if c.config != "" {
				git("config", "core.sshCommand", c.config)
			}
			env, err := fetchEnv(ctx, repo)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(env, "GIT_TERMINAL_PROMPT=0") {
				t.Errorf("the fetch's environment %q has no GIT_TERMINAL_PROMPT=0", env)
			}
			var got string
			for _, v := range env {
				if command, ok := strings.CutPrefix(v, "GIT_SSH_COMMAND="); ok {
					got = command
				}
			}
			if got != c.want {
				t.Errorf("the fetch's GIT_SSH_COMMAND = %q, want %q", got, c.want)
			}
		})
	}
}

// git runs the configured ssh command with BatchMode=yes for a fetch from an ssh remote.
func TestFetchFromAnSSHRemoteRunsSSHInBatchMode(t *testing.T) {
	noSSHEnv(t)
	repo, git := newRepo(t)
	dir := t.TempDir()
	args := filepath.Join(dir, "args")
	script := filepath.Join(dir, "ssh.sh")
	if err := os.WriteFile(script, []byte("printf '%s\\n' \"$@\" >> '"+args+"'\nexit 255\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("config", "core.sshCommand", "sh '"+script+"'")
	git("remote", "add", "origin", "ssh://example.invalid/remote.git")
	if _, err := (Git{}).Fetch(context.Background(), repo, "origin", "refs/heads/main"); err == nil {
		t.Error("a fetch through an ssh that fails should fail")
	}
	got, err := os.ReadFile(args)
	if err != nil {
		t.Fatalf("git didn't run the configured ssh command: %v", err)
	}
	if !strings.Contains(string(got), "BatchMode=yes") {
		t.Errorf("git ran the configured ssh command with %q, without -o BatchMode=yes", got)
	}
}
