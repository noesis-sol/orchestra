package dispatch

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

// ---- Git -----------------------------------------------------------------------------

// fakeCommit is a commit: a hash, its subject, and the files it adds.
type fakeCommit struct {
	hash, subject string
	files         []string
}

// fakeGit is one repository in memory, for scenarios whose subject is timing rather than git: no
// command runs, so a synctest bubble's clock isn't held up by one. A branch is its commits, oldest
// first; a worktree is an empty folder on a branch. A rebase replays the branch's own commits onto
// the base with new hashes, or, for a branch given conflicts, stops until it is aborted. The main
// checkout is always clean and on the base.
type fakeGit struct {
	mu        sync.Mutex
	base      string
	made      int                     // commits made, for their hashes
	branches  map[string][]fakeCommit // by name
	worktrees map[string]string       // the branch each worktree folder is on
	conflicts map[string][]string     // the files each of these branches' rebases stops on
	rebasing  map[string]bool         // worktree folders with a rebase stopped in them
}

func newFakeGit(base string) *fakeGit {
	g := &fakeGit{base: base, branches: map[string][]fakeCommit{}, worktrees: map[string]string{},
		conflicts: map[string][]string{}, rebasing: map[string]bool{}}
	g.branches[base] = []fakeCommit{g.newCommit("init", ".gitignore")}
	return g
}

// newCommit makes a commit with a fresh hash. The caller holds mu.
func (g *fakeGit) newCommit(subject string, files ...string) fakeCommit {
	g.made++
	return fakeCommit{hash: fmt.Sprintf("c%06d", g.made), subject: subject, files: files}
}

// commit adds a commit to the branch.
func (g *fakeGit) commit(branch, subject string, files ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.branches[branch] = append(g.branches[branch], g.newCommit(subject, files...))
}

// commitIn adds a commit to the branch the worktree is on.
func (g *fakeGit) commitIn(wt, subject string, files ...string) error {
	g.mu.Lock()
	br, ok := g.worktrees[wt]
	g.mu.Unlock()
	if !ok {
		return fmt.Errorf("fatal: not a git repository: %s", wt)
	}
	g.commit(br, subject, files...)
	return nil
}

// conflict makes every rebase of branch stop on conflicts in files.
func (g *fakeGit) conflict(branch string, files ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.conflicts[branch] = files
}

// log is the branch's commits as git log --oneline gives them, newest first.
func (g *fakeGit) log(branch string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var b strings.Builder
	for _, c := range slices.Backward(g.branches[branch]) {
		b.WriteString(c.hash + " " + c.subject + "\n")
	}
	return b.String()
}

// branchList is the branches, sorted.
func (g *fakeGit) branchList() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var l []string
	for br := range g.branches {
		l = append(l, br)
	}
	slices.Sort(l)
	return l
}

// resolve is the history of rev, a branch or a commit hash, oldest first; nil if there is none.
// The caller holds mu.
func (g *fakeGit) resolve(rev string) []fakeCommit {
	if cs, ok := g.branches[rev]; ok {
		return slices.Clone(cs)
	}
	for _, cs := range g.branches {
		for i, c := range cs {
			if c.hash == rev {
				return slices.Clone(cs[:i+1])
			}
		}
	}
	return nil
}

// only is the commits of rev not in base's history ("base..rev"), oldest first. The caller holds mu.
func (g *fakeGit) only(base, rev string) []fakeCommit {
	in := map[string]bool{}
	for _, c := range g.resolve(base) {
		in[c.hash] = true
	}
	var l []fakeCommit
	for _, c := range g.resolve(rev) {
		if !in[c.hash] {
			l = append(l, c)
		}
	}
	return l
}

// Checkout

func (g *fakeGit) DirtyTree(ctx context.Context, dir string) (string, error) { return "", nil }
func (g *fakeGit) DirtyWorktree(ctx context.Context, dir string) string      { return "" }

func (g *fakeGit) CurrentBranch(ctx context.Context, repo string) (string, error) { return g.base, nil }

func (g *fakeGit) Head(ctx context.Context, repo, rev string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if cs := g.resolve(rev); len(cs) > 0 {
		return cs[len(cs)-1].hash
	}
	return ""
}

func (g *fakeGit) TrackedFiles(ctx context.Context, repo string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var files []string
	for _, c := range g.branches[g.base] {
		files = append(files, c.files...)
	}
	slices.Sort(files)
	return slices.Compact(files)
}

// Worktrees

func (g *fakeGit) WorktreeOf(ctx context.Context, repo, branch string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	for wt, br := range g.worktrees {
		if br == branch {
			return wt
		}
	}
	return ""
}

func (g *fakeGit) HasBranch(ctx context.Context, repo, branch string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.branches[branch]
	return ok
}

// Prune forgets the worktrees whose folders are gone.
func (g *fakeGit) Prune(ctx context.Context, repo string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for wt := range g.worktrees {
		if !exists(wt) {
			delete(g.worktrees, wt)
		}
	}
	return "", nil
}

func (g *fakeGit) AddWorktree(ctx context.Context, repo, path, branch string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.branches[branch]; !ok {
		return "", fmt.Errorf("fatal: invalid reference: %s", branch)
	}
	return "", g.addWorktree(path, branch)
}

func (g *fakeGit) NewWorktree(ctx context.Context, repo, path, branch, base string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.branches[branch]; ok {
		return "", fmt.Errorf("fatal: a branch named '%s' already exists", branch)
	}
	g.branches[branch] = g.resolve(base)
	return "", g.addWorktree(path, branch)
}

