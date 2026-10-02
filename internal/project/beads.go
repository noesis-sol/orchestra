package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/command"
)

// orchestra init sets Beads up before anything else: it installs bd where it is missing, with the
// user's consent, and runs bd init in a repository without .beads/. bd init commits what it adds,
// together with whatever was staged, so it runs before init stages anything of its own.
const (
	// InstallLimit is how long installing bd may take: Homebrew can spend minutes updating itself.
	InstallLimit = 15 * time.Minute

	beadsScript = "https://raw.githubusercontent.com/gastownhall/beads/main/scripts/install.sh"
	beadsDocs   = "https://beads.gascity.com/getting-started/installation"
)

// bdInit sets Beads up without a prompt: --role maintainer is "not contributing to someone else's
// repo", and --non-interactive leaves auto-export off. A repository set up meanwhile isn't an error.
var bdInit = []string{"init", "--non-interactive", "--role", "maintainer", "--init-if-missing"}

// BeadsInstall is how init installs bd on this machine (see FindBeadsInstall).
type BeadsInstall struct {
	// Method names the way for the form and the summary ("Homebrew"), and Command is what it runs,
	// as one would type it; both are "" where init can't install bd, and Manual says how to by hand.
	Method, Command, Manual string
	script                  bool // the install script, rather than Homebrew
}

// FindBeadsInstall returns how init installs bd on goos (runtime.GOOS): with Homebrew where brew is
// on the PATH (macOS or Linux), the way Beads recommends, otherwise with the Beads install script
// (macOS, Linux, FreeBSD), which checks what it downloads and falls back to go install. On Windows
// and other systems init installs nothing: Manual says how to.
func FindBeadsInstall(goos string) BeadsInstall {
	if _, err := exec.LookPath("brew"); err == nil && (goos == "darwin" || goos == "linux") {
		return BeadsInstall{Method: "Homebrew", Command: "brew install beads"}
	}
	switch goos {
	case "darwin", "linux", "freebsd":
		return BeadsInstall{Method: "the Beads install script", Command: "curl -fsSL " + beadsScript + " | bash",
			script: true}
	case "windows":
		return BeadsInstall{Manual: "in PowerShell: irm https://raw.githubusercontent.com/gastownhall/beads/main/" +
			"install.ps1 | iex"}
	}
	return BeadsInstall{Manual: "as " + beadsDocs + " says"}
}

// LocateBd finds bd on the PATH or, failing that, where the Beads install script and go install put
// it, which may be off the PATH: ~/.local/bin, GOBIN or GOPATH/bin, and /usr/local/bin. path is ""
// when bd is in none of them.
func LocateBd(getenv func(string) string) (path string, onPath bool) {
	if p, err := exec.LookPath("bd"); err == nil {
		return p, true
	}
	home := getenv("HOME")
	var dirs []string
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	if gobin := getenv("GOBIN"); gobin != "" {
		dirs = append(dirs, gobin)
	}
	if gopath, _, _ := strings.Cut(getenv("GOPATH"), string(os.PathListSeparator)); gopath != "" {
		dirs = append(dirs, filepath.Join(gopath, "bin"))
	} else if home != "" {
		dirs = append(dirs, filepath.Join(home, "go", "bin"))
	}
	for _, d := range append(dirs, "/usr/local/bin") {
		p := filepath.Join(d, "bd")
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, false
		}
	}
	return "", false
}

// offPath is how to put bd, found off the PATH at path, on it.
func offPath(path string) string {
	dir := filepath.Dir(path)
	return "it is in " + dir + ", which isn't on your PATH: add it in your shell's profile " +
		"(export PATH=\"$PATH:" + dir + "\") and open a new terminal"
}

// SetUpBeads installs bd where it is missing and the choice consents, then runs bd init in repo when
// Beads isn't set up there, and returns a step for each it did, couldn't do or wasn't asked to do. A
// failure is a step saying why, not an error: init goes on, and Prerequisites reports what's missing.
// It tells working what it runs before it runs it.
func SetUpBeads(
	ctx context.Context, repo string, c Choice, getenv func(string) string, working func(string),
) []Step {
	var steps []Step
	path, onPath := LocateBd(getenv)
	if path != "" && !onPath {
		return nil // installed, off the PATH: Prerequisites says how to add it
	}
	if !onPath {
		var s Step
		s, onPath = installBd(ctx, repo, c, getenv, working)
		steps = append(steps, s)
	}
	if !onPath || fileExists(filepath.Join(repo, ".beads")) {
		return steps
	}
	working("setting Beads up here: bd " + strings.Join(bdInit, " "))
	return append(steps, runBdInit(ctx, repo))
}

