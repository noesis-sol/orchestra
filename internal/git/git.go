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
	"sync"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Git is git as orchestra uses it: the state of checkouts and branches, worktrees, rebasing and
// merging finished tickets, the history the organs read, and what orchestra init reads and stages.
//
// A method that asks git a question returns git's error beside its answer when git fails to answer,
// which is not the same as no, none or "": the caller decides what to do without one. Only what is
// for people and the organs reads a failed git call as nothing: the texts (ShortStatus, OneLineLog,
// DiffStat, Subjects) "", ChangedFiles and LastCommit none.
type Git struct{}

// readGit runs a git command that changes nothing, in dir ("" for the working directory), within
// command.ReadLimit, and returns its output. It takes no optional locks (GIT_OPTIONAL_LOCKS=0): git
// would otherwise refresh a stale index under .git/index.lock, and anything writing to the repository
// meanwhile, such as the maintainer's own git or a worker committing on the base, fails on that lock.
// git diff refreshes the index regardless, so a diff against the work tree also passes noIndexRefresh.
func readGit(ctx context.Context, dir string, args ...string) (string, error) {
	return command.OutputWithEnv(ctx, command.ReadLimit, dir, []string{"GIT_OPTIONAL_LOCKS=0"}, "git", args...)
}

// answer is what a git command that answers yes or no by its exit status says, given its error:
// yes on exit 0, no on exit 1, and otherwise the error, git having failed to answer (a time limit, a
// missing commit, a broken repository).
func answer(err error) (bool, error) {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, err
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
	args := append([]string{"-C", dir, "status", "--porcelain"}, treePaths...)
	for _, p := range paths {
		args = append(args, ":(exclude,literal)"+p)
	}
	out, err := readGit(ctx, "", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// treePaths are the paths DirtyTree looks at, for git status: all but .claude/, .beads/ and
// .orchestra/.
var treePaths = []string{"--", ".", ":(exclude).claude", ":(exclude).beads", ":(exclude).orchestra"}

// DirtyTreeAndBranch is DirtyTree and the branch checked out in dir, from one git status: "" on a
// detached HEAD. The branch is named as git status names it, which unlike CurrentBranch never puts
// heads/ before it where a tag has the same name. It returns git's error when git can't tell.
func (Git) DirtyTreeAndBranch(ctx context.Context, dir string) (dirty, branch string, err error) {
	// --no-ahead-behind: the branch line says nothing of its upstream that git has to count.
	args := append([]string{"-C", dir, "status", "--porcelain", "--branch", "--no-ahead-behind"}, treePaths...)
	out, err := readGit(ctx, "", args...)
	if err != nil {
		return "", "", err
	}
	line, rest, _ := strings.Cut(out, "\n")
	branch, ok := branchOfStatus(line)
	if !ok {
		return "", "", errors.New("git status printed no branch line, but " + strconv.Quote(line))
	}
	return strings.TrimSpace(rest), branch, nil
}

// branchOfStatus reads the branch line of 'git status --porcelain --branch': "## main", "## main...origin/main
// [different]", "## No commits yet on main" (or "## Initial commit on main" before git 2.17), or "## HEAD (no
// branch)" when detached, which is "". A branch's name holds neither a space nor "..".
func branchOfStatus(line string) (string, bool) {
	line, ok := strings.CutPrefix(line, "## ")
	if !ok {
		return "", false
	}
	if line == "HEAD (no branch)" {
		return "", true
	}
	for _, unborn := range []string{"No commits yet on ", "Initial commit on "} {
		if b, ok := strings.CutPrefix(line, unborn); ok {
			return b, true
		}
	}
	line, _, _ = strings.Cut(line, "...")
	line, _, _ = strings.Cut(line, " ")
	return line, true
}

// DirtyWorktree lists uncommitted work in a ticket's worktree dir outside .orchestra/run/ (the
// ticket's scratch). Unlike DirtyTree it counts .claude/, .beads/ and the rest of .orchestra/: a
// change a worker left there is its ticket's work. It returns git's error when git can't tell, which
// is not the same as clean.
func (Git) DirtyWorktree(ctx context.Context, dir string) (string, error) {
	out, err := readGit(ctx, "", "-C", dir, "status", "--porcelain",
		"--", ".", ":(exclude).orchestra/run")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Changes maps each changed or untracked file in repo, under paths when any are given, to its
// two-letter code from 'git status --porcelain' ("M ", "??", …), listing each untracked file rather
// than its folder. It returns git's error when git can't tell, which is not the same as no changes.
func (Git) Changes(ctx context.Context, repo string, paths ...string) (map[string]string, error) {
	args := append([]string{"status", "--porcelain", "-z", "--untracked-files=all", "--"}, paths...)
	out, err := readGit(ctx, repo, args...)
	if err != nil {
		return nil, err
	}
	return parseStatus(out), nil
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
	if l, err := locate(ctx, repo); err == nil {
		return l.common, nil
	}
	// A bare repository, or a git directory, has no top level for locate to find; and the error is the
	// common directory's.
	out, err := readGit(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// TopLevel returns the top folder of the checkout dir is in ("" for the working directory), or
// git's error when dir is not in one.
func (Git) TopLevel(ctx context.Context, dir string) (string, error) {
	if l, err := locate(ctx, dir); err == nil {
		return l.top, nil
	}
	// The git directories locate asks for too may be what git can't read: the error is the top level's.
	out, err := readGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// LinkedWorktree reports whether repo is a linked worktree rather than the main checkout: its git
// directory is not the common one. It returns git's error when git can't tell.
func (Git) LinkedWorktree(ctx context.Context, repo string) (bool, error) {
	if l, err := locate(ctx, repo); err == nil {
		return l.gitDir != l.common, nil
	}
	// A bare repository, or a git directory, has no top level for locate to find, nor worktrees; and
	// the error is the git directories'.
	out, err := readGit(ctx, repo, "rev-parse", "--path-format=absolute", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return false, err
	}
	gitDir, commonDir, _ := strings.Cut(strings.TrimSpace(out), "\n")
	return gitDir != commonDir, nil
}

// location is where a checkout is, as git rev-parse gives it: its top folder, its own git directory
// (a linked worktree's is in the common one) and the git directory its worktrees share, all absolute.
type location struct {
	top, gitDir, common string
}

// locate returns where the checkout holding dir ("" for the working directory) is, from one git
// rev-parse, or from what a ctx made by Remember remembers. It returns git's error when dir is not
// in a checkout, as in a bare repository, or git can't tell any of the three: each caller then asks
// git for what it needs alone, for that answer or git's error about it.
func locate(ctx context.Context, dir string) (location, error) {
	mem, _ := ctx.Value(locationsKey{}).(*locations)
	if l, ok := mem.of(dir); ok {
		return l, nil
	}
	out, err := readGit(ctx, dir, "rev-parse", "--path-format=absolute",
		"--show-toplevel", "--absolute-git-dir", "--git-common-dir")
	if err != nil {
		return location{}, err
	}
	paths := strings.Split(strings.TrimSpace(out), "\n")
	if len(paths) != 3 {
		return location{}, errors.New("git rev-parse gave " + strconv.Itoa(len(paths)) +
			" lines for 3 paths: a path with a newline in it? " + strconv.Quote(out))
	}
	l := location{top: paths[0], gitDir: paths[1], common: paths[2]}
	mem.remember(dir, l)
	return l, nil
}

// Remember returns ctx with a memory, for one orchestra command, of where the checkouts it asks about
// are: then TopLevel, CommonDir and LinkedWorktree ask git about a folder once, rather than at each
// call, and once asked about a folder in a checkout, not about its top folder. Where a checkout is
// doesn't change while orchestra runs: it neither makes repositories nor moves them. A failure isn't
// remembered.
func Remember(ctx context.Context) context.Context {
	return context.WithValue(ctx, locationsKey{}, &locations{byDir: map[string]location{}})
}

// locationsKey is the key of a ctx's locations.
type locationsKey struct{}

// locations is what a ctx made by Remember remembers: each folder asked about, by its path, and
// each checkout's top folder. A nil *locations remembers nothing.
type locations struct {
	mu    sync.Mutex
	byDir map[string]location
}

// of returns what is remembered of dir, if anything. The working directory ("") isn't remembered:
// it is the process's, which may change.
func (m *locations) of(dir string) (location, bool) {
	if m == nil || dir == "" {
		return location{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.byDir[dir]
	return l, ok
}

// remember keeps l, the location of the checkout dir is in, for dir and for its top folder.
func (m *locations) remember(dir string, l location) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if dir != "" {
		m.byDir[dir] = l
	}
	m.byDir[l.top] = l
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

// HasBranch reports whether repo has the local branch. It returns git's error when git can't tell,
// which is not the same as no branch.
func (Git) HasBranch(ctx context.Context, repo, branch string) (bool, error) {
	_, err := readGit(ctx, repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return answer(err) // exit 1: no such ref
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
// as "<hash> <subject>" cut to 70 characters, or "" when none does; git's error when git can't list
// them.
func (g Git) CommitNaming(ctx context.Context, repo, base, branch, ticket string) (string, error) {
	return g.CommitNamingOn(ctx, repo, base+".."+branch, ticket)
}

// CommitNamingOn returns the latest commit reachable from rev (or in a range such as a..b) whose
// message names the ticket, as "<hash> <subject>" cut to 70 characters, or "" when none does; git's
// error when git can't list them.
func (Git) CommitNamingOn(ctx context.Context, repo, rev, ticket string) (string, error) {
	out, err := readGit(ctx, repo, "log", "--no-show-signature",
		"--fixed-strings", "--grep="+ticket, "--format=%h %s%x00%B%x1e", rev, "--")
	if err != nil {
		return "", err
	}
	return latestNaming(out, ticket), nil
}

// OwnCommits is CommitNaming and CountCommits of base..branch from one git log: the latest commit on
// branch, not on base, whose message names the ticket, as "<hash> <subject>" cut to 70 characters, or
// ""; and how many commits branch has that base hasn't. It returns git's error when git can't list
// them.
func (Git) OwnCommits(ctx context.Context, repo, base, branch, ticket string) (naming string, own int, err error) {
	out, err := readGit(ctx, repo, "log", "--no-show-signature", "--format=%h %s%x00%B%x1e", base+".."+branch, "--")
	if err != nil {
		return "", 0, err
	}
	return latestNaming(out, ticket), strings.Count(out, "\x1e"), nil
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

// IsAncestor reports whether ancestor is an ancestor of rev (or the same commit). It returns git's
// error when git can't tell, as for a rev that names no commit.
func (Git) IsAncestor(ctx context.Context, repo, ancestor, rev string) (bool, error) {
	_, err := readGit(ctx, repo, "merge-base", "--is-ancestor", ancestor, rev)
	return answer(err) // exit 1: not an ancestor
}

// MergeBase returns the best common ancestor of a and b, as a full hash, or "" when they have none.
// It returns git's error when git can't tell, as for a rev that names no commit.
func (Git) MergeBase(ctx context.Context, repo, a, b string) (string, error) {
	out, err := readGit(ctx, repo, "merge-base", a, b)
	if found, err := answer(err); !found {
		return "", err // exit 1: no common ancestor
	}
	return strings.TrimSpace(out), nil
}

// Head returns the commit rev points at, or "" when there is none, such as HEAD before the first
// commit. It returns git's error when git can't tell, which is not the same as none.
func (Git) Head(ctx context.Context, repo, rev string) (string, error) {
	// --verify: without it, git prints a rev it can't find back as if it were the commit.
	out, err := readGit(ctx, repo, "rev-parse", "-q", "--verify", rev)
	if found, err := answer(err); !found {
		return "", err // -q: exit 1 for a rev that names nothing
	}
	return strings.TrimSpace(out), nil
}

// FileAt returns the content of the file at path, from the repository's top, in commit rev, as
// committed. It returns git's error when rev has no such file, or git can't say.
func (Git) FileAt(ctx context.Context, repo, rev, path string) (string, error) {
	// cat-file, not show: the blob as committed, without the filters and text conversions show may apply.
	return readGit(ctx, repo, "cat-file", "blob", rev+":"+path)
}

// TrackedFiles lists the files git tracks in repo. It returns git's error when git can't list them.
func (Git) TrackedFiles(ctx context.Context, repo string) ([]string, error) {
	out, err := readGit(ctx, repo, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// Tracks reports whether git tracks the file at path, relative to repo. It returns git's error when
// git can't tell.
func (Git) Tracks(ctx context.Context, repo, path string) (bool, error) {
	_, err := readGit(ctx, repo, "ls-files", "--error-unmatch", "--", path)
	return answer(err) // exit 1: git tracks no such file
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
	return answer(err) // exit 1: none of the patterns matches
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
	head, err := g.Head(ctx, repo, "HEAD")
	switch {
	case err != nil:
		return "", err
	case head == "":
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

// ConflictedFiles lists the files left unmerged in worktree by a rebase that stopped, or none. It
// returns git's error when git can't list them.
func (Git) ConflictedFiles(ctx context.Context, worktree string) ([]string, error) {
	out, err := readGit(ctx, "", "-C", worktree, "-c", noIndexRefresh, "diff", "--name-only", "-z",
		"--diff-filter=U")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// RebaseInProgress reports whether a rebase has stopped in worktree and not been finished or
// aborted. It returns git's error when git can't tell, which is not the same as none.
func (Git) RebaseInProgress(ctx context.Context, worktree string) (bool, error) {
	for _, dir := range []string{"rebase-merge", "rebase-apply"} {
		out, err := readGit(ctx, "", "-C", worktree, "rev-parse", "--path-format=absolute", "--git-path", dir)
		if err != nil {
			return false, err
		}
		switch _, err := os.Stat(strings.TrimSpace(out)); {
		case err == nil:
			return true, nil
		case !errors.Is(err, os.ErrNotExist):
			return false, err
		}
	}
	return false, nil
}

// CountCommits counts the commits in revs, such as base..branch. It returns git's error when git
// can't count them.
func (Git) CountCommits(ctx context.Context, repo, revs string) (int, error) {
	out, err := readGit(ctx, repo, "rev-list", "--count", revs, "--")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// Unchanged reports whether branch, taken as a whole, changes nothing since it left base: its tree is
// the tree of their merge-base, as when a commit and its revert are all it has. It returns git's error
// when git can't tell, which is not the same as changed.
func (Git) Unchanged(ctx context.Context, repo, base, branch string) (bool, error) {
	// base...branch: from the merge-base to branch. --quiet exits 1 on a difference.
	_, err := readGit(ctx, repo, "diff", "--quiet", base+"..."+branch, "--")
	return answer(err)
}

// Holds reports whether base holds branch's changes already: merging branch into it would change
// nothing, as after the branch was squash-merged or its commits were cherry-picked onto base. It
// reports false when they conflict, and returns git's error when git can't tell, as git before 2.38
// can't (merge-tree --write-tree).
func (Git) Holds(ctx context.Context, repo, base, branch string) (bool, error) {
	// merge-tree prints the merged tree on its first line, and exits 1 on conflicts, as it does on a
	// rev it can't merge, for which it prints no tree.
	out, err := readGit(ctx, repo, "merge-tree", "--write-tree", base, branch)
	merged, _, _ := strings.Cut(out, "\n")
	merged = strings.TrimSpace(merged)
	switch clean, failed := answer(err); {
	case failed != nil || !clean && merged == "":
		return false, err
	case !clean:
		return false, nil // conflicts
	}
	tree, err := readGit(ctx, repo, "rev-parse", "--verify", base+"^{tree}")
	if err != nil {
		return false, err
	}
	return merged == strings.TrimSpace(tree), nil
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
