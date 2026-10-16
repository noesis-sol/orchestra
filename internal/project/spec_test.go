package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// specCommit is what a spec run's commit did in repo: CommitSpec's results, and the subject and the
// files of HEAD.
type specCommit struct {
	spec      Spec
	committed bool
	subject   string
	files     string // HEAD's files, one per line
}

// commitSpec runs CommitSpec on file in repo and reads HEAD after it.
func commitSpec(t *testing.T, repo, file string, git func(dir string, args ...string) string) specCommit {
	t.Helper()
	s, committed, err := CommitSpec(context.Background(), repo, file)
	if err != nil {
		t.Fatal(err)
	}
	return specCommit{spec: s, committed: committed,
		subject: strings.TrimSpace(git(repo, "log", "-1", "--format=%s")),
		files:   strings.TrimSpace(git(repo, "show", "--name-only", "--format=", "HEAD"))}
}

// head is the commit HEAD names, in full.
func head(t *testing.T, repo string, git func(dir string, args ...string) string) string {
	t.Helper()
	return strings.TrimSpace(git(repo, "rev-parse", "HEAD"))
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// besides puts in repo what a spec's commit must leave out: a file staged, one untracked, and one
// changed but not staged.
func besides(t *testing.T, repo string, git func(dir string, args ...string) string) {
	t.Helper()
	for name, text := range map[string]string{"tracked.txt": "one\n", "staged.txt": "staged\n", "loose.txt": "loose\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(repo, "add", "tracked.txt")
	git(repo, "commit", "-q", "-m", "tracked")
	git(repo, "add", "staged.txt")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// leftAsTheyWere checks that what besides put in repo is still uncommitted, as it was.
func leftAsTheyWere(t *testing.T, repo string, git func(dir string, args ...string) string) {
	t.Helper()
	status := git(repo, "status", "--porcelain", "--", "tracked.txt", "staged.txt", "loose.txt")
	for _, want := range []string{" M tracked.txt\n", "A  staged.txt\n", "?? loose.txt\n"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
}

// A spec outside the repository is copied into .orchestra/specs/ and committed there once, alone;
// run again unchanged it commits nothing, and changed it makes an update commit.
func TestCommitSpecCopiesASpecFromOutside(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: commits with real git")
	}
	repo, git := gitRepo(t)
	besides(t, repo, git)
	file := filepath.Join(t.TempDir(), "app.md")
	if err := os.WriteFile(file, []byte("# An app\n\nIt lists things.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := commitSpec(t, repo, file, git)
	const rel = ".orchestra/specs/app.md"
	if !first.committed || first.subject != "orchestra: add spec app.md" || first.files != rel {
		t.Errorf("first run: committed %v, %q with files %q", first.committed, first.subject, first.files)
	}
	want := Spec{Path: rel, SHA256: sha256Hex("# An app\n\nIt lists things.\n"), Commit: head(t, repo, git)}
	if first.spec != want {
		t.Errorf("first run: %+v, want %+v", first.spec, want)
	}
	if got := read(t, filepath.Join(repo, ".orchestra", "specs", "app.md")); got != "# An app\n\nIt lists things.\n" {
		t.Errorf("the copy holds %q", got)
	}
	if body := git(repo, "log", "-1", "--format=%b"); !strings.Contains(body, "sha256 "+want.SHA256) {
		t.Errorf("the commit's message doesn't give the SHA-256:\n%s", body)
	}
	leftAsTheyWere(t, repo, git)

	again := commitSpec(t, repo, file, git)
	if again.committed || again.spec != want || head(t, repo, git) != want.Commit {
		t.Errorf("unchanged: committed %v, %+v, HEAD %s; want no commit and %+v", again.committed, again.spec,
			head(t, repo, git), want)
	}

	if err := os.WriteFile(file, []byte("# An app\n\nIt lists and sorts things.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := commitSpec(t, repo, file, git)
	if !changed.committed || changed.subject != "orchestra: update spec app.md" || changed.files != rel {
		t.Errorf("changed: committed %v, %q with files %q", changed.committed, changed.subject, changed.files)
	}
	if changed.spec.Commit != head(t, repo, git) || changed.spec.Commit == want.Commit ||
		changed.spec.SHA256 != sha256Hex("# An app\n\nIt lists and sorts things.\n") {
		t.Errorf("changed: %+v, HEAD %s", changed.spec, head(t, repo, git))
	}
	leftAsTheyWere(t, repo, git)
}

// A spec inside the repository is committed where it is, alone: untracked, then modified; committed
// and unchanged, it isn't committed again, and its commit is the one that last changed it.
func TestCommitSpecKeepsASpecInsideWhereItIs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: commits with real git")
	}
	repo, git := gitRepo(t)
	besides(t, repo, git)
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(repo, "docs", "app spec.md")
	if err := os.WriteFile(file, []byte("Build a lister.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	untracked := commitSpec(t, repo, file, git)
	if !untracked.committed || untracked.subject != "orchestra: add spec app spec.md" ||
		untracked.files != "docs/app spec.md" || untracked.spec.Path != "docs/app spec.md" {
		t.Errorf("untracked: %+v", untracked)
	}
	if _, err := os.Stat(filepath.Join(repo, ".orchestra", "specs")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a spec inside the repository was copied: %v", err)
	}
	added := head(t, repo, git)

	git(repo, "commit", "-q", "--allow-empty", "--only", "-m", "later") // leaves staged.txt staged
	unchanged := commitSpec(t, repo, file, git)
	if unchanged.committed || unchanged.spec.Commit != added {
		t.Errorf("unchanged: committed %v, commit %s; want none and %s", unchanged.committed, unchanged.spec.Commit, added)
	}

	if err := os.WriteFile(file, []byte("Build a lister that sorts.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	modified := commitSpec(t, repo, file, git)
	if !modified.committed || modified.subject != "orchestra: update spec app spec.md" ||
		modified.files != "docs/app spec.md" || modified.spec.Commit != head(t, repo, git) {
		t.Errorf("modified: %+v", modified)
	}
	leftAsTheyWere(t, repo, git)

	// Staged but not committed, it is committed too.
	if err := os.WriteFile(file, []byte("Build a lister that sorts and filters.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "docs/app spec.md")
	if staged := commitSpec(t, repo, file, git); !staged.committed || staged.files != "docs/app spec.md" {
		t.Errorf("staged: %+v", staged)
	}
}

// A file that can't be a spec is refused, as the run's setup problems tell apart: missing, a
// folder, empty, or not text in UTF-8.
func TestReadSpecRefusesWhatIsntASpec(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, tc := range []struct {
		name, file string
		want       error
	}{
		{"missing", filepath.Join(dir, "none.md"), fs.ErrNotExist},
		{"empty", write("empty.md", ""), ErrSpecEmpty},
		{"whitespace", write("blank.md", " \n\t\n"), ErrSpecEmpty},
		{"NUL", write("nul.md", "# Spec\x00\n"), ErrSpecNotText},
		{"not UTF-8", write("latin1.md", "caf\xe9\n"), ErrSpecNotText},
	} {
		if _, err := ReadSpec(tc.file); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := ReadSpec(dir); err == nil {
		t.Error("a folder was read as a spec")
	}
	if b, err := ReadSpec(write("ok.md", "# Ein Spezifikation\n\nÜber alles.\n")); err != nil || len(b) == 0 {
		t.Errorf("a spec in UTF-8: %q, %v", b, err)
	}
}

// A spec's path in the repository: its own inside it, followed through symlinks, else its name in
// .orchestra/specs/.
func TestSpecPath(t *testing.T) {
	repo, outside := filepath.Join(t.TempDir(), "repo"), t.TempDir()
	for _, p := range []string{filepath.Join(repo, "docs", "a.md"), filepath.Join(outside, "b.md")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	links := map[string]string{
		filepath.Join(outside, "in.md"): filepath.Join(repo, "docs", "a.md"), // outside, leading in
		filepath.Join(repo, "out.md"):   filepath.Join(outside, "b.md"),      // inside, leading out
	}
	for link, to := range links {
		if err := os.Symlink(to, link); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		file, want string
		inside     bool
	}{
		{filepath.Join(repo, "docs", "a.md"), "docs/a.md", true},
		{filepath.Join(outside, "b.md"), ".orchestra/specs/b.md", false},
		{filepath.Join(outside, "in.md"), "docs/a.md", true},
		{filepath.Join(repo, "out.md"), ".orchestra/specs/out.md", false},
	} {
		if got, inside := SpecPath(repo, tc.file); got != tc.want || inside != tc.inside {
			t.Errorf("SpecPath(%s) = %q, %v; want %q, %v", tc.file, got, inside, tc.want, tc.inside)
		}
	}
	if runtime.GOOS == "darwin" {
		upper := filepath.Join(filepath.Dir(repo), "REPO", "docs", "a.md")
		if got, inside := SpecPath(repo, upper); got != "docs/a.md" || !inside {
			t.Errorf("the repository's folder in another case: %q, %v", got, inside)
		}
	}
}

// A spec in the repository's .git folder, which git keeps no file of, isn't committed.
func TestCommitSpecRefusesOneInTheGitFolder(t *testing.T) {
	repo, git := gitRepo(t)
	file := filepath.Join(repo, ".git", "spec.md")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := head(t, repo, git)
	if _, _, err := CommitSpec(context.Background(), repo, file); !errors.Is(err, ErrSpecInGitDir) {
		t.Errorf("err %v, want ErrSpecInGitDir", err)
	}
	if head(t, repo, git) != before {
		t.Error("committed a spec in .git")
	}
}

// A spec run's lock names its spec, which the run is scoped to before its root epic is filed.
func TestHolderNamesTheSpec(t *testing.T) {
	h := Holder{PID: 7, Branch: "main", Spec: ".orchestra/specs/app.md", Ticket: "s-1"}
	if got, want := h.describe(time.Now()), "pid 7, main, --spec .orchestra/specs/app.md"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
