// Package git is orchestra's adapter for git: the state of checkouts and branches it checks
// before starting, merging or cleaning up a ticket.
package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Git is git as orchestra uses it: the state of checkouts and branches, worktrees, rebasing and
// merging finished tickets, the history the organs read, and what orchestra init reads and stages.
//
// What a failed git call reads as, method by method: IsAncestor, Unchanged, Holds, Tracks and
// HasBranch report false; RebaseInProgress true, as a rebase can't be shown to be over; DirtyWorktree
// git's error text, as uncommitted work it can't rule out; CountCommits -1; CommitNaming(On), MergeBase,
// Head and the texts for people (ShortStatus, OneLineLog, DiffStat, Subjects) ""; Changes,
// ChangedFiles, ConflictedFiles and TrackedFiles none. The other methods return git's error.
type Git struct{}

// readGit runs a git command that changes nothing, in dir ("" for the working directory), within
// command.ReadLimit, and returns its output. It takes no optional locks (GIT_OPTIONAL_LOCKS=0): git
// would otherwise refresh a stale index under .git/index.lock, and anything writing to the repository
// meanwhile, such as the maintainer's own git or a worker committing on the base, fails on that lock.
// git diff refreshes the index regardless, so a diff against the work tree also passes noIndexRefresh.
func readGit(ctx context.Context, dir string, args ...string) (string, error) {
	return command.OutputWithEnv(ctx, command.ReadLimit, dir, []string{"GIT_OPTIONAL_LOCKS=0"}, "git", args...)
}

// noIndexRefresh keeps a git diff against the work tree from refreshing the index, which it does
// whatever GIT_OPTIONAL_LOCKS says.
const noIndexRefresh = "diff.autoRefreshIndex=false"

// writeGit runs a git command that changes the repository, in dir ("" for the working directory),
// within command.WriteLimit, and returns its output.
func writeGit(ctx context.Context, dir string, args ...string) (string, error) {
	return command.Output(ctx, command.WriteLimit, dir, "git", args...)
}

// DirtyTree lists uncommitted work in checkout dir outside .claude/, .beads/ and .orchestra/
// (agent settings, tracker data, and orchestra's own files). It returns git's error when git can't
// tell, which is not the same as dirty.
func (g Git) DirtyTree(ctx context.Context, dir string) (string, error) {
	return g.DirtyTreeBesides(ctx, dir)
}

