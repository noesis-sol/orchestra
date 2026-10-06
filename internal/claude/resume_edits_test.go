package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A worker resuming the session of the one before it carries on its work, so the files that one
// edited are kept; what it last did and its session are removed, as for a new worker.
func TestResumedWorkerKeepsTheEditsBeforeIt(t *testing.T) {
	wt := t.TempDir()
	var r Reporter
	if _, err := r.ReportArgs(wt); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(wt, project.RunPath(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(editsName, filepath.Join(wt, "internal/x.go")+"\n")
	write(activityName, `{"hook_event_name":"Stop"}`)
	write(sessionName, `{"session_id":"`+testID+`"}`)

	args, err := r.ResumeArgs(wt)
	if err != nil || len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("ResumeArgs = %q, %v", args, err)
	}
	if _, err := os.Stat(args[1]); err != nil {
		t.Errorf("the hooks should be written: %v", err)
	}
	if got := r.EditedFiles(wt); !slices.Equal(got, []string{"internal/x.go"}) {
		t.Errorf("edited %q; want the earlier worker's internal/x.go kept", got)
	}
	for _, name := range []string{activityName, sessionName} {
		if _, err := os.Stat(filepath.Join(wt, project.RunPath(name))); !os.IsNotExist(err) {
			t.Errorf("the earlier worker's %s should be removed: %v", name, err)
		}
	}
}
