//go:build unix

package project

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// fakeBeadsTools puts a folder first on a PATH without the machine's own bd and brew (git, then
// /usr/bin and /bin), and HOME in a folder of its own, and returns the folder. It holds a bd when
// withBd, which logs its calls to calls; its init adds .beads/ and commits it, and adds AGENTS.md
// uncommitted, or fails when the folder holds bd-fails. A brew, when withBrew, logs its calls and
// installs that bd into the folder, or fails when the folder holds brew-fails, or first waits a
// minute in a child process when it holds brew-hangs. curl logs its calls
// and prints an install script that puts the bd in ~/.local/bin, off the PATH, and fails as the
// Beads script does then.
func fakeBeadsTools(t *testing.T, withBd, withBrew bool) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir, tools, home := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(tools, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{dir, tools, "/usr/bin", "/bin"}, string(os.PathListSeparator)))
	t.Setenv("HOME", home)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	for _, name := range []string{"bd", "brew"} {
		if p, err := exec.LookPath(name); err == nil {
			t.Skipf("%s is in /usr/bin or /bin: %s", name, p)
		}
	}
	if _, err := os.Stat("/usr/local/bin/bd"); err == nil {
		t.Skip("bd is in /usr/local/bin, where LocateBd looks off the PATH")
	}
	bd := `#!/bin/sh
d='` + dir + `'
echo "bd $*" >> "$d/calls"
[ "$1" = init ] || exit 0
[ -e .orchestra ] && echo "bd init saw .orchestra" >> "$d/calls"
[ -f "$d/bd-fails" ] && { echo 'Error: failed to open the Dolt database' >&2; exit 1; }
mkdir -p .beads && echo 'issue-prefix: t' > .beads/config.yaml && echo agents > AGENTS.md
git add .beads && git -c user.name=t -c user.email=t@t commit -q -m 'bd init: initialize beads issue tracking'
`
	// What brew and the install script install, as a fake tool is: a link to it and a copy of its script.
	faketool.Write(t, dir, "bd.fake", bd)
	if withBd {
		faketool.Write(t, dir, "bd", bd)
	}
	if withBrew {
		faketool.Write(t, dir, "brew", `#!/bin/sh
d='`+dir+`'
echo "brew $*" >> "$d/calls"
[ -f "$d/brew-fails" ] && { printf '==> Fetching beads\nError: beads: no bottle available!\n' >&2; exit 1; }
[ -f "$d/brew-hangs" ] && sleep 60
cp "$d/bd.fake.sh" "$d/bd.sh" && ln -f "$d/bd.fake" "$d/bd"
`)
	}
	faketool.Write(t, dir, "curl", `#!/bin/sh
d='`+dir+`'
echo "curl $*" >> "$d/calls"
cat "$d/install.sh"
`)
	install := `d='` + dir + `'
echo "==> Installing to $HOME/.local/bin..." >&2
mkdir -p "$HOME/.local/bin" && cp "$d/bd.fake.sh" "$HOME/.local/bin/bd.sh" && ln -f "$d/bd.fake" "$HOME/.local/bin/bd"
echo 'Error: bd was installed but is not in PATH' >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(dir, "install.sh"), []byte(install), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// calls is what the fake tools in dir were called with, one call a line.
func calls(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// setUpBeads runs SetUpBeads in repo and returns its steps and what it said it was working on.
func setUpBeads(t *testing.T, repo string, c Choice) ([]Step, []string) {
	t.Helper()
	var working []string
	steps := SetUpBeads(context.Background(), repo, c, os.Getenv, func(s string) { working = append(working, s) })
	return steps, working
}

// same reports whether two steps are the same.
func same(a, b Step) bool { return reflect.DeepEqual(a, b) }

const bdInitCall = "bd init --non-interactive --role maintainer --init-if-missing\n"

func TestSetUpBeadsInstallsWithHomebrewThenRunsBdInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs real git, a fake brew and a fake bd")
	}
	dir := fakeBeadsTools(t, false, true)
	repo, _ := gitRepo(t)
	c := Choice{Install: FindBeadsInstall("darwin"), InstallBeads: true}
	steps, working := setUpBeads(t, repo, c)
	if got := calls(t, dir); got != "brew install beads\n"+bdInitCall {
		t.Errorf("calls:\n%s", got)
	}
	if len(steps) != 2 || !same(steps[0], Step{Kind: StepDone, Label: "bd",
		Detail: "installed with Homebrew (brew install beads)"}) || steps[1].Kind != StepDone || steps[1].Label != "Beads" {
		t.Fatalf("steps = %+v", steps)
	}
	for _, want := range []string{"ran bd init (maintainer, not contributing to someone else's repo; auto-export off)",
		"bd committed .beads/", "it added AGENTS.md, to commit with .orchestra/"} {
		if !strings.Contains(steps[1].Detail, want) {
			t.Errorf("the Beads step lacks %q: %s", want, steps[1].Detail)
		}
	}
	if !slices.Equal(steps[1].Commit, []string{"AGENTS.md"}) {
		t.Errorf("to commit: %v", steps[1].Commit)
	}
	if len(working) != 2 || !strings.HasPrefix(working[0], "installing Beads with Homebrew") ||
		!strings.Contains(working[1], "bd init --non-interactive") {
		t.Errorf("working: %q", working)
	}
	pre := Prerequisites(repo, os.Getenv)
	if pre[0].Kind != StepDone || pre[1].Kind != StepDone {
		t.Errorf("prerequisites: %+v", pre[:2])
	}
	next := strings.Join(NextSteps(context.Background(), repo, steps, pre, Choice{}), "\n")
	if strings.Contains(next, "bd init") || !strings.Contains(next, "Commit AGENTS.md.") {
		t.Errorf("next:\n%s", next)
	}
}

func TestSetUpBeadsRunsOnlyBdInitWhereBdIsInstalled(t *testing.T) {
	dir := fakeBeadsTools(t, true, true)
	repo, _ := gitRepo(t)
	steps, _ := setUpBeads(t, repo, Choice{Install: FindBeadsInstall("darwin"), InstallBeads: true})
	if got := calls(t, dir); got != bdInitCall {
		t.Errorf("calls:\n%s", got)
	}
	if len(steps) != 1 || steps[0].Label != "Beads" || steps[0].Kind != StepDone {
		t.Errorf("steps = %+v", steps)
	}
}

func TestSetUpBeadsDoesNothingWhereBeadsIsSetUp(t *testing.T) {
	dir := fakeBeadsTools(t, true, true)
	repo, _ := gitRepo(t)
	if err := os.Mkdir(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	steps, working := setUpBeads(t, repo, Choice{Install: FindBeadsInstall("darwin"), InstallBeads: true})
	if got := calls(t, dir); got != "" || len(steps) != 0 || len(working) != 0 {
		t.Errorf("calls %q, steps %+v, working %q", got, steps, working)
	}
}

func TestSetUpBeadsInstallsNothingWithoutConsent(t *testing.T) {
	dir := fakeBeadsTools(t, false, true)
	repo, _ := gitRepo(t)
	in := FindBeadsInstall("linux")
	for _, tc := range []struct {
		c    Choice
		want string
	}{
		{Choice{Install: in, InstallUnasked: true},
			"not installed; not asked (no terminal): --install-beads installs it with Homebrew (brew install beads)"},
		{Choice{Install: in}, "not installed (declined); install it with brew install beads"},
		{Choice{Install: FindBeadsInstall("windows"), InstallBeads: true}, "orchestra can't install it here; " +
			"install it in PowerShell: irm https://raw.githubusercontent.com/gastownhall/beads/main/install.ps1 | iex"},
	} {
		steps, _ := setUpBeads(t, repo, tc.c)
		if len(steps) != 1 || steps[0].Kind != StepMissing || steps[0].Label != "bd" ||
			!strings.Contains(steps[0].Detail, tc.want) {
			t.Errorf("%+v: steps = %+v", tc.c, steps)
		}
	}
	if got := calls(t, dir); got != "" {
		t.Errorf("calls:\n%s", got)
	}
	pre := Prerequisites(repo, os.Getenv)
	if !same(pre[0], Step{Kind: StepMissing, Label: "bd", Detail: "install Beads"}) ||
		!same(pre[1], Step{Kind: StepMissing, Label: "Beads", Detail: "set it up here: bd init"}) {
		t.Errorf("prerequisites: %+v", pre[:2])
	}
}

func TestSetUpBeadsSaysWhereTheInstallScriptPutBd(t *testing.T) {
	dir := fakeBeadsTools(t, false, false)
	repo, _ := gitRepo(t)
	in := FindBeadsInstall("linux")
	if in.Method != "the Beads install script" ||
		in.Command != "curl -fsSL https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh | bash" {
		t.Fatalf("install = %+v", in)
	}
	steps, _ := setUpBeads(t, repo, Choice{Install: in, InstallBeads: true})
	bin := filepath.Join(os.Getenv("HOME"), ".local", "bin")
	if got := calls(t, dir); got != "curl -fsSL "+beadsScript+"\n" {
		t.Errorf("calls:\n%s", got)
	}
	want := "installed with the Beads install script, but it is in " + bin + ", which isn't on your PATH: " +
		`add it in your shell's profile (export PATH="$PATH:` + bin + `") and open a new terminal`
	if len(steps) != 1 || !same(steps[0], Step{Kind: StepMissing, Label: "bd", Detail: want}) {
		t.Errorf("steps = %+v", steps)
	}
	pre := Prerequisites(repo, os.Getenv)
	if pre[0].Kind != StepMissing || !strings.HasPrefix(pre[0].Detail, "it is in "+bin+", which isn't on your PATH") {
		t.Errorf("prerequisites: %+v", pre[0])
	}
	if next := NextSteps(context.Background(), repo, steps, pre, Choice{}); !strings.HasPrefix(next[0],
		"Fix what's missing above (bd: it is in "+bin) {
		t.Errorf("next: %q", next[0])
	}
	// Run again, init doesn't install bd a second time: it is there, off the PATH.
	if steps, _ = setUpBeads(t, repo, Choice{Install: in, InstallBeads: true}); len(steps) != 0 {
		t.Errorf("second run: steps = %+v", steps)
	}
	if got := calls(t, dir); strings.Count(got, "curl") != 1 {
		t.Errorf("calls:\n%s", got)
	}
}

