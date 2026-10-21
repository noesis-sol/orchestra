package git

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// FetchLimit is how long a fetch of a branch's upstream may take before it is stopped: longer than a
// read, as it reaches the network, and short enough that a remote that doesn't answer, or an ssh
// waiting on a passphrase nobody types, doesn't hold a run's start for long.
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
// Nothing is asked of anyone: git asks for no credentials (GIT_TERMINAL_PROMPT=0), failing instead.
func (Git) Fetch(ctx context.Context, repo, remote, remoteRef string) (string, error) {
	return command.OutputWithEnv(ctx, FetchLimit, repo, []string{"GIT_TERMINAL_PROMPT=0"},
		"git", "fetch", "--quiet", "--no-tags", remote, remoteRef)
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
