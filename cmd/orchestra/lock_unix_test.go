//go:build unix

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// holdLock takes repo's run lock as another run would, releasing it at the end of the test.
func holdLock(t *testing.T, repo string, h project.Holder) {
	t.Helper()
	l, err := project.LockRun(repo, h)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
}

// A second run in the same repository stops before it changes anything, naming the run that holds
// the lock: a --feature request isn't screened, and no log, worktree folder or git exclude is written.
func TestASecondRunIsRefused(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	repo := configFixture(t, `{"concurrent": 1}`)
	started := time.Now().Truncate(time.Minute)
	holdLock(t, repo, project.Holder{PID: 44497, Started: started, Branch: "main", Pane: "w2B:p60"})

	var out, errOut strings.Builder
	err := run(context.Background(), []string{"orchestra", "--feature", "Add a flag", "--yes"}, os.Getenv,
		strings.NewReader(""), &out, &errOut)
	want := "(pid 44497, since " + started.Format("15:04") + ", main, pane w2B:p60). One run at a time"
	if exitOf(err) != 2 || !strings.Contains(errOut.String(), "orchestra cannot start:\n  - orchestra is already running in ") ||
		!strings.Contains(errOut.String(), want) {
		t.Errorf("exit %d, stderr:\n%s\nwant it to contain %q", exitOf(err), errOut.String(), want)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("screened while another run holds the lock: %v", err)
	}
	for _, p := range []string{filepath.Join(repo, ".orchestra", "orchestra.log"),
		filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-worktrees")} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s: %v, want none", p, err)
		}
	}
	if exclude := read(t, filepath.Join(repo, ".git", "info", "exclude")); strings.Contains(exclude, project.Dir) {
		t.Errorf("info/exclude was written:\n%s", exclude)
	}
}

// A run holds the lock while it runs and lets go as it ends, leaving its details in the file.
func TestARunLeavesItsDetailsInTheLock(t *testing.T) {
	featureTools(t, featureScreenOK, featurePlanJSON, 0)
	t.Setenv("HERDR_PANE_ID", "w2B:p61")
	repo, stdout, stderr, code := runFeatureIn(t, "--plain", "--ticket", "f-1")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if h, held, err := project.RunHolder(repo); err != nil || held {
		t.Errorf("after the run: held %v (%+v, %v)", held, h, err)
	}
	var h project.Holder
	if err := json.Unmarshal([]byte(read(t, filepath.Join(repo, project.RunPath(project.LockName)))), &h); err != nil {
		t.Fatal(err)
	}
	if h.PID != os.Getpid() || h.Pane != "w2B:p61" || h.Ticket != "f-1" || h.Branch == "" || h.Version == "" ||
		time.Since(h.Started) > time.Minute {
		t.Errorf("the lock says %+v", h)
	}
}

// orchestra plan --apply warns that a run is going, and adds the links anyway; the proposal alone doesn't.
func TestPlanApplyWarnsOfARunningRun(t *testing.T) {
	calls := fakePlanBd(t)
	repo, _ := gitRepo(t)
	holdLock(t, repo, project.Holder{PID: 44497, Branch: "main"})

	_, stderr, err := runIn(t, repo, nil, "plan")
	if err != nil || stderr != "" {
		t.Errorf("plan: %v, stderr:\n%s", err, stderr)
	}
	stdout, stderr, err := runIn(t, repo, nil, "plan", "--apply")
	if err != nil || !strings.Contains(stdout, "Added 1 link.") || read(t, calls) != "dep add k-2 k-1\n" {
		t.Errorf("plan --apply: %v, stdout:\n%s", err, stdout)
	}
	if want := "orchestra plan: warning: orchestra is already running in "; !strings.Contains(stderr, want) ||
		!strings.Contains(stderr, "(pid 44497, main). A ticket it has started keeps going") {
		t.Errorf("plan --apply stderr:\n%s", stderr)
	}
}