func (g *fakeGit) DetachedWorktree(ctx context.Context, repo, path, rev string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.resolve(rev)) == 0 {
		return "", fmt.Errorf("fatal: invalid reference: %s", rev)
	}
	return "", g.addWorktree(path, "")
}

// addWorktree makes the worktree's folder. The caller holds mu.
func (g *fakeGit) addWorktree(path, branch string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return err
	}
	g.worktrees[path] = branch
	return nil
}

func (g *fakeGit) RemoveWorktree(ctx context.Context, repo, path string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.worktrees, path)
	return "", os.RemoveAll(path)
}

func (g *fakeGit) DeleteBranch(ctx context.Context, repo, branch string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.branches, branch)
	return "", nil
}

// Merger

func (g *fakeGit) IsAncestor(ctx context.Context, repo, ancestor, rev string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.isAncestor(ancestor, rev)
}

// isAncestor reports whether ancestor is in rev's history. The caller holds mu.
func (g *fakeGit) isAncestor(ancestor, rev string) bool {
	a, r := g.resolve(ancestor), g.resolve(rev)
	return len(a) > 0 && len(a) <= len(r) && a[len(a)-1].hash == r[len(a)-1].hash
}

// CommitNaming is the latest commit on branch, not on base, whose subject begins with the ticket's ID.
func (g *fakeGit) CommitNaming(ctx context.Context, repo, base, branch, ticket string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return naming(g.only(base, branch), ticket)
}

func (g *fakeGit) CommitNamingOn(ctx context.Context, repo, rev, ticket string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return naming(g.resolve(rev), ticket)
}

// CommitNotNaming is the oldest commit in revs whose subject doesn't begin with the ticket's ID.
func (g *fakeGit) CommitNotNaming(ctx context.Context, repo, revs, ticket string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	base, rev, _ := strings.Cut(revs, "..")
	for _, c := range g.only(base, rev) {
		if !strings.HasPrefix(c.subject, ticket+":") {
			return c.hash + " " + c.subject, nil
		}
	}
	return "", nil
}

// naming is the latest of the commits whose subject begins with the ticket's ID, as "<hash> <subject>".
func naming(cs []fakeCommit, ticket string) string {
	for _, c := range slices.Backward(cs) {
		if strings.HasPrefix(c.subject, ticket+":") {
			return c.hash + " " + c.subject
		}
	}
	return ""
}

func (g *fakeGit) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	br := g.worktrees[worktree]
	if files, ok := g.conflicts[br]; ok {
		g.rebasing[worktree] = true
		return "CONFLICT (content): Merge conflict in " + strings.Join(files, ", "), fmt.Errorf("git rebase %s: exit status 1", onto)
	}
	rebased := g.resolve(onto)
	for _, c := range g.only(onto, br) {
		rebased = append(rebased, g.newCommit(c.subject, c.files...))
	}
	g.branches[br] = rebased
	return "", nil
}

func (g *fakeGit) AbortRebase(ctx context.Context, worktree string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.rebasing, worktree)
	return "", nil
}

func (g *fakeGit) ConflictedFiles(ctx context.Context, worktree string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.rebasing[worktree] {
		return nil
	}
	return g.conflicts[g.worktrees[worktree]]
}

func (g *fakeGit) RebaseInProgress(ctx context.Context, worktree string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rebasing[worktree]
}

func (g *fakeGit) CountCommits(ctx context.Context, repo, revs string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	base, rev, _ := strings.Cut(revs, "..")
	return len(g.only(base, rev))
}

// Unchanged reports whether the commits of branch not in base's history add no files: a commit made
// with none stands for one that changes nothing, or one its revert undoes.
func (g *fakeGit) Unchanged(ctx context.Context, repo, base, branch string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.only(base, branch) {
		if len(c.files) > 0 {
			return false
		}
	}
	return true
}

func (g *fakeGit) ResetBranch(ctx context.Context, worktree, rev string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.branches[g.worktrees[worktree]] = g.resolve(rev)
	return "", nil
}

// FastForward moves the base to branch, which must be on top of it.
func (g *fakeGit) FastForward(ctx context.Context, repo, branch string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.isAncestor(g.base, branch) {
		return "fatal: Not possible to fast-forward, aborting.", fmt.Errorf("git merge --ff-only %s: exit status 128", branch)
	}
	g.branches[g.base] = g.resolve(branch)
	return "", nil
}

// History

func (g *fakeGit) ShortStatus(ctx context.Context, worktree string) string { return "" }
func (g *fakeGit) OneLineLog(ctx context.Context, dir, revs string) string { return "" }
func (g *fakeGit) DiffStat(ctx context.Context, worktree string) string    { return "" }

// ChangedFiles is the files the commits of to not in from's history add: what differs between them
// when to is from with commits on top.
func (g *fakeGit) ChangedFiles(ctx context.Context, repo, from, to string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var files []string
	for _, c := range g.only(from, to) {
		for _, f := range c.files {
			if !slices.Contains(files, f) {
				files = append(files, f)
			}
		}
	}
	return files
}

func (g *fakeGit) Subjects(ctx context.Context, repo, revs string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	base, rev, _ := strings.Cut(revs, "..")
	var l []string
	for _, c := range slices.Backward(g.only(base, rev)) {
		l = append(l, c.subject)
	}
	return strings.Join(l, "\n")
}
