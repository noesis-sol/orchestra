package claude

import (
	"errors"
	"os"
	"path/filepath"
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