func TestSetUpBeadsShowsWhyItFailed(t *testing.T) {
	dir := fakeBeadsTools(t, false, true)
	repo, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "brew-fails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	c := Choice{Install: FindBeadsInstall("darwin"), InstallBeads: true}
	steps, _ := setUpBeads(t, repo, c)
	want := "brew install beads failed (exit status 1): ==> Fetching beads Error: beads: no bottle available!; " +
		"install it by hand: see " + beadsDocs
	if len(steps) != 1 || !same(steps[0], Step{Kind: StepMissing, Label: "bd", Detail: want}) {
		t.Errorf("steps = %+v", steps)
	}
	if got := calls(t, dir); got != "brew install beads\n" {
		t.Errorf("calls:\n%s", got)
	}

	// bd init failing keeps the fix in the Next box.
	if err := os.Rename(filepath.Join(dir, "brew-fails"), filepath.Join(dir, "bd-fails")); err != nil {
		t.Fatal(err)
	}
	steps, _ = setUpBeads(t, repo, c)
	want = "bd init failed (exit status 1): Error: failed to open the Dolt database"
	if len(steps) != 2 || !same(steps[1], Step{Kind: StepMissing, Label: "Beads", Detail: want}) {
		t.Errorf("steps = %+v", steps)
	}
	pre := Prerequisites(repo, os.Getenv)
	if next := NextSteps(context.Background(), repo, steps, pre, Choice{}); next[0] !=
		"Fix what's missing above (Beads: set it up here: bd init)." {
		t.Errorf("next: %q", next)
	}
}

