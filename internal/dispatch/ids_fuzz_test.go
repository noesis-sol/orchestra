package dispatch

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// trickyIDs are IDs near IDProblem's rules and git's, on either side.
var trickyIDs = []string{
	"orchestra-k4i", "orchestra-k4i.1", "B", "émile-q2", "x\xff", "-x", "x-@", "@", "x-a@b", "x-HEAD", "x-a.lock.b",
	"x-a/b", `x-a\b`, "x-../../y", "x-..", "x-a..b", "../y", "/abs", "", ".", "x/", "x//y", "x/.y", "x.lock/y",
	"x-a b", "x-a~1", "x-a^", "x-a:b", "x-a?", "x-a[", "x-a.lock", "x-a.", ".x", "x-@{1}", "x-a*", "x-\x7f",
	"x-\n", "x-\t", "x-\x01",
}

// FuzzIDProblem gives IDProblem any ID. One it accepts names a branch git takes, wt/<id>, and a
// folder directly under the worktree root. go test runs the seeds; docs/development.md says how to
// fuzz.
func FuzzIDProblem(f *testing.F) {
	for _, id := range trickyIDs {
		f.Add(id)
	}
	root := filepath.FromSlash("/projects/wt")
	f.Fuzz(func(t *testing.T, id string) {
		if IDProblem(id) != "" {
			return
		}
		if ref := "refs/heads/" + branchOf(id); !refFormatOK(ref) {
			t.Errorf("IDProblem accepts %q, but git refuses the branch %s", id, ref)
		}
		if dir := filepath.Join(root, id); filepath.Dir(dir) != root || filepath.Base(dir) != id {
			t.Errorf("IDProblem accepts %q, but its worktree %s isn't the folder %q in %s", id, dir, id, root)
		}
	})
}

// refFormatOK reports whether git check-ref-format takes ref, by the rules git help check-ref-format
// lists, in process for the fuzzer's speed. TestRefFormatOKAgreesWithGit holds it to git's verdict.
func refFormatOK(ref string) bool {
	if ref == "@" || !strings.Contains(ref, "/") || strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") ||
		strings.Contains(ref, "//") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") ||
		strings.Contains(ref, "@{") {
		return false
	}
	for _, c := range []byte(ref) { // git checks bytes: those of a multibyte character are all ≥ 0x80
		if c < 0o40 || c == 0o177 || strings.IndexByte(` ~^:?*[\`, c) >= 0 {
			return false
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func TestRefFormatOKAgreesWithGit(t *testing.T) {
	t.Parallel()
	for _, id := range trickyIDs {
		ref := "refs/heads/" + branchOf(id)
		_, err := command.Output(t.Context(), command.ReadLimit, "", "git", "check-ref-format", ref)
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		if takes := err == nil; refFormatOK(ref) != takes {
			t.Errorf("refFormatOK(%q) = %t, but git check-ref-format says %t", ref, !takes, takes)
		}
	}
}
