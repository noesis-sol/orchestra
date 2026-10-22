package git

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// FetchLimit is how long a fetch of a branch's upstream may take before it is stopped: longer than a
// read, as it reaches the network, and short enough that a remote that doesn't answer doesn't hold a
// run's start for long.
const FetchLimit = time.Minute

// UpstreamOf returns the upstream branch is set to follow: its remote-tracking ref (refs/remotes/origin/main),
// the remote's name and the branch on the remote (refs/heads/main), all "" when it follows none. branch
// is a branch's name, with heads/ or refs/heads/ before it or without. It returns git's error when git
// can't tell, which is not the same as none.
func (Git) UpstreamOf(ctx context.Context, repo, branch string) (ref, remote, remoteRef string, err error) {
	name := strings.TrimPrefix(strings.TrimPrefix(branch, "refs/"), "heads/")
	out, err := readGit(ctx, repo, "for-each-ref",
		"--format=%(upstream)%00%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/"+name)
	if err != nil {
		return "", "", "", err
	}
	fields := strings.Split(strings.TrimSpace(out), "\x00")
	if len(fields) != 3 || fields[0] == "" {
		return "", "", "", nil // no such branch, or no upstream
	}
	return fields[0], fields[1], fields[2], nil
}

// Fetch fetches remoteRef from remote into its remote-tracking ref, without tags, within FetchLimit.
// Nothing is asked of anyone: git asks for no credentials (GIT_TERMINAL_PROMPT=0) and ssh for no
// passphrase and about no host key (BatchMode=yes, see fetchEnv), failing instead. An ssh that asked
// would read the terminal from the fetch's process group of its own, be stopped, and hold the run's
// start until FetchLimit.
func (Git) Fetch(ctx context.Context, repo, remote, remoteRef string) (string, error) {
	env, err := fetchEnv(ctx, repo)
	if err != nil {
		return "", err
	}
	return command.OutputWithEnv(ctx, FetchLimit, repo, env, "git", "fetch", "--quiet", "--no-tags", remote, remoteRef)
}

// fetchEnv is the environment Fetch adds to git's: gitEnv, with its GIT_TERMINAL_PROMPT=0, and the
// ssh command git would run in repo, in the order git picks it (GIT_SSH_COMMAND, core.sshCommand,
// GIT_SSH, ssh), with -o BatchMode=yes after it, so that ssh fails rather than asks. A command git runs
// as PuTTY's plink, or as a simple ssh, is left as it is, as it may take no -o. It returns git's error
// when git can't read core.sshCommand.
func fetchEnv(ctx context.Context, repo string) ([]string, error) {
	env := slices.Clone(gitEnv)
	ssh := os.Getenv("GIT_SSH_COMMAND")
	if ssh == "" {
		out, err := readGit(ctx, repo, "config", "--get", "core.sshCommand")
		set, err := answer(err)
		if err != nil {
			return nil, fmt.Errorf("git config core.sshCommand: %w", err)
		}
		if set {
			ssh = strings.TrimSpace(out)
		}
	}
	if ssh == "" {
		if program := os.Getenv("GIT_SSH"); program != "" {
			ssh = "'" + strings.ReplaceAll(program, "'", `'\''`) + "'"
		} else {
			ssh = "ssh"
		}
	}
	if takesNoOptions(ssh) {
		return env, nil
	}
	return append(env, "GIT_SSH_COMMAND="+ssh+" -o BatchMode=yes"), nil
}

// takesNoOptions reports whether git runs ssh, an ssh command, as PuTTY's plink or TortoisePlink, or
// as a simple ssh that takes no options, by GIT_SSH_VARIANT or by the name of its program, as git
// tells them apart.
func takesNoOptions(ssh string) bool {
	variant := os.Getenv("GIT_SSH_VARIANT")
	if variant == "" {
		program := strings.TrimSpace(ssh)
		if quote := program[:min(1, len(program))]; quote == "'" || quote == `"` {
			program, _, _ = strings.Cut(program[1:], quote)
		} else if fields := strings.Fields(program); len(fields) > 0 {
			program = fields[0]
		}
		program = program[strings.LastIndexAny(program, `/\`)+1:]
		variant = strings.TrimSuffix(strings.ToLower(program), ".exe")
	}
	switch variant {
	case "plink", "putty", "tortoiseplink", "simple":
		return true
	}
	return false
}

// AheadBehind counts the commits a has that b doesn't (ahead) and those b has that a doesn't
// (behind). It returns git's error when git can't count them.
func (Git) AheadBehind(ctx context.Context, repo, a, b string) (ahead, behind int, err error) {
	out, err := readGit(ctx, repo, "rev-list", "--left-right", "--count", a+"..."+b, "--")
	if err != nil {
		return 0, 0, err
	}
	counts := strings.Fields(out)
	if len(counts) != 2 {
		return 0, 0, fmt.Errorf("git rev-list --left-right --count %s...%s: unexpected output %q", a, b, out)
	}
	if ahead, err = strconv.Atoi(counts[0]); err == nil {
		behind, err = strconv.Atoi(counts[1])
	}
	return ahead, behind, err
}
