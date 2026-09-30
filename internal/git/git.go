// Package git is orchestra's adapter for git: the state of checkouts and branches it checks
// before starting, merging or cleaning up a ticket.
package git

import (
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// DirtyTree lists uncommitted work in checkout dir outside .claude/, .beads/ and .orchestra/
// (agent settings, tracker data, and orchestra's own files). A failed git call counts as dirty.
func DirtyTree(dir string) string {
	out, err := command.Output("", "git", "-C", dir, "status", "--porcelain", "--", ".", ":(exclude).claude", ":(exclude).beads", ":(exclude).orchestra")
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(out)
}

// CurrentBranch returns the branch checked out in repo, or "" on a detached HEAD.
func CurrentBranch(repo string) string {
	out, _ := command.Output(repo, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	return strings.TrimSpace(out)
}

// parseWorktreeOf returns the path of the worktree that has branch checked out, from
// 'git worktree list --porcelain'.
func parseWorktreeOf(porcelain, branch string) string {
	path := ""
	for _, line := range strings.Split(porcelain, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			path = p
		} else if line == "branch refs/heads/"+branch {
			return path
		}
	}
	return ""
}

// WorktreeOf returns the path of the worktree that has branch checked out, or "".
func WorktreeOf(repo, branch string) string {
	out, _ := command.Output(repo, "git", "worktree", "list", "--porcelain")
	return parseWorktreeOf(out, branch)
}

// HasBranch reports whether repo has the local branch.
func HasBranch(repo, branch string) bool {
	_, err := command.Output(repo, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// CommitNaming returns the latest commit on branch (not on base) whose message names the ticket,
// as "<hash> <subject>" cut to 70 characters, or "".
func CommitNaming(repo, base, branch, ticket string) string {
	out, _ := command.Output(repo, "git", "log", "--oneline", "-1", "--grep="+ticket, base+".."+branch)
	c := strings.TrimSpace(out)
	if r := []rune(c); len(r) > 70 {
		c = string(r[:70])
	}
	return c
}

// IsAncestor reports whether ancestor is an ancestor of rev (or the same commit).
func IsAncestor(repo, ancestor, rev string) bool {
	_, err := command.Output(repo, "git", "merge-base", "--is-ancestor", ancestor, rev)
	return err == nil
}