// installBd installs bd, which is nowhere, as the choice says, and returns the step and whether bd
// is on the PATH now.
func installBd(
	ctx context.Context, repo string, c Choice, getenv func(string) string, working func(string),
) (Step, bool) {
	missing := func(detail string) (Step, bool) { return Step{Kind: StepMissing, Label: "bd", Detail: detail}, false }
	in := c.Install
	switch {
	case in.Command == "":
		return missing("not installed, and orchestra can't install it here; install it " + in.Manual)
	case c.InstallUnasked:
		return missing("not installed; not asked (no terminal): --install-beads installs it with " + in.Method +
			" (" + in.Command + ")")
	case !c.InstallBeads:
		return missing("not installed (declined); install it with " + in.Command)
	}
	working("installing Beads with " + in.Method + ", which can take a few minutes: " + in.Command)
	failed := runInstall(ctx, repo, in)
	if path, onPath := LocateBd(getenv); onPath {
		return Step{Kind: StepDone, Label: "bd", Detail: "installed with " + in.Method + " (" + in.Command + ")"}, true
	} else if path != "" {
		return missing("installed with " + in.Method + ", but " + offPath(path))
	}
	if failed != "" {
		return missing(failed + "; install it by hand: see " + beadsDocs)
	}
	return missing(in.Command + " ran, but bd isn't on the PATH; install it by hand: see " + beadsDocs)
}

// runInstall installs bd with Homebrew, or with the install script as its instructions run it, and
// returns how it failed, or "". At the time limit, or on Ctrl+C, it stops everything the install
// started: Homebrew's downloads, the script's go install.
func runInstall(ctx context.Context, repo string, in BeadsInstall) string {
	installCtx, cancel := context.WithTimeout(ctx, InstallLimit)
	defer cancel()
	name, args := "brew", []string{"install", "beads"}
	if in.script {
		name, args = "bash", []string{"-c", "set -o pipefail; " + in.Command} // a failed download fails too
	}
	out, err := command.GroupOutput(installCtx, 5*time.Second, repo, name, args...)
	switch {
	case err == nil:
		return ""
	case ctx.Err() != nil:
		err = errors.New("stopped")
	case errors.Is(installCtx.Err(), context.DeadlineExceeded):
		err = errors.New("timed out after " + strings.TrimSuffix(InstallLimit.String(), "0s"))
	}
	return failure(in.Command, err, string(out))
}

// runBdInit runs bd init in repo and returns the step, listing what bd added: what it committed,
// and what is left to commit with .orchestra/.
func runBdInit(ctx context.Context, repo string) Step {
	before := readWorktree(ctx, repo)
	if _, err := command.Output(ctx, command.WriteLimit, repo, "bd", bdInit...); err != nil {
		var e *command.Error
		why, stderr := err, ""
		if errors.As(err, &e) {
			why, stderr = e.Err, e.Stderr
		}
		return Step{Kind: StepMissing, Label: "Beads", Detail: failure("bd init", why, stderr)}
	}
	after := readWorktree(ctx, repo)
	var committed []string
	if after.head != before.head {
		committed = changedIn(ctx, repo, before.head, after.head)
	}
	var added []string
	for p, st := range after.status {
		if before.status[p] != st {
			added = append(added, p)
		}
	}
	committed, added = topLevel(committed), topLevel(added)
	detail := "ran bd init (maintainer, not contributing to someone else's repo; auto-export off)"
	if len(committed) > 0 {
		detail += "; bd committed " + joinAnd(committed)
	}
	if len(added) > 0 {
		detail += "; it added " + joinAnd(added) + ", to commit with " + Dir + "/"
	}
	return Step{Kind: StepDone, Label: "Beads", Detail: detail, Commit: added}
}

// worktree is what git says of a repository: its HEAD commit ("" before the first) and the status
// of each changed or untracked file.
type worktree struct {
	head   string
	status map[string]string
}

// readWorktree reads the repository's HEAD and status. It only names the files bd init added, so
// a git that fails leaves them out.
func readWorktree(ctx context.Context, repo string) worktree {
	head, _ := command.Output(ctx, command.ReadLimit, repo, "git", "rev-parse", "-q", "--verify", "HEAD")
	out, _ := command.Output(ctx, command.ReadLimit, repo, "git", "status", "--porcelain", "-z",
		"--untracked-files=all")
	w := worktree{head: strings.TrimSpace(head), status: map[string]string{}}
	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		e := entries[i]
		if len(e) < 4 {
			continue
		}
		w.status[e[3:]] = e[:2]
		if e[0] == 'R' || e[0] == 'C' {
			i++ // the path it was renamed or copied from
		}
	}
	return w
}

// changedIn lists the files changed from commit from (or "" for none) to commit to.
func changedIn(ctx context.Context, repo, from, to string) []string {
	args := []string{"diff", "--name-only", "-z", from, to}
	if from == "" {
		args = []string{"ls-tree", "-r", "--name-only", "-z", to}
	}
	out, _ := command.Output(ctx, command.ReadLimit, repo, "git", args...) // see readWorktree
	return strings.FieldsFunc(out, func(r rune) bool { return r == 0 })
}

// topLevel shortens paths to what the repository's top holds, sorted: .beads/ for .beads/config.yaml.
func topLevel(paths []string) []string {
	var top []string
	for _, p := range paths {
		if first, _, nested := strings.Cut(p, "/"); nested {
			p = first + "/"
		}
		if !slices.Contains(top, p) {
			top = append(top, p)
		}
	}
	slices.Sort(top)
	return top
}

// joinAnd lists items as a sentence does: "a", "a and b", "a, b and c".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// failure says how a command failed for a step: why, and the end of its output, where a command
// says what went wrong.
func failure(what string, why error, output string) string {
	msg := what + " failed (" + why.Error() + ")"
	var lines []string
	for _, l := range strings.Split(ansi.Strip(output), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		msg += ": " + strings.Join(lines[max(len(lines)-3, 0):], " ")
	}
	return msg
}
