//go:build unix

package faketool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// machineTools puts on the process's PATH, for the rest of the test, a folder holding tools called
// installed and other, as tools installed on the machine are, and returns it. Each is echo: a fake
// (Write) runs only by the path it was made at, not through a link.
func machineTools(t *testing.T) string {
	t.Helper()
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Fatal(err)
	}
	machine := t.TempDir()
	for _, name := range []string{"installed", "other"} {
		if err := os.Symlink(echo, filepath.Join(machine, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", machine+string(os.PathListSeparator)+os.Getenv("PATH"))
	return machine
}

// lookPath is where the PATH path finds name, "" where it doesn't.
func lookPath(t *testing.T, path, name string) string {
	t.Helper()
	p, err := command.LookPath(command.WithEnv(context.Background(), "PATH="+path), name)
	if err != nil {
		return ""
	}
	return p
}

// A test's PATH holds its fakes first, then the real tools it names and the POSIX utilities: a tool
// of the machine's it didn't name is not found, though the process's PATH has it.
func TestPathHoldsOnlyTheFakesAndTheToolsNamed(t *testing.T) {
	machineTools(t)
	dir := t.TempDir()
	fake := Write(t, dir, "bd", "echo fake bd\n")
	path := Path(t, dir, "installed")
	if got := filepath.SplitList(path); len(got) != 2 || got[0] != dir {
		t.Fatalf("PATH %q: want %s, then a folder of links", path, dir)
	}
	if got := lookPath(t, path, "bd"); got != fake {
		t.Errorf("bd is %q, want the fake %s", got, fake)
	}
	if got := lookPath(t, path, "other"); got != "" {
		t.Errorf("a tool not named is on the PATH: %s", got)
	}
	out, err := command.Output(command.WithEnv(context.Background(), "PATH="+path), command.ReadLimit, "",
		"sh", "-c", "installed tool && echo a | cat")
	if err != nil || out != "tool\na\n" {
		t.Errorf("the tool named and the utilities: %q, %v", out, err)
	}
	if got := Path(t, "", "installed"); len(filepath.SplitList(got)) != 1 {
		t.Errorf("with no fakes' folder: %q", got)
	}
}

// A tool named that the machine doesn't have is an error, a utility it lacks is left out.
func TestLinkToolsNeedsTheToolsNamed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := linkTools(t.TempDir(), nil); err != nil {
		t.Errorf("no utilities: %v", err)
	}
	if err := linkTools(t.TempDir(), []string{"installed"}); err == nil || !strings.Contains(err.Error(), "no installed") {
		t.Errorf("no installed: %v", err)
	}
}

// pathOf is tests that keep the PATH and MachinePath they ran with.
type pathOf struct{ path, machine *string }

func (p pathOf) Run() int {
	*p.path, *p.machine = os.Getenv("PATH"), MachinePath()
	return 7
}

// OnlyTools runs the tests with a PATH holding only the tools named and the utilities, keeps the
// machine's for MachinePath, and returns their exit status.
func TestOnlyToolsRunsTheTestsWithOnlyTheToolsNamed(t *testing.T) {
	machine := machineTools(t)
	before := os.Getenv("PATH")
	var path, kept string
	if code := OnlyTools(pathOf{&path, &kept}, "installed").Run(); code != 7 {
		t.Errorf("exit %d, want the tests' 7", code)
	}
	if kept != before {
		t.Errorf("MachinePath %q, want %q", kept, before)
	}
	if strings.Contains(path, machine) || lookPath(t, path, "other") != "" {
		t.Errorf("the machine's PATH is behind the tests': %q", path)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the folder of links is left: %v", err)
	}
	if code := OnlyTools(pathOf{&path, &kept}, "missing").Run(); code != 1 {
		t.Errorf("a tool the machine lacks: exit %d, want 1", code)
	}
	if _, err := exec.LookPath("installed"); err != nil {
		t.Errorf("the PATH isn't restored for the rest of the test: %v", err)
	}
}
