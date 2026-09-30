package git

import "testing"

func TestParseWorktreeOf(t *testing.T) {
	porcelain := "worktree /repo\nHEAD 111\nbranch refs/heads/main\n\n" +
		"worktree /wt/kinieta-abc\nHEAD 222\nbranch refs/heads/wt/kinieta-abc\n\n" +
		"worktree /wt/detached\nHEAD 333\ndetached\n"
	if got := parseWorktreeOf(porcelain, "wt/kinieta-abc"); got != "/wt/kinieta-abc" {
		t.Errorf("got %q", got)
	}
	if got := parseWorktreeOf(porcelain, "wt/kinieta"); got != "" {
		t.Errorf("a prefix must not match, got %q", got)
	}
}

func TestParseWorktreeOfSkipsPrunable(t *testing.T) {
	porcelain := "worktree /repo\nHEAD 111\nbranch refs/heads/main\n\n" +
		"worktree /wt/gone\nHEAD 222\nbranch refs/heads/wt/gone\nprunable gitdir file points to non-existent location\n"
	if got := parseWorktreeOf(porcelain, "wt/gone"); got != "" {
		t.Errorf("a worktree whose folder is gone must not be reused, got %q", got)
	}
}
