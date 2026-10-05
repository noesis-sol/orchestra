package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// A worker may name a worktree whose path has an accent in the other Unicode form than orchestra
// does: é as one letter (NFC, as typed) or as e and U+0301 (NFD, as Finder gives names). Its edits
// are still in the worktree, each in the form the worker gave it.
func TestEditedFilesInEitherUnicodeForm(t *testing.T) {
	for _, tc := range []struct {
		name        string
		form, other norm.Form
	}{
		{"worktree in NFC", norm.NFC, norm.NFD},
		{"worktree in NFD", norm.NFD, norm.NFC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wt := filepath.Join(t.TempDir(), tc.form.String("projét"))
			if err := os.MkdirAll(filepath.Join(wt, ".orchestra", "run"), 0o755); err != nil {
				t.Fatal(err)
			}
			resolved, err := filepath.EvalSymlinks(wt) // /private/var/… for /var/… on macOS
			if err != nil {
				t.Fatal(err)
			}
			other := func(elem ...string) string { return tc.other.String(filepath.Join(elem...)) }
			edits := []string{
				other(wt, "docs", "résumé.md"),
				other(resolved, "a.go"),
				filepath.Join(wt, tc.other.String("café.go")),
				other(wt, "docs", "résumé.md"),
				other(wt, ".orchestra", "run", "prompt.md"),
				other(wt+"-sibling", "x.go"),
				other(wt, "..", "outside.go"),
			}
			var b []byte
			for _, e := range edits {
				b = append(b, e+"\n"...)
			}
			if err := os.WriteFile(filepath.Join(wt, ".orchestra", "run", editsName), b, 0o644); err != nil {
				t.Fatal(err)
			}

			want := []string{tc.other.String("docs/résumé.md"), "a.go", tc.other.String("café.go")}
			if got := (Reporter{}).EditedFiles(wt); !slices.Equal(got, want) {
				t.Errorf("edited %+q, want %+q", got, want)
			}
		})
	}
}
