package claude

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The worker's hooks and records are written, removed and read only inside its worktree: through
// a symlink out of it, setting up the reports is an *project.EscapeError, nothing outside is
// written or removed, and nothing outside is read back as a report.
func TestReportsStayInsideTheWorktree(t *testing.T) {
	for _, link := range []string{
		filepath.Join(project.Dir, project.RunName),
		project.RunPath(settingsName),
		project.RunPath(activityName),
		project.RunPath(editsName),
	} {
		t.Run(link, func(t *testing.T) {
			wt, outside := t.TempDir(), t.TempDir()
			// What the link points to looks like a worker's records, to be read or removed.
			for name, content := range map[string]string{
				settingsName: "kept", activityName: `{"hook_event_name":"Stop"}`, editsName: "a.go\n",
			} {
				if err := os.WriteFile(filepath.Join(outside, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			target := outside
			if filepath.Base(link) != project.RunName {
				target = filepath.Join(outside, filepath.Base(link))
			}
			if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, link)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(wt, link)); err != nil {
				t.Fatal(err)
			}

			var r Reporter
			if u, ok := r.LastToolUse(wt); ok {
				t.Errorf("read %v from outside the worktree", u)
			}
			if files := r.EditedFiles(wt); files != nil {
				t.Errorf("read %v from outside the worktree", files)
			}
			_, err := r.ReportArgs(wt)
			var e *project.EscapeError
			if !errors.As(err, &e) || e.Path != link {
				t.Fatalf("err = %v, want an *EscapeError for %s", err, link)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 3 {
				t.Fatalf("outside the worktree: %v %v, want the three files", entries, err)
			}
			if b, _ := os.ReadFile(filepath.Join(outside, settingsName)); string(b) != "kept" {
				t.Errorf("%s outside was overwritten: %q", settingsName, b)
			}
		})
	}
}

// A hooks.json that is a symlink staying inside the worktree is replaced, not written through: the
// hooks would otherwise land where it points, as an untracked file for the worker to commit.
func TestHooksReplaceALinkInside(t *testing.T) {
	wt := t.TempDir()
	kept := filepath.Join(wt, "docs", settingsName)
	if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(wt, project.RunPath(settingsName))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "docs", settingsName), link); err != nil {
		t.Fatal(err)
	}

	args, err := Reporter{}.ReportArgs(wt)
	if err != nil {
		t.Fatalf("ReportArgs: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || !fi.Mode().IsRegular() {
		t.Errorf("%s: %v %v, want a file in place of the link", link, fi, err)
	}
	if want := []string{"--settings", link}; !slices.Equal(args, want) {
		t.Errorf("args = %q, want %q", args, want)
	}
	if b, err := os.ReadFile(kept); err != nil || string(b) != "kept" {
		t.Errorf("%s where the link pointed: %q %v, want it as it was", kept, b, err)
	}
}
