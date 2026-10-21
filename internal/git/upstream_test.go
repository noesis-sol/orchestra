package git

import (
	"context"
	"path/filepath"
	"testing"
)

// A branch's upstream is its remote-tracking ref, the remote and the branch there, whichever way the
// branch is named, and none for a branch that follows none; a fetch brings the remote's commits into
// the remote-tracking ref, and AheadBehind counts the commits each side has that the other hasn't.
func TestUpstreamFetchAndAheadBehind(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	for _, name := range []string{"main", "heads/main", "refs/heads/main"} {
		ref, remote, remoteRef, err := g.UpstreamOf(ctx, repo, name)
		if ref != "" || remote != "" || remoteRef != "" || err != nil {
			t.Errorf("UpstreamOf(%s) with no upstream = %q, %q, %q, %v; want none", name, ref, remote, remoteRef, err)
		}
	}

	bare := filepath.Join(t.TempDir(), "remote.git")
	git("init", "-q", "--bare", "-b", "main", bare)
	git("remote", "add", "origin", bare)
	git("push", "-q", "-u", "origin", "main")
	for _, name := range []string{"main", "heads/main", "refs/heads/main"} {
		ref, remote, remoteRef, err := g.UpstreamOf(ctx, repo, name)
		if ref != "refs/remotes/origin/main" || remote != "origin" || remoteRef != "refs/heads/main" || err != nil {
			t.Errorf("UpstreamOf(%s) = %q, %q, %q, %v; want refs/remotes/origin/main, origin, refs/heads/main",
				name, ref, remote, remoteRef, err)
		}
	}

	other := filepath.Join(t.TempDir(), "other")
	git("clone", "-q", bare, other)
	write(t, other, "b.md", "b\n")
	git("-C", other, "add", ".")
	git("-C", other, "commit", "-q", "-m", "Add b.md")
	git("-C", other, "push", "-q", "origin", "main")
	write(t, repo, "c.md", "c\n")
	git("add", ".")
	git("commit", "-q", "-m", "Add c.md")

	if _, err := g.Fetch(ctx, repo, "origin", "refs/heads/main"); err != nil {
		t.Fatal(err)
	}
	ahead, behind, err := g.AheadBehind(ctx, repo, "main", "refs/remotes/origin/main")
	if ahead != 1 || behind != 1 || err != nil {
		t.Errorf("AheadBehind after the fetch = %d, %d, %v; want 1 and 1", ahead, behind, err)
	}
	if _, _, err := g.AheadBehind(ctx, repo, "main", "no-such-branch"); err == nil {
		t.Error("AheadBehind with a branch git can't find should be git's error")
	}

	git("remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	if _, err := g.Fetch(ctx, repo, "origin", "refs/heads/main"); err == nil {
		t.Error("a fetch from a remote that isn't there should fail")
	}
}