// DirtyTreeBesides is DirtyTree leaving out these paths too, relative to dir and taken literally:
// a file orchestra is about to commit, such as a spec.
func (Git) DirtyTreeBesides(ctx context.Context, dir string, paths ...string) (string, error) {
	args := []string{"-C", dir, "status", "--porcelain",
		"--", ".", ":(exclude).claude", ":(exclude).beads", ":(exclude).orchestra"}
	for _, p := range paths {
		args = append(args, ":(exclude,literal)"+p)
	}
	out, err := readGit(ctx, "", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// DirtyWorktree lists uncommitted work in a ticket's worktree dir outside .orchestra/run/ (the
// ticket's scratch). Unlike DirtyTree it counts .claude/, .beads/ and the rest of .orchestra/: a
// change a worker left there is its ticket's work. A failed git call counts as dirty.
func (Git) DirtyWorktree(ctx context.Context, dir string) string {
	out, err := readGit(ctx, "", "-C", dir, "status", "--porcelain",
		"--", ".", ":(exclude).orchestra/run")
	if err != nil {
		return err.Error()
	}
	return strings.TrimSpace(out)
}

// Changes maps each changed or untracked file in repo, under paths when any are given, to its
// two-letter code from 'git status --porcelain' ("M ", "??", …), listing each untracked file rather
// than its folder. It is empty when git can't tell.
func (Git) Changes(ctx context.Context, repo string, paths ...string) map[string]string {
	args := append([]string{"status", "--porcelain", "-z", "--untracked-files=all", "--"}, paths...)
	out, _ := readGit(ctx, repo, args...) // empty on failure, as documented
	return parseStatus(out)
}

// parseStatus reads 'git status --porcelain -z': a two-letter code, a space and the path, with a
// renamed or copied file's former path in the next field, whether it was renamed or copied in the
// index (R or C first) or in the work tree (R or C second, as after 'git add -N' of a moved file).
func parseStatus(out string) map[string]string {
	status := map[string]string{}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		status[e[3:]] = e[:2]
		if strings.ContainsAny(e[:2], "RC") {
			i++ // the path it was renamed or copied from
		}
	}
	return status
}

// CurrentBranch returns the branch checked out in repo, or "" on a detached HEAD. It returns git's
// error when git can't tell, which is not the same as detached.
func (Git) CurrentBranch(ctx context.Context, repo string) (string, error) {
	out, err := readGit(ctx, repo, "symbolic-ref", "--quiet", "--short", "HEAD")
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 { // --quiet: HEAD is not a branch
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CommonDir returns the absolute path of the git directory repo's worktrees share: the main
// checkout's .git, or a bare repository's own directory.
func (Git) CommonDir(ctx context.Context, repo string) (string, error) {
	out, err := readGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// TopLevel returns the top folder of the checkout dir is in ("" for the working directory), or
// git's error when dir is not in one.
func (Git) TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := readGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// LinkedWorktree reports whether repo is a linked worktree rather than the main checkout: its git
// directory is not the common one. It returns git's error when git can't tell.
func (g Git) LinkedWorktree(ctx context.Context, repo string) (bool, error) {
	gitDir, err := readGit(ctx, repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return false, err
	}
	commonDir, err := g.CommonDir(ctx, repo)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(gitDir) != commonDir, nil
}

// parseWorktreeOf returns the path of the worktree that has branch checked out, from
// 'git worktree list --porcelain'. A worktree git marks prunable (its folder is gone) doesn't count.
func parseWorktreeOf(porcelain, branch string) string {
	for entry := range strings.SplitSeq(porcelain, "\n\n") {
		path, found := "", false
		for line := range strings.SplitSeq(entry, "\n") {
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

// WorktreeOf returns the path of the worktree that has branch checked out, or "" when none has. The
// error is git failing to list the worktrees: then whether one has the branch is unknown.
func (Git) WorktreeOf(ctx context.Context, repo, branch string) (string, error) {
	out, err := readGit(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		return "", err
	}
	return parseWorktreeOf(out, branch), nil
}

// HasBranch reports whether repo has the local branch.
func (Git) HasBranch(ctx context.Context, repo, branch string) bool {
	_, err := readGit(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// BranchesStartingWith returns repo's local branches whose names start with prefix, which holds no
// glob characters and no slash after its last one (e.g. wt/orchestra-).
func (Git) BranchesStartingWith(ctx context.Context, repo, prefix string) ([]string, error) {
	out, err := readGit(ctx, repo, "for-each-ref", "--format=%(refname)",
		"refs/heads/"+prefix+"*")
	if err != nil {
		return nil, err
	}
	var branches []string
	for l := range strings.SplitSeq(out, "\n") {
		if b, ok := strings.CutPrefix(strings.TrimSpace(l), "refs/heads/"); ok {
			branches = append(branches, b)
		}
	}
	return branches, nil
}

// CommitNaming returns the latest commit on branch (not on base) whose message names the ticket,
// as "<hash> <subject>" cut to 70 characters, or "".
func (g Git) CommitNaming(ctx context.Context, repo, base, branch, ticket string) string {
	return g.CommitNamingOn(ctx, repo, base+".."+branch, ticket)
}

// CommitNamingOn returns the latest commit reachable from rev (or in a range such as a..b) whose
// message names the ticket, as "<hash> <subject>" cut to 70 characters, or "".
func (Git) CommitNamingOn(ctx context.Context, repo, rev, ticket string) string {
	// On failure none is found: nothing is merged.
	out, _ := readGit(ctx, repo, "log", "--no-show-signature",
		"--fixed-strings", "--grep="+ticket, "--format=%h %s%x00%B%x1e", rev, "--")
	return latestNaming(out, ticket)
}

// CommitNotNaming returns the oldest commit in revs, a range such as a..b, whose message doesn't name
// the ticket, as "<hash> <subject>" cut to 70 characters, or "" when each one names it; an error when
// git can't list them.
func (Git) CommitNotNaming(ctx context.Context, repo, revs, ticket string) (string, error) {
	out, err := readGit(ctx, repo, "log", "--no-show-signature", "--reverse",
		"--format=%h %s%x00%B%x1e", revs, "--")
	if err != nil {
		return "", err
	}
	for record := range strings.SplitSeq(out, "\x1e") {
		line, body, ok := strings.Cut(strings.TrimLeft(record, "\n"), "\x00")
		if ok && !namesID(body, ticket) {
			return cut70(line), nil
		}
	}
	return "", nil
}

// latestNaming picks, from 'git log --format=%h %s%x00%B%x1e' newest first, the first commit
// whose message names ticket as a whole ID, as "<hash> <subject>" cut to 70 characters, or "".
func latestNaming(log, ticket string) string {
	for record := range strings.SplitSeq(log, "\x1e") {
		line, body, _ := strings.Cut(strings.TrimLeft(record, "\n"), "\x00")
		if !namesID(body, ticket) {
			continue
		}
		return cut70(line)
	}
	return ""
}

// cut70 cuts a commit's "<hash> <subject>" line to 70 characters.
func cut70(line string) string {
	if r := []rune(line); len(r) > 70 {
		return string(r[:70])
	}
	return line
}

// namesID reports whether msg names id as a whole ID: not inside a longer one such as id3, xid,
// id-x or the child id.1.
func namesID(msg, id string) bool {
	if id == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(msg[i:], id)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(id)
		before := start == 0 || !isIDChar(msg[start-1]) && msg[start-1] != '-' && msg[start-1] != '.'
		after := end == len(msg) || !isIDChar(msg[end]) &&
			!((msg[end] == '-' || msg[end] == '.') && end+1 < len(msg) && isIDChar(msg[end+1]))
		if before && after {
			return true
		}
		i = start + 1
	}
}

// isIDChar reports whether c is a letter, digit or underscore, the characters an ID's parts are
// made of.
func isIDChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

// IsAncestor reports whether ancestor is an ancestor of rev (or the same commit).
func (Git) IsAncestor(ctx context.Context, repo, ancestor, rev string) bool {
	_, err := readGit(ctx, repo, "merge-base", "--is-ancestor", ancestor, rev)
	return err == nil
}

// MergeBase returns the best common ancestor of a and b, as a full hash, or "" when git can't find
// one.
func (Git) MergeBase(ctx context.Context, repo, a, b string) string {
	out, _ := readGit(ctx, repo, "merge-base", a, b) // "" on failure
	return strings.TrimSpace(out)
}

// Head returns the commit rev points at, or "" when there is none, such as HEAD before the first
// commit.
func (Git) Head(ctx context.Context, repo, rev string) string {
	// --verify: without it, git prints a rev it can't find back as if it were the commit.
	out, _ := readGit(ctx, repo, "rev-parse", "-q", "--verify", rev)
	return strings.TrimSpace(out) // "" on failure, as documented
}

// FileAt returns the content of the file at path, from the repository's top, in commit rev, as
// committed. It returns git's error when rev has no such file, or git can't say.
func (Git) FileAt(ctx context.Context, repo, rev, path string) (string, error) {
	// cat-file, not show: the blob as committed, without the filters and text conversions show may apply.
	return readGit(ctx, repo, "cat-file", "blob", rev+":"+path)
}

// TrackedFiles lists the files git tracks in repo, or nil if git can't.
func (Git) TrackedFiles(ctx context.Context, repo string) []string {
	out, err := readGit(ctx, repo, "ls-files", "-z")
	if err != nil {
		return nil
	}
	return splitNUL(out)
}

// Tracks reports whether git tracks the file at path, relative to repo.
func (Git) Tracks(ctx context.Context, repo, path string) bool {
	_, err := readGit(ctx, repo, "ls-files", "--error-unmatch", "--", path)
	return err == nil
}

// PathChanged reports whether the file at path, relative to repo, differs from what HEAD holds:
// changed, staged, untracked, or ignored by git. It returns git's error when git can't tell.
func (Git) PathChanged(ctx context.Context, repo, path string) (bool, error) {
	out, err := readGit(ctx, repo, "status", "--porcelain", "-z", "--untracked-files=all", "--ignored",
		"--", ":(literal)"+path)
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// Ignored reports whether git ignores the file at path, relative to repo, which need not exist:
// a .gitignore or the repository's info/exclude matches it, and git doesn't track it. It returns
// git's error when git can't tell.
func (Git) Ignored(ctx context.Context, repo, path string) (bool, error) {
	_, err := readGit(ctx, repo, "check-ignore", "-q", "--", path)
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1: // none of the patterns matches
		return false, nil
	}
	return false, err
}

// LastCommit returns the latest commit, as a full hash, that changed the file at path, relative
// to repo, on the branch checked out; "" when none has, or git can't tell.
func (Git) LastCommit(ctx context.Context, repo, path string) string {
	out, _ := readGit(ctx, repo, "log", "-1", "--no-show-signature", "--format=%H", "--", ":(literal)"+path)
	return strings.TrimSpace(out) // "" on failure, as documented
}

// PathDiff is the diff of the file at path, relative to repo, from commit from to commit to, as git
// diff prints it without colour or an external diff tool; "" when it is the same in both. It returns
// git's error when git can't say, as when it has no commit from.
func (Git) PathDiff(ctx context.Context, repo, from, to, path string) (string, error) {
	return readGit(ctx, repo, "diff", "--no-ext-diff", "--no-color", "--no-textconv", from, to, "--",
		":(literal)"+path)
}

// ChangedFiles lists the files that differ between commits from and to in repo, or every file in
// to when from is "" (to is the first commit). It lists none if git can't.
func (Git) ChangedFiles(ctx context.Context, repo, from, to string) []string {
	// --no-renames: a renamed file is listed under both its paths, so its old one is too.
	args := []string{"diff", "--no-renames", "--name-only", "-z", from, to, "--"}
	if from == "" {
		args = []string{"ls-tree", "-r", "--name-only", "-z", to}
	}
	out, _ := readGit(ctx, repo, args...) // none on failure, as documented
	return splitNUL(out)
}

// StagedFiles lists the files staged in repo: those whose index entry differs from HEAD's, or every
// staged file before the first commit. It returns git's error when git can't tell.
func (Git) StagedFiles(ctx context.Context, repo string) ([]string, error) {
	out, err := readGit(ctx, repo, "diff", "--cached", "--name-only", "-z", "--")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// Attribute returns the value .gitattributes gives attr for path, relative to repo: "unspecified",
// "set", "unset" or the value it is set to, as 'git check-attr' says.
func (Git) Attribute(ctx context.Context, repo, attr, path string) (string, error) {
	out, err := readGit(ctx, repo, "check-attr", "-z", attr, "--", path)
	if err != nil {
		return "", err
	}
	// -z: path, attr and value, each ended by a NUL, so a path with ": " in it reads right.
	if fields := strings.Split(out, "\x00"); len(fields) > 2 {
		return fields[2], nil
	}
	return "", errors.New("git check-attr printed no value for " + attr + " of " + path)
}

// splitNUL splits the output of a git command run with -z into its file names, which -z leaves
// unquoted, spaces and all.
func splitNUL(out string) []string {
	files := []string{}
	for f := range strings.SplitSeq(out, "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// Move moves the file at from to to, both relative to repo, with 'git mv', which stages the move.
func (Git) Move(ctx context.Context, repo, from, to string) (string, error) {
	return writeGit(ctx, repo, "mv", "--", from, to)
}

// CommitPath commits the file at path, relative to repo, and nothing else, on the branch checked out,
// with message msg, and returns the commit as a full hash. What else is staged stays staged and out
// of the commit. The repository's hooks before a commit don't run: one could add files to it.
func (g Git) CommitPath(ctx context.Context, repo, path, msg string) (string, error) {
	return g.CommitPaths(ctx, repo, msg, path)
}

// CommitPaths is CommitPath for the files at paths, each taken literally. In a repository with no
// commit yet, such as one git init has just made, it makes the first, on the branch HEAD names.
func (g Git) CommitPaths(ctx context.Context, repo, msg string, paths ...string) (string, error) {
	specs := make([]string, len(paths))
	for i, p := range paths {
		specs[i] = ":(literal)" + p
	}
	if _, err := writeGit(ctx, repo, append([]string{"add", "--"}, specs...)...); err != nil {
		return "", err
	}
	commit := append([]string{"commit", "--quiet", "--no-verify", "--only", "-m", msg, "--"}, specs...)
	if _, err := writeGit(ctx, repo, commit...); err != nil {
		return "", err
	}
	head := g.Head(ctx, repo, "HEAD")
	if head == "" {
		return "", errors.New("git committed " + strings.Join(paths, ", ") + " but HEAD names no commit")
	}
	return head, nil
}

// Prune forgets worktrees whose folders are gone.
func (Git) Prune(ctx context.Context, repo string) (string, error) {
	return writeGit(ctx, repo, "worktree", "prune")
}

// AddWorktree checks out the existing branch in a new worktree at path.
func (Git) AddWorktree(ctx context.Context, repo, path, branch string) (string, error) {
	return writeGit(ctx, repo, "worktree", "add", "--quiet", path, branch)
}

// NewWorktree creates branch from base, checked out in a new worktree at path.
func (Git) NewWorktree(ctx context.Context, repo, path, branch, base string) (string, error) {
	return writeGit(ctx, repo, "worktree", "add", "--quiet", "-b", branch, path, base)
}

// DetachedWorktree checks out rev, on no branch, in a new worktree at path: an empty folder, or none.
func (Git) DetachedWorktree(ctx context.Context, repo, path, rev string) (string, error) {
	return writeGit(ctx, repo, "worktree", "add", "--quiet", "--detach", path, rev)
}

// RemoveWorktree removes the worktree at path.
func (Git) RemoveWorktree(ctx context.Context, repo, path string) (string, error) {
	return writeGit(ctx, repo, "worktree", "remove", path)
}

// DeleteBranch deletes a branch that has been merged. Deleting a ref takes the repository's
// packed-refs.lock, which a commit, rebase or merge in any worktree also takes for a moment; git
// gives up after a second, too soon on a loaded machine while workers commit, so it waits longer.
func (Git) DeleteBranch(ctx context.Context, repo, branch string) (string, error) {
	return writeGit(ctx, repo, "-c", "core.packedRefsTimeout=10000", "branch", "-d", branch)
}

// Rebase rebases the branch checked out in worktree onto onto.
func (Git) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	return writeGit(ctx, "", "-C", worktree, "rebase", onto)
}

// AbortRebase gives up a rebase in progress in worktree.
func (Git) AbortRebase(ctx context.Context, worktree string) (string, error) {
	return writeGit(ctx, "", "-C", worktree, "rebase", "--abort")
}

// ConflictedFiles lists the files left unmerged in worktree by a rebase that stopped, or none.
func (Git) ConflictedFiles(ctx context.Context, worktree string) []string {
	// None on failure: the files only explain the conflict.
	out, _ := readGit(ctx, "", "-C", worktree, "-c", noIndexRefresh, "diff", "--name-only", "-z",
		"--diff-filter=U")
	return splitNUL(out)
}

// RebaseInProgress reports whether a rebase has stopped in worktree and not been finished or
// aborted. A git call that fails counts as one in progress: it can't be shown to be over.
func (Git) RebaseInProgress(ctx context.Context, worktree string) bool {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		out, err := readGit(ctx, "", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-path", dir)
		if err != nil {
			return true
		}
		if _, err := os.Stat(strings.TrimSpace(out)); !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	return false
}

// CountCommits counts the commits in revs, such as base..branch, or returns -1 if git can't.
func (Git) CountCommits(ctx context.Context, repo, revs string) int {
	out, err := readGit(ctx, repo, "rev-list", "--count", revs, "--")
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1
	}
	return n
}

// Unchanged reports whether branch, taken as a whole, changes nothing since it left base: its tree is
// the tree of their merge-base, as when a commit and its revert are all it has. It reports false
// when git can't tell.
func (Git) Unchanged(ctx context.Context, repo, base, branch string) bool {
	// base...branch: from the merge-base to branch. --quiet exits 1 on a difference, as on a failure.
	_, err := readGit(ctx, repo, "diff", "--quiet", base+"..."+branch, "--")
	return err == nil
}

// Holds reports whether base holds branch's changes already: merging branch into it would change
// nothing, as after the branch was squash-merged or its commits were cherry-picked onto base. It
// reports false when git can't tell, as git before 2.38 can't (merge-tree --write-tree).
func (g Git) Holds(ctx context.Context, repo, base, branch string) bool {
	// merge-tree prints the merged tree on its first line, and exits 1 on conflicts.
	out, err := readGit(ctx, repo, "merge-tree", "--write-tree", base, branch)
	if err != nil {
		return false
	}
	merged, _, _ := strings.Cut(out, "\n")
	tree := g.Head(ctx, repo, base+"^{tree}")
	return tree != "" && strings.TrimSpace(merged) == tree
}

// ResetBranch moves the branch checked out in worktree to rev, with its files.
func (Git) ResetBranch(ctx context.Context, worktree, rev string) (string, error) {
	return writeGit(ctx, "", "-C", worktree, "reset", "--hard", "--quiet", rev, "--")
}

// FastForward merges branch into the checked-out branch of repo, only if that is a fast-forward.
func (Git) FastForward(ctx context.Context, repo, branch string) (string, error) {
	return writeGit(ctx, repo, "merge", "--ff-only", "--quiet", branch)
}

// ShortStatus is 'git status --short' in worktree.
func (Git) ShortStatus(ctx context.Context, worktree string) string {
	// For people and the organs: "" on failure.
	out, _ := readGit(ctx, "", "-C", worktree, "status", "--short")
	return out
}

// OneLineLog is 'git log --oneline revs' in dir.
func (Git) OneLineLog(ctx context.Context, dir, revs string) string {
	// For people and the organs: "" on failure.
	out, _ := readGit(ctx, "", "-C", dir, "log", "--no-show-signature", "--oneline", revs, "--")
	return out
}

// DiffStat is the diff stat of worktree's uncommitted changes.
func (Git) DiffStat(ctx context.Context, worktree string) string {
	// For people and the organs: "" on failure.
	out, _ := readGit(ctx, "", "-C", worktree, "-c", noIndexRefresh, "diff", "--stat", "HEAD", "--")
	return out
}

// Subjects lists revs as "<hash> <subject>" lines, run in repo.
func (Git) Subjects(ctx context.Context, repo, revs string) string {
	// For people and the organs: "" on failure.
	out, _ := readGit(ctx, repo, "log", "--no-show-signature", "--format=%h %s", revs, "--")
	return out
}
