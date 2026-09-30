// Package git is orchestra's adapter for git: the state of checkouts and branches it checks
// before starting, merging or cleaning up a ticket.
package git

import (
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Git is git as orchestra uses it: the state of checkouts and branches, worktrees, rebasing and
// merging finished tickets, and the history the organs read.
type Git struct{}

// DirtyTree lists uncommitted work in checkout dir outside .claude/, .beads/ and .orchestra/
// (agent settings, tracker data, and orchestra's own files). A failed git call counts as dirty.
func (Git) DirtyTree(dir string) string {
	out, err := command.Output("", "git", "-C", dir, "status", "--porcelain", "--", ".", ":(exclude).claude", ":(exclude).beads", ":(exclude).orchestra")
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(out)
}

// CurrentBranch returns the branch checked out in repo, or "" on a detached HEAD.
func (Git) CurrentBranch(repo string) string {
	out, _ := command.Output(repo, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	return strings.TrimSpace(out)
}

// parseWorktreeOf returns the path of the worktree that has branch checked out, from
// 'git worktree list --porcelain'. A worktree git marks prunable (its folder is gone) doesn't count.
func parseWorktreeOf(porcelain, branch string) string {
	for _, entry := range strings.Split(porcelain, "\n\n") {
		path, found := "", false
		for _, line := range strings.Split(entry, "\n") {
			if p, ok := strings.CutPrefix(line, "worktree "); ok {
				path = p
			} else if line == "branch refs/heads/"+branch {
				found = true
			} else if line == "prunable" || strings.HasPrefix(line, "prunable ") {
				found = false
				break
			}
		}
		if found {
			return path
		}
	}
	return ""
}

// WorktreeOf returns the path of the worktree that has branch checked out, or "".
func (Git) WorktreeOf(repo, branch string) string {
	out, _ := command.Output(repo, "git", "worktree", "list", "--porcelain")
	return parseWorktreeOf(out, branch)
}

// HasBranch reports whether repo has the local branch.
func (Git) HasBranch(repo, branch string) bool {
	_, err := command.Output(repo, "git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// CommitNaming returns the latest commit on branch (not on base) whose message names the ticket,
// as "<hash> <subject>" cut to 70 characters, or "".
func (g Git) CommitNaming(repo, base, branch, ticket string) string {
	return g.CommitNamingOn(repo, base+".."+branch, ticket)
}

// CommitNamingOn returns the latest commit reachable from rev (or in a range such as a..b) whose
// message names the ticket, as "<hash> <subject>" cut to 70 characters, or "".
func (Git) CommitNamingOn(repo, rev, ticket string) string {
	out, _ := command.Output(repo, "git", "log", "--oneline", "-1", "--grep="+ticket, rev)
	c := strings.TrimSpace(out)
	if r := []rune(c); len(r) > 70 {
		c = string(r[:70])
	}
	return c
}

// IsAncestor reports whether ancestor is an ancestor of rev (or the same commit).
func (Git) IsAncestor(repo, ancestor, rev string) bool {
	_, err := command.Output(repo, "git", "merge-base", "--is-ancestor", ancestor, rev)
	return err == nil
}

// Head returns the commit rev points at, or "".
func (Git) Head(repo, rev string) string {
	out, _ := command.Output(repo, "git", "rev-parse", rev)
	return strings.TrimSpace(out)
}

// Prune forgets worktrees whose folders are gone.
func (Git) Prune(repo string) {
	command.Output(repo, "git", "worktree", "prune")
}

// AddWorktree checks out the existing branch in a new worktree at path.
func (Git) AddWorktree(repo, path, branch string) (string, error) {
	return command.Output(repo, "git", "worktree", "add", "--quiet", path, branch)
}

// NewWorktree creates branch from base, checked out in a new worktree at path.
func (Git) NewWorktree(repo, path, branch, base string) (string, error) {
	return command.Output(repo, "git", "worktree", "add", "--quiet", "-b", branch, path, base)
}

// RemoveWorktree removes the worktree at path.
func (Git) RemoveWorktree(repo, path string) (string, error) {
	return command.Output(repo, "git", "worktree", "remove", path)
}

// DeleteBranch deletes a branch that has been merged.
func (Git) DeleteBranch(repo, branch string) (string, error) {
	return command.Output(repo, "git", "branch", "-d", branch)
}

// Rebase rebases the branch checked out in worktree onto onto.
func (Git) Rebase(worktree, onto string) (string, error) {
	return command.Output("", "git", "-C", worktree, "rebase", onto)
}

// AbortRebase gives up a rebase in progress in worktree.
func (Git) AbortRebase(worktree string) {
	command.Output("", "git", "-C", worktree, "rebase", "--abort")
}

// FastForward merges branch into the checked-out branch of repo, only if that is a fast-forward.
func (Git) FastForward(repo, branch string) (string, error) {
	return command.Output(repo, "git", "merge", "--ff-only", "--quiet", branch)
}

// ShortStatus is 'git status --short' in worktree.
func (Git) ShortStatus(worktree string) string {
	out, _ := command.Output("", "git", "-C", worktree, "status", "--short")
	return out
}

// OneLineLog is 'git log --oneline revs' in dir.
func (Git) OneLineLog(dir, revs string) string {
	out, _ := command.Output("", "git", "-C", dir, "log", "--oneline", revs)
	return out
}

// DiffStat is the diff stat of worktree's uncommitted changes.
func (Git) DiffStat(worktree string) string {
	out, _ := command.Output("", "git", "-C", worktree, "diff", "--stat", "HEAD")
	return out
}

// Subjects lists revs as "<hash> <subject>" lines, run in repo.
func (Git) Subjects(repo, revs string) string {
	out, _ := command.Output(repo, "git", "log", "--format=%h %s", revs)
	return out
}