func TestSetUpBeadsStopsTheWholeInstallWhenCancelled(t *testing.T) {
	dir := fakeBeadsTools(t, false, true)
	repo, _ := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "brew-hangs"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	steps := SetUpBeads(ctx, repo, Choice{Install: FindBeadsInstall("darwin"), InstallBeads: true}, os.Getenv,
		func(string) {})
	// brew's sleep holds the output open: only stopping the whole group ends the wait before its grace.
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("SetUpBeads took %s after the cancel", took)
	}
	if len(steps) != 1 || !strings.HasPrefix(steps[0].Detail, "brew install beads failed (stopped);") {
		t.Errorf("steps = %+v", steps)
	}
}

func TestFindBeadsInstall(t *testing.T) {
	fakeBeadsTools(t, false, true)
	for goos, method := range map[string]string{"darwin": "Homebrew", "linux": "Homebrew",
		"freebsd": "the Beads install script"} {
		if in := FindBeadsInstall(goos); in.Method != method || in.Command == "" {
			t.Errorf("%s with brew: %+v", goos, in)
		}
	}
	for _, goos := range []string{"windows", "plan9"} {
		if in := FindBeadsInstall(goos); in.Command != "" || in.Manual == "" {
			t.Errorf("%s: %+v", goos, in)
		}
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	if in := FindBeadsInstall("darwin"); in.Method != "the Beads install script" {
		t.Errorf("darwin without brew: %+v", in)
	}
}

func TestLocateBdLooksWhereGoInstallPutsIt(t *testing.T) {
	fakeBeadsTools(t, false, false)
	gopath := t.TempDir()
	bin := filepath.Join(gopath, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	faketool.Write(t, bin, "bd", "#!/bin/sh\n")
	env := map[string]string{"GOPATH": gopath + string(os.PathListSeparator) + t.TempDir()}
	if path, onPath := LocateBd(func(k string) string { return env[k] }); path != filepath.Join(bin, "bd") || onPath {
		t.Errorf("LocateBd = %q, %v", path, onPath)
	}
	if path, _ := LocateBd(func(string) string { return "" }); path != "" {
		t.Errorf("without GOPATH or HOME: %q", path)
	}
}

func TestTopLevelAndJoinAnd(t *testing.T) {
	got := topLevel([]string{"AGENTS.md", ".beads/config.yaml", ".beads/hooks/pre-commit", ".claude/settings.json"})
	if !slices.Equal(got, []string{".beads/", ".claude/", "AGENTS.md"}) {
		t.Errorf("topLevel = %q", got)
	}
	for want, items := range map[string][]string{"": nil, "a": {"a"}, "a and b": {"a", "b"}, "a, b and c": {"a", "b", "c"}} {
		if got := joinAnd(items); got != want {
			t.Errorf("joinAnd(%q) = %q", items, got)
		}
	}
}
