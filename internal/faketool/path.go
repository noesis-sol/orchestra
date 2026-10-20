package faketool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// utilities are the POSIX utilities, found on every Unix, that the fakes' scripts and the tests' own
// commands run: Path and OnlyTools link them on every PATH they build. None of them is a tool that
// one machine has and another lacks, as claude, bd, brew, tmux, herdr and cmux are.
var utilities = []string{
	"[", "awk", "basename", "cat", "chmod", "cmp", "comm", "cp", "cut", "date", "diff", "dirname", "echo",
	"env", "expr", "false", "find", "grep", "head", "id", "join", "kill", "ln", "ls", "mkdir", "mktemp",
	"mv", "nohup", "od", "paste", "printf", "ps", "pwd", "readlink", "rm", "rmdir", "sed", "seq", "sh",
	"sleep", "sort", "stat", "tail", "tee", "test", "touch", "tr", "true", "tty", "uname", "uniq", "wc",
	"xargs", "yes",
}

// Path returns a PATH for t that holds only dir, the folder of its fakes, unless dir is "", and a
// new folder of links to the real tools named and to the POSIX utilities (above), as the process's
// PATH finds them now. A tool t neither faked nor named is not found on it, as on a machine without
// it, wherever the machine running the test has it installed. Path fails t if a tool named is not
// on the process's PATH: a test that can do without one checks for it first.
//
//	t.Setenv("PATH", faketool.Path(t, dir, "git"))
func Path(t testing.TB, dir string, tools ...string) string {
	t.Helper()
	links := t.TempDir()
	if err := linkTools(links, tools); err != nil {
		t.Fatalf("faketool.Path: %v", err)
	}
	if dir == "" {
		return links
	}
	return dir + string(os.PathListSeparator) + links
}

// machinePath is the PATH the process had before OnlyTools replaced it, "" until it does.
var machinePath string

// MachinePath is the PATH the process had before OnlyTools replaced it, for a live test that runs
// the machine's own tools on purpose, such as the real claude; the process's PATH where OnlyTools
// didn't replace it.
func MachinePath() string {
	if machinePath != "" {
		return machinePath
	}
	return os.Getenv("PATH")
}

// OnlyTools returns m set to run with a PATH holding only a folder of links to the real tools named
// and to the POSIX utilities, as the process's PATH finds them when m runs: a command the tests run
// finds no other tool of the machine's, whatever environment it is given, so a test that relies on
// one it didn't put on its own PATH fails on every machine, not only on those without it. It fails
// if a tool named is not on the PATH. A package runs its tests through it from its TestMain, inside
// gittest.Main, whose git it links:
//
//	func TestMain(m *testing.M) { os.Exit(faketool.Main(gittest.Main(faketool.OnlyTools(m, "git"))).Run()) }
func OnlyTools(m TestingM, tools ...string) TestingM { return onlyTools{m, tools} }

type onlyTools struct {
	m     TestingM
	tools []string
}

func (o onlyTools) Run() int {
	links, err := os.MkdirTemp("", "faketool-path-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "faketool.OnlyTools:", err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(links); err != nil {
			fmt.Fprintln(os.Stderr, "faketool.OnlyTools:", err) // said, not failed: a leftover temporary folder breaks no test
		}
	}()
	path := os.Getenv("PATH")
	err = linkTools(links, o.tools)
	if err == nil {
		err = os.Setenv("PATH", links)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "faketool.OnlyTools:", err)
		return 1
	}
	machinePath = path
	defer func() {
		machinePath = ""
		if err := os.Setenv("PATH", path); err != nil {
			fmt.Fprintln(os.Stderr, "faketool.OnlyTools:", err) // said, not failed: the tests are over
		}
	}()
	return o.m.Run()
}

// linkTools puts in dir a link to each of the tools named and of the POSIX utilities, as the
// process's PATH finds them. A tool named that it doesn't find is an error; a utility that it
// doesn't find is left out, for a fake that runs it to fail as it would.
func linkTools(dir string, tools []string) error {
	for _, name := range tools {
		p, err := exec.LookPath(name)
		if err != nil {
			return fmt.Errorf("the PATH has no %s: %w", name, err)
		}
		if err := os.Symlink(p, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	for _, name := range utilities {
		if slices.Contains(tools, name) {
			continue
		}
		p, err := exec.LookPath(name)
		if err != nil {
			continue // as above
		}
		if err := os.Symlink(p, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}
