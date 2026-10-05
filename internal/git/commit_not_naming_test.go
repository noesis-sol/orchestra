package git

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// CommitNotNaming finds the oldest commit in a range whose message doesn't name the ticket as a
// whole ID, its body included.
func TestCommitNotNaming(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit(t, repo, "Start")
	if out, err := exec.Command("git", "-C", repo, "checkout", "--quiet", "-b", "wt/x-12").CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %v: %s", err, out)
	}
	commit(t, repo, "x-12: Fix the loop")
	commit(t, repo, "Update the admin mock\n\nFor x-12.")
	g, ctx := Git{}, context.Background()
	if got, err := g.CommitNotNaming(ctx, repo, "main..wt/x-12", "x-12"); err != nil || got != "" {
		t.Errorf("each commit names x-12: got %q, %v", got, err)
	}
	commit(t, repo, "x-123: Fix a follow-up")
	commit(t, repo, "Tidy up")
	if got, err := g.CommitNotNaming(ctx, repo, "main..wt/x-12", "x-12"); err != nil || !strings.HasSuffix(got, " x-123: Fix a follow-up") {
		t.Errorf("got %q, %v; want the commit naming x-123", got, err)
	}
	if _, err := g.CommitNotNaming(ctx, repo, "main..no-such-branch", "x-12"); err == nil {
		t.Error("a range git can't list should be an error")
	}
}
