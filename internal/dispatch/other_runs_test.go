package dispatch

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// OtherRuns are the runs registered in the machine's folder that are going, those that started first
// first; one whose process has ended is left out and its file removed, as is anything not a run's.
func TestOtherRuns(t *testing.T) {
	t.Parallel()
	if runs := OtherRuns(filepath.Join(t.TempDir(), "none")); len(runs) != 0 {
		t.Errorf("a folder that isn't there has runs: %v", runs)
	}
	dir := t.TempDir()
	ended := exec.Command("true")
	if err := ended.Run(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	write := func(name string, pid int, repo string, started time.Time) string {
		path := filepath.Join(dir, name)
		body := fmt.Sprintf(`{"pid":%d,"repo":%q,"concurrent":3,"started":%q,"running":2}`, pid, repo,
			started.UTC().Format(time.RFC3339Nano))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("run-1-b.json", os.Getpid(), "/later", now.Add(time.Second))
	write("run-1-a.json", os.Getpid(), "/earlier", now)
	gone := write("run-2-a.json", ended.Process.Pid, "/gone", now.Add(-time.Hour))
	write("notes.txt", os.Getpid(), "/not a run", now)
	runs := OtherRuns(dir)
	if len(runs) != 2 || runs[0].Repo != "/earlier" || runs[1].Repo != "/later" {
		t.Fatalf("OtherRuns = %+v, want /earlier then /later", runs)
	}
	if r := runs[0]; r.PID != os.Getpid() || r.Concurrent != 3 || r.Running != 2 {
		t.Errorf("the run reads %+v", r)
	}
	if _, err := os.Stat(gone); !os.IsNotExist(err) {
		t.Errorf("the ended run's file is left: %v", err)
	}
}
