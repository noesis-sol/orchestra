package claude

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// installRelease writes a fake orchestra binary of the release named in it to exe, as orchestra update
// does: a new file beside it, renamed over it.
func installRelease(t *testing.T, exe, release string) {
	t.Helper()
	if err := os.WriteFile(exe+".new", []byte(release), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(exe+".new", exe); err != nil {
		t.Fatal(err)
	}
}

// hookCommands returns the command of every hook in the settings file path.
func hookCommands(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	var cmds []string
	for _, matchers := range s.Hooks {
		for _, m := range matchers {
			for _, h := range m.Hooks {
				cmds = append(cmds, h.Command)
			}
		}
	}
	return cmds
}

// TestRunBinaryOutlivesUpdate checks that the workers' hooks run the run's own binary, which an update
// renaming a new release over orchestra's path leaves as it was, and that the next run's start takes
// the new release.
func TestRunBinaryOutlivesUpdate(t *testing.T) {
	dir := t.TempDir()
	exe, repo, worktree := filepath.Join(dir, "orchestra"), filepath.Join(dir, "repo"), filepath.Join(dir, "worktree")
	for _, d := range []string{repo, worktree} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installRelease(t, exe, "release 1")

	bin, err := placeBinary(repo, exe, os.Link)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, project.RunPath(binaryName)); bin != want {
		t.Errorf("the run's binary is %s, want %s", bin, want)
	}
	args, err := Reporter{}.WithBinary(bin).ReportArgs(worktree)
	if err != nil {
		t.Fatal(err)
	}
	cmds := hookCommands(t, args[1])
	if len(cmds) == 0 {
		t.Fatal("the settings have no hooks")
	}
	for _, c := range cmds {
		if !strings.Contains(c, command.ShellQuote(bin)+" "+hookCommand+" ") {
			t.Errorf("hook %q doesn't run the run's binary %s", c, bin)
		}
	}

	installRelease(t, exe, "release 2") // orchestra update during the run
	if b, err := os.ReadFile(bin); err != nil || string(b) != "release 1" {
		t.Errorf("after the update the run's binary holds %q (%v), want the release it started with", b, err)
	}

	if _, err := placeBinary(repo, exe, os.Link); err != nil { // the next run's start
		t.Fatal(err)
	}
	if b, err := os.ReadFile(bin); err != nil || string(b) != "release 2" {
		t.Errorf("the next run's binary holds %q (%v), want the new release", b, err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, project.RunPath("")))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != binaryName {
			t.Errorf("%s left in .orchestra/run/", e.Name())
		}
	}
}

// TestRunBinaryCopies checks that the run's binary is a copy, executable, where it can't be a hard link,
// as on another file system.
func TestRunBinaryCopies(t *testing.T) {
	dir := t.TempDir()
	exe, repo := filepath.Join(dir, "orchestra"), filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	installRelease(t, exe, "release 1")
	noLink := func(string, string) error { return errors.New("cross-device link") }
	bin, err := placeBinary(repo, exe, noLink)
	if err != nil {
		t.Fatal(err)
	}
	installRelease(t, exe, "release 2")
	if b, err := os.ReadFile(bin); err != nil || string(b) != "release 1" {
		t.Errorf("the run's binary holds %q (%v), want a copy of the release it started with", b, err)
	}
	fi, err := os.Stat(bin)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("the run's binary is %v, not executable", fi.Mode())
	}
}
