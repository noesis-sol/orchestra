package project

import (
	"os"
	"path/filepath"
	"testing"
)

// A repository is set up once .orchestra/ has the worker prompt or the settings, or .claude/ has
// the prompt of a project set up before 'orchestra init'; an empty .orchestra/ or Beads alone isn't.
func TestIsSetUp(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  bool
	}{
		{nil, false},
		{[]string{".beads/config.yaml", ".orchestra/.gitignore"}, false},
		{[]string{".orchestra/worker-prompt.md"}, true},
		{[]string{".orchestra/settings.json"}, true},
		{[]string{".claude/worker-prompt.md"}, true},
	} {
		repo := t.TempDir()
		for _, f := range tc.files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, f)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, f), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := IsSetUp(repo); got != tc.want {
			t.Errorf("%q: IsSetUp = %v", tc.files, got)
		}
	}
}
