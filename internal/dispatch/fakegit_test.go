package dispatch

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
)

// ---- Git -----------------------------------------------------------------------------

// fakeCommit is a commit: a hash, its subject, and the files it adds, with the content of those it
// was given one for (see commitFile).
type fakeCommit struct {
	hash, subject string
	files         []string
	content       map[string]string
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
	replaying map[string][]fakeCommit // the history HEAD is at in these folders mid-rebase (see replayed)
	dirty     map[string]string       // the uncommitted changes in these worktree folders, as git status shows them
	unions    map[string]bool         // the files .gitattributes merges by union (merge=union)
	replaced  [][]fakeCommit          // the histories rebases and resets moved branches from, as git keeps their commits
}

func newFakeGit(base string) *fakeGit {
	g := &fakeGit{base: base, branches: map[string][]fakeCommit{}, worktrees: map[string]string{},
		conflicts: map[string][]string{}, rebasing: map[string]bool{}, dirty: map[string]string{}}
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

// leaveChange leaves file changed in the worktree, uncommitted.
func (g *fakeGit) leaveChange(wt, file string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dirty[wt] = " M " + file
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

// commitFile adds a commit to the branch the worktree is on that writes content to file.
func (g *fakeGit) commitFile(wt, subject, file, content string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	br, ok := g.worktrees[wt]
	if !ok {
		return fmt.Errorf("fatal: not a git repository: %s", wt)
	}
	c := g.newCommit(subject, file)
	c.content = map[string]string{file: content}
	g.branches[br] = append(g.branches[br], c)
	return nil
}

// switchTo puts the worktree on branch, made from the commit it is on if there is no such branch, as
// git switch -c does; "" detaches its HEAD there, as git switch --detach does.
func (g *fakeGit) switchTo(wt, branch string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.branches[branch]; !ok && branch != "" {
		g.branches[branch] = g.resolve(g.worktrees[wt])
	}
	g.worktrees[wt] = branch
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
	for _, cs := range slices.Concat(slices.Collect(maps.Values(g.branches)), g.replaced) {
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

// DirtyTreeAndBranch is a clean main checkout on the branch CurrentBranch says.
func (g *fakeGit) DirtyTreeAndBranch(ctx context.Context, dir string) (string, string, error) {
	br, err := g.CurrentBranch(ctx, dir)
	return "", br, err
}
func (g *fakeGit) DirtyWorktree(ctx context.Context, dir string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dirty[dir], nil
}
func (g *fakeGit) Changes(ctx context.Context, repo string, paths ...string) (map[string]string, error) {
	return nil, nil
}

// CurrentBranch is the branch the worktree is on, "" when detached, as it is while a rebase is stopped
// in it; the main checkout is on the base.
func (g *fakeGit) CurrentBranch(ctx context.Context, repo string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.rebasing[repo] {
		return "", nil
	}
	if br, ok := g.worktrees[repo]; ok {
		return br, nil
	}
	return g.base, nil
}

// Head is the commit rev is on; "HEAD" in a worktree is its branch's, or where its rebase has got to.
func (g *fakeGit) Head(ctx context.Context, repo, rev string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if br, ok := g.worktrees[repo]; ok && rev == "HEAD" {
		rev = br
		if cs := g.replaying[repo]; len(cs) > 0 {
			return cs[len(cs)-1].hash, nil
		}
	}
	if cs := g.resolve(rev); len(cs) > 0 {
		return cs[len(cs)-1].hash, nil
	}
	return "", nil
}

// head is the commit rev is on, "" for none.
func (g *fakeGit) head(rev string) string {
	h, _ := g.Head(context.Background(), "", rev) // the fake answers every question
	return h
}

// FileAt is the content the latest commit in rev's history that adds file gave it: "" when that
// commit was given none.
func (g *fakeGit) FileAt(ctx context.Context, repo, rev, file string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range slices.Backward(g.resolve(rev)) {
		if slices.Contains(c.files, file) {
			return c.content[file], nil
		}
	}
	return "", fmt.Errorf("fatal: path '%s' does not exist in '%s'", file, rev)
}

func (g *fakeGit) TrackedFiles(ctx context.Context, repo string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var files []string
	for _, c := range g.branches[g.base] {
		files = append(files, c.files...)
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

// mergeByUnion has .gitattributes merge file by union.
func (g *fakeGit) mergeByUnion(file string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.unions == nil {
		g.unions = map[string]bool{}
	}
	g.unions[file] = true
}

func (g *fakeGit) Attribute(ctx context.Context, repo, attr, path string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if attr == "merge" && g.unions[path] {
		return "union", nil
	}
	return "unspecified", nil
}

// Worktrees

func (g *fakeGit) WorktreeOf(ctx context.Context, repo, branch string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for wt, br := range g.worktrees {
		if br == branch {
			return wt, nil
		}
	}
	return "", nil
}

func (g *fakeGit) HasBranch(ctx context.Context, repo, branch string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.branches[branch]
	return ok, nil
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

// forceDelete deletes branch, merged or not, as git branch -D does.
func (g *fakeGit) forceDelete(branch string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.branches, branch)
}

// DeleteBranch deletes branch, refusing, as git branch -d does, one with commits the base hasn't.
func (g *fakeGit) DeleteBranch(ctx context.Context, repo, branch string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.branches[branch]; ok && !g.isAncestor(branch, g.base) {
		return "error: the branch '" + branch + "' is not fully merged", fmt.Errorf("git branch -d %s: exit status 1", branch)
	}
	delete(g.branches, branch)
	return "", nil
}

// Merger

// IsAncestor reports whether ancestor is in rev's history, or fails, as git does, when either names
// no commit.
func (g *fakeGit) IsAncestor(ctx context.Context, repo, ancestor, rev string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.noCommit(ancestor, rev); err != nil {
		return false, err
	}
	return g.isAncestor(ancestor, rev), nil
}

// noCommit is git's error for the first of revs that names no commit, or nil. The caller holds mu.
func (g *fakeGit) noCommit(revs ...string) error {
	for _, rev := range revs {
		if len(g.resolve(rev)) == 0 {
			return fmt.Errorf("fatal: Not a valid commit name %s", rev)
		}
	}
	return nil
}

// isAncestor reports whether ancestor is in rev's history. The caller holds mu.
func (g *fakeGit) isAncestor(ancestor, rev string) bool {
	a, r := g.resolve(ancestor), g.resolve(rev)
	return len(a) > 0 && len(a) <= len(r) && a[len(a)-1].hash == r[len(a)-1].hash
}

// MergeBase is the last commit a's and b's histories share.
func (g *fakeGit) MergeBase(ctx context.Context, repo, a, b string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	ra, rb := g.resolve(a), g.resolve(b)
	base := ""
	for i := 0; i < len(ra) && i < len(rb) && ra[i].hash == rb[i].hash; i++ {
		base = ra[i].hash
	}
	return base, nil
}

// CommitNaming is the latest commit on branch, not on base, whose subject begins with the ticket's ID.
func (g *fakeGit) CommitNaming(ctx context.Context, repo, base, branch, ticket string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return naming(g.only(base, branch), ticket), nil
}

// OwnCommits is CommitNaming and CountCommits of base..branch.
func (g *fakeGit) OwnCommits(ctx context.Context, repo, base, branch, ticket string) (string, int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	own := g.only(base, branch)
	return naming(own, ticket), len(own), nil
}

// CommitNamingOn is the latest commit in rev's history, in a range a..b or in rev^! (rev alone),
// whose subject begins with the ticket's ID.
func (g *fakeGit) CommitNamingOn(ctx context.Context, repo, rev, ticket string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if base, tip, ok := strings.Cut(rev, ".."); ok {
		return naming(g.only(base, tip), ticket), nil
	}
	if tip, ok := strings.CutSuffix(rev, "^!"); ok {
		cs := g.resolve(tip)
		return naming(cs[max(len(cs)-1, 0):], ticket), nil
	}
	return naming(g.resolve(rev), ticket), nil
}

// naming is the latest commit in rev's history naming the ticket, as CommitNamingOn gives it.
func (g *fakeGit) naming(rev, ticket string) string {
	n, _ := g.CommitNamingOn(context.Background(), "", rev, ticket) // the fake answers every question
	return n
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
		r := g.newCommit(c.subject, c.files...)
		r.content = c.content
		rebased = append(rebased, r)
	}
	g.replaced = append(g.replaced, g.branches[br])
	g.branches[br] = rebased
	return "", nil
}

func (g *fakeGit) AbortRebase(ctx context.Context, worktree string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.rebasing, worktree)
	delete(g.replaying, worktree)
	return "", nil
}

func (g *fakeGit) ConflictedFiles(ctx context.Context, worktree string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.rebasing[worktree] {
		return nil, nil
	}
	return g.conflicts[g.worktrees[worktree]], nil
}

func (g *fakeGit) RebaseInProgress(ctx context.Context, worktree string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.rebasing[worktree], nil
}

// inRebase reports whether a rebase is stopped in worktree.
func (g *fakeGit) inRebase(worktree string) bool {
	in, _ := g.RebaseInProgress(context.Background(), worktree) // the fake answers every question
	return in
}

func (g *fakeGit) CountCommits(ctx context.Context, repo, revs string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	base, rev, _ := strings.Cut(revs, "..")
	return len(g.only(base, rev)), nil
}

// Unchanged reports whether the commits of branch not in base's history add no files: a commit made
// with none stands for one that changes nothing, or one its revert undoes.
func (g *fakeGit) Unchanged(ctx context.Context, repo, base, branch string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.only(base, branch) {
		if len(c.files) > 0 {
			return false, nil
		}
	}
	return true, nil
}

// Holds reports whether each file the commits of branch not in base's history add is added by a
// commit of base's not in branch's made after them: base has the branch's changes, as after a
// squash merge. It fails, as git does, when base or branch names no commit.
func (g *fakeGit) Holds(ctx context.Context, repo, base, branch string) (bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.noCommit(base, branch); err != nil {
		return false, err
	}
	own := g.only(base, branch)
	var on []string
	for _, c := range g.only(branch, base) {
		if len(own) == 0 || c.hash > own[len(own)-1].hash { // hashes count up as commits are made
			on = append(on, c.files...)
		}
	}
	for _, c := range own {
		for _, f := range c.files {
			if !slices.Contains(on, f) {
				return false, nil
			}
		}
	}
	return true, nil
}

func (g *fakeGit) ResetBranch(ctx context.Context, worktree, rev string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	br := g.worktrees[worktree]
	to := g.resolve(rev)
	g.replaced = append(g.replaced, g.branches[br])
	g.branches[br] = to
	return "", nil
}

// FastForward moves the base to rev, a branch or a commit, which must be on top of it.
func (g *fakeGit) FastForward(ctx context.Context, repo, rev string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.isAncestor(g.base, rev) {
		return "fatal: Not possible to fast-forward, aborting.", fmt.Errorf("git merge --ff-only %s: exit status 128", rev)
	}
	g.branches[g.base] = g.resolve(rev)
	return "", nil
}

// History

func (g *fakeGit) ShortStatus(ctx context.Context, worktree string) string { return "" }
func (g *fakeGit) OneLineLog(ctx context.Context, dir, revs string) string { return "" }
func (g *fakeGit) DiffStat(ctx context.Context, worktree string) string    { return "" }

// ChangedFiles is the files the commits of to not in from's history add: what differs between them
// when to is from with commits on top.
func (g *fakeGit) ChangedFiles(ctx context.Context, repo, from, to string) ([]string, error) {
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
	return files, nil
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
