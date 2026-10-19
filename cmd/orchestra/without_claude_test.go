package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// withoutClaude returns m set to run with no claude on the process's PATH, as on a machine without
// Claude Code, such as the release's runner: a test finds a claude only where it put one, on its own
// PATH (onPath). One that hands orchestra a context without its environment (testCtx) then fails
// here too, rather than finding the claude installed here and passing.
func withoutClaude(m faketool.TestingM) faketool.TestingM { return noClaude{m} }

type noClaude struct{ m faketool.TestingM }

func (n noClaude) Run() int {
	tmp, err := os.MkdirTemp("", "orchestra-path-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "withoutClaude:", err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(tmp); err != nil {
			fmt.Fprintln(os.Stderr, "withoutClaude:", err) // said, not failed: a leftover temporary folder breaks no test
		}
	}()
	path, err := pathWithout(os.Getenv("PATH"), "claude", tmp)
	if err == nil {
		err = os.Setenv("PATH", path)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "withoutClaude:", err)
		return 1
	}
	return n.m.Run()
}

// pathWithout is path with each directory on it that holds name replaced by a folder made in tmp,
// holding a link to everything else in it: what shares a directory with name, as git, bd and tmux
// can with claude in Homebrew's, stays on the PATH.
func pathWithout(path, name, tmp string) (string, error) {
	dirs := filepath.SplitList(path)
	for i, dir := range dirs {
		if !filepath.IsAbs(dir) {
			continue // exec.LookPath runs nothing it finds by a relative one (exec.ErrDot)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		kept := filepath.Join(tmp, strconv.Itoa(i))
		if err := os.Mkdir(kept, 0o755); err != nil {
			return "", err
		}
		for _, e := range entries {
			if e.Name() == name {
				continue
			}
			if err := os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(kept, e.Name())); err != nil {
				return "", err
			}
		}
		dirs[i] = kept
	}
	return strings.Join(dirs, string(os.PathListSeparator)), nil
}

// The tests run with no claude on the process's PATH, wherever Claude Code is installed.
func TestTheTestsFindNoClaude(t *testing.T) {
	t.Parallel()
	if p, err := exec.LookPath("claude"); err == nil {
		t.Errorf("the process's PATH has a claude: %s", p)
	}
}

// A directory holding claude is replaced by one with everything else in it; the others, and a
// relative one, are kept as they are.
func TestPathWithoutClaude(t *testing.T) {
	t.Parallel()
	shared, other, tmp := t.TempDir(), t.TempDir(), t.TempDir()
	for _, name := range []string{"claude", "git", "tmux"} {
		if err := os.WriteFile(filepath.Join(shared, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(other, "bd"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	path, err := pathWithout(strings.Join([]string{other, shared, "bin"}, sep), "claude", tmp)
	if err != nil {
		t.Fatal(err)
	}
	dirs := filepath.SplitList(path)
	if len(dirs) != 3 || dirs[0] != other || dirs[1] == shared || filepath.Dir(dirs[1]) != tmp || dirs[2] != "bin" {
		t.Fatalf("PATH %q", path)
	}
	entries, err := os.ReadDir(dirs[1])
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if target, err := os.Readlink(filepath.Join(dirs[1], e.Name())); err != nil || target != filepath.Join(shared, e.Name()) {
			t.Errorf("%s links to %q (%v)", e.Name(), target, err)
		}
	}
	if !slices.Equal(names, []string{"git", "tmux"}) {
		t.Errorf("kept %q, want git and tmux", names)
	}
}
