//go:build unix

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// fakeBeadsTools puts a folder first on a PATH without the machine's own bd and brew (git, then
// /usr/bin and /bin) and returns it. It holds a brew that logs its calls to calls and installs a
// bd into the folder, and that bd when withBd. bd logs its calls, and notes when bd init finds
// .orchestra/ there already; its init adds .beads/ and commits it, and adds AGENTS.md uncommitted.
func fakeBeadsTools(t *testing.T, withBd bool) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir, tools := t.TempDir(), t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(tools, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{dir, tools, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	for _, name := range []string{"bd", "brew"} {
		if p, err := exec.LookPath(name); err == nil {
			t.Skipf("%s is in /usr/bin or /bin: %s", name, p)
		}
	}
	if _, err := os.Stat("/usr/local/bin/bd"); err == nil {
		t.Skip("bd is in /usr/local/bin, where init looks off the PATH")
	}
	bd := `#!/bin/sh
d='` + dir + `'
echo "bd $*" >> "$d/calls"
[ "$1" = init ] || exit 0
[ -e .orchestra ] && echo "bd init saw .orchestra" >> "$d/calls"
mkdir -p .beads && echo 'issue-prefix: t' > .beads/config.yaml && echo agents > AGENTS.md
git add .beads && git -c user.name=t -c user.email=t@t commit -q -m 'bd init: initialize beads issue tracking'
`
	// What brew installs, as a fake tool is: a link to it and a copy of its script.
	faketool.Write(t, dir, "bd.fake", bd)
	if withBd {
		faketool.Write(t, dir, "bd", bd)
	}
	faketool.Write(t, dir, "brew", `#!/bin/sh
d='`+dir+`'
echo "brew $*" >> "$d/calls"
cp "$d/bd.fake.sh" "$d/bd.sh" && ln -f "$d/bd.fake" "$d/bd"
`)
	return dir
}

// toolCalls is what the fake tools in dir were called with, one call a line.
func toolCalls(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

const bdInitCall = "bd init --non-interactive --role maintainer --init-if-missing\n"

func TestInitInstallsBeadsThenRunsBdInit(t *testing.T) {
	dir := fakeBeadsTools(t, false)
	repo, _ := gitRepo(t)
	env := map[string]string{"HOME": t.TempDir()}
	stdout, stderr, err := runIn(t, repo, env, "init", "--check", "make check", "--install-beads")
	if err != nil {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	// bd init runs before init writes .orchestra/: it commits whatever is staged.
	if got := toolCalls(t, dir); got != "brew install beads\n"+bdInitCall {
		t.Errorf("calls:\n%s", got)
	}
	plain := strings.Join(strings.Fields(stdout), " ")
	for _, want := range []string{
		"… installing Beads with Homebrew, which can take a few minutes: brew install beads",
		"✓ bd installed with Homebrew (brew install beads)",
		"✓ Beads ran bd init (maintainer, not contributing to someone else's repo; auto-export off); " +
			"bd committed .beads/; it added AGENTS.md, to commit with .orchestra/",
		"✓ bd · ✓ Beads",
		"Commit .orchestra/ and AGENTS.md.",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(plain, "set it up here") || strings.Contains(plain, "install Beads") {
		t.Errorf("stdout still asks for Beads:\n%s", stdout)
	}
}

func TestInitRunsBdInitOnlyWhereBdIsInstalled(t *testing.T) {
	dir := fakeBeadsTools(t, true)
	repo, _ := gitRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "make check")
	if err != nil || toolCalls(t, dir) != bdInitCall {
		t.Fatalf("init: %v, calls:\n%s\nstdout:\n%s\nstderr:\n%s", err, toolCalls(t, dir), stdout, stderr)
	}
	// Run again, with bd and Beads both there: neither runs.
	stdout, _, err = runIn(t, repo, nil, "init")
	if err != nil || toolCalls(t, dir) != bdInitCall || strings.Contains(stdout, "bd init") {
		t.Errorf("second init: %v, calls:\n%s\nstdout:\n%s", err, toolCalls(t, dir), stdout)
	}
}

func TestInitWithoutATerminalOrConsentInstallsNothing(t *testing.T) {
	dir := fakeBeadsTools(t, false)
	repo, _ := gitRepo(t)
	env := map[string]string{"HOME": t.TempDir()}
	for args, want := range map[string]string{
		"": "✗ bd not installed; not asked (no terminal): --install-beads installs it with Homebrew " +
			"(brew install beads)",
		"--install-beads=false": "✗ bd not installed (declined); install it with brew install beads",
	} {
		stdout, stderr, err := runIn(t, repo, env, strings.Fields("init --check true "+args)...)
		plain := strings.Join(strings.Fields(stdout), " ")
		if err != nil || !strings.Contains(plain, want) ||
			!strings.Contains(plain, "Fix what's missing above (bd: install Beads).") {
			t.Errorf("%q: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
		}
	}
	if got := toolCalls(t, dir); got != "" {
		t.Errorf("calls:\n%s", got)
	}
}
