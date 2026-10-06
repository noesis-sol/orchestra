package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
)

// A project's checks are two runners, shell scripts init writes in the project from its suites and
// the project then owns:
//
//	scripts/check-fast.sh   the merge check, run on every ticket rebased before it merges
//	scripts/check-full.sh   everything: check-fast.sh, then the slower suites, run once at the end of a run
//
// A runner lists the suites' commands, one line each; each framework finds its own test files, so
// a new test in an existing suite needs no edit there. An existing scripts/check.sh is never
// edited: check-fast.sh calls it as a suite.
const (
	// FastRunner is the merge check's runner, as settings.json's check_fast names it.
	FastRunner = "scripts/check-fast.sh"
	// FullRunner is the full check's runner, as settings.json's check_full names it.
	FullRunner = "scripts/check-full.sh"
)

// SuiteMarker starts the line a runner prints as each suite starts, "SUITE: unit tests": when the
// full check fails, the last one names the suite it failed in. Any project's check can print them.
const SuiteMarker = "SUITE:"

// Suite is one line of a runner: a command that tests, lints, type-checks or builds the project.
type Suite struct {
	Name    string // such as "unit tests"; it names the suite's lock
	Command string // run from the repository's root, such as "npm test"
	FoundIn string // where it was found, such as "package.json:7", or "--check-fast"
	// Serial is set for a suite that isn't parallel_safe: two copies at once, in two worktrees,
	// would collide (a fixed port, a shared database). It runs through the lock, taking turns.
	Serial bool
}

// runnerHeader starts every runner: what it is, and whose.
const runnerHeader = "#!/bin/sh\n" +
	"# %s: %s\n" +
	"# orchestra init wrote it; it is the project's to edit. Each suite is one command, run from the\n" +
	"# repository's root, and finds its own test files: a new test in an existing suite needs no edit here.\n"

const (
	fastPurpose = "orchestra's merge check, run on every ticket rebased onto work merged while it ran."
	fullPurpose = "every check, " + FastRunner + " then the slower suites. orchestra runs it once at\n" +
		"# the end of a run."
)

// lockFunctions are the shell functions a runner with a Serial suite starts with.
const lockFunctions = `
# lock NAME: take turns on suite NAME with the checks running at once in this repository's other
# worktrees, until unlock or the end of the script. The lock is a directory in the temp directory,
# named for the repository and the suite, holding its holder's PID; it is taken over once that
# process is gone. (macOS has no flock.)
lock() {
	lock_try="${TMPDIR:-/tmp}/check-$(repo_id)-$1.lock"
	until mkdir "$lock_try" 2>/dev/null; do
		lock_holder=$(cat "$lock_try/pid" 2>/dev/null) || lock_holder=
		if stale "$lock_try" "$lock_holder" && mkdir "$lock_try.take" 2>/dev/null; then
			# Only a taker holding .take removes another's lock: check it is still the stale one.
			if stale "$lock_try" "$lock_holder" && [ "$(cat "$lock_try/pid" 2>/dev/null)" = "$lock_holder" ]; then
				rm -rf "$lock_try"
			fi
			rmdir "$lock_try.take"
		else
			find "$lock_try.take" -prune -mmin +1 -exec rm -rf {} + 2>/dev/null || true # its taker died
			sleep 1
		fi
	done
	lock_dir=$lock_try
	echo $$ >"$lock_dir/pid"
}

unlock() {
	if [ -n "${lock_dir:-}" ]; then
		rm -rf "$lock_dir"
		lock_dir=
	fi
}

# stale DIR PID: the lock's holder is gone, or never wrote its PID in a minute.
stale() {
	if [ -n "$2" ]; then
		! kill -0 "$2" 2>/dev/null
	else
		[ -n "$(find "$1" -prune -mmin +1 2>/dev/null)" ]
	fi
}

# repo_id names the repository the same in all its worktrees: a checksum of its git directory.
repo_id() {
	(cd "$(git rev-parse --git-common-dir 2>/dev/null || echo .)" && pwd -P) | cksum | cut -d ' ' -f 1
}

trap unlock EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
`

// FastScript is the scripts/check-fast.sh init writes for suites: just 'exit 0' for none. A suite
// that runs a runner is left out (RunsRunner).
func FastScript(suites []Suite) string {
	suites, _ = WithoutRunners(suites)
	return runnerScript(FastRunner, fastPurpose, suites)
}

// FullScript is the scripts/check-full.sh init writes for its own suites, the slower ones: FastRunner
// first, then those. With none, and fastEmpty set (check-fast.sh checks nothing either), it is just
// 'exit 0'. A suite that runs a runner is left out (RunsRunner).
func FullScript(suites []Suite, fastEmpty bool) string {
	suites, _ = WithoutRunners(suites)
	if len(suites) > 0 || !fastEmpty {
		suites = append([]Suite{{Command: FastRunner, FoundIn: "the merge check"}}, suites...)
	}
	return runnerScript(FullRunner, fullPurpose, suites)
}

// RunsRunner reports whether a suite's command runs FastRunner or FullRunner anywhere in it: as a
// word ("bash scripts/check-fast.sh", "make lint && ./scripts/check-full.sh") or at the end of a path
// ("$PWD/scripts/check-fast.sh", "/home/me/repo/scripts/check-fast.sh"). A runner with such a suite
// would run itself, check-fast.sh by way of check-full.sh, until fork fails; check-full.sh runs
// check-fast.sh first already.
func RunsRunner(command string) bool {
	return namesPathIn(command, FastRunner) || namesPathIn(command, FullRunner)
}

// commandWords are a command's words, split at spaces, quotes and the shell's operators, so that
// "sh -c 'scripts/check-fast.sh'" and "$(pwd)/scripts/check-fast.sh" show the runner's path.
func commandWords(command string) []string {
	return strings.FieldsFunc(command, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune(";&|()<>'\"`", r)
	})
}

// namesPath reports whether word is path, from the repository's root: path itself, or path at the
// end of a longer one ("./scripts/check-fast.sh", "$PWD/scripts/check-fast.sh").
func namesPath(word, path string) bool {
	return word == path || strings.HasSuffix(word, "/"+path)
}

// namesPathIn reports whether a word of command is path (namesPath).
func namesPathIn(command, path string) bool {
	return slices.ContainsFunc(commandWords(command), func(w string) bool { return namesPath(w, path) })
}

// WithoutRunners splits suites into those a runner may list and those it may not, which run a runner
// (RunsRunner).
func WithoutRunners(suites []Suite) (kept, left []Suite) {
	for _, s := range suites {
		if RunsRunner(s.Command) {
			left = append(left, s)
		} else {
			kept = append(kept, s)
		}
	}
	return kept, left
}

func runnerScript(path, purpose string, suites []Suite) string {
	var b strings.Builder
	fmt.Fprintf(&b, runnerHeader, path, purpose)
	var lines []Suite
	serial := false
	for _, s := range suites {
		if s.Command = strings.TrimSpace(s.Command); s.Command != "" {
			lines = append(lines, s)
			serial = serial || s.Serial
		}
	}
	if len(lines) == 0 {
		b.WriteString("# It checks nothing yet: add a line for each suite.\nexit 0\n")
		return b.String()
	}
	b.WriteString("set -e\ncd \"$(dirname \"$0\")/..\"\n")
	if serial {
		b.WriteString(lockFunctions)
	}
	for _, s := range lines {
		b.WriteString("\n" + suiteComment(s) + "\n")
		b.WriteString(markerPrintf + shellQuote(SuiteMarker+" "+suiteLabel(s)) + "\n")
		if s.Serial {
			fmt.Fprintf(&b, "lock %s\n%s\nunlock\n", lockName(s), s.Command)
		} else {
			b.WriteString(s.Command + "\n")
		}
	}
	return b.String()
}

// suiteComment is the comment above a suite's line: its name and where it was found.
func suiteComment(s Suite) string {
	var parts []string
	if name := oneLine(s.Name); name != "" {
		parts = append(parts, name)
	}
	if from := oneLine(s.FoundIn); from != "" {
		parts = append(parts, "from "+from)
	}
	if s.Serial {
		parts = append(parts, "one worktree at a time")
	}
	if len(parts) == 0 {
		return "# a suite"
	}
	return "# " + strings.Join(parts, ", ")
}

// markerPrintf starts the line that prints a suite's SuiteMarker, its label shell-quoted after it.
const markerPrintf = `printf '%s\n' `

// isMarkerLine reports whether line prints a SuiteMarker and does nothing else, as runnerScript
// writes it: its one argument a single quoted word, which shellQuote gives back unchanged.
func isMarkerLine(line string) bool {
	word, ok := strings.CutPrefix(line, markerPrintf)
	if !ok || len(word) < 2 || word[0] != '\'' || word[len(word)-1] != '\'' {
		return false
	}
	text := strings.ReplaceAll(word[1:len(word)-1], `'\''`, "'")
	return strings.HasPrefix(text, SuiteMarker+" ") && shellQuote(text) == word
}

// suiteLabel is what a runner's SuiteMarker line calls a suite: its name, or else its command.
func suiteLabel(s Suite) string {
	if name := oneLine(s.Name); name != "" {
		return name
	}
	return oneLine(s.Command)
}

// shellQuote quotes s as one word for sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// lockName is a suite's lock's name: its name, or else its command, in lowercase letters, digits
// and dashes.
func lockName(s Suite) string {
	from := s.Name
	if strings.TrimSpace(from) == "" {
		from = s.Command
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(from) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	if b.Len() == 0 {
		return "suite"
	}
	return b.String()
}

// Runner is a runner init would write: its path from the repository's top, its script, and whether
// a file is there already and differs from the script.
type Runner struct {
	Path, Script    string
	Exists, Differs bool
	Replace         bool    // write it over a file that differs
	Left            []Suite // the suites left out of Script: they run a runner (RunsRunner)
}

// PlanRunners says what init would write for the choice's runners: for Fast or Full nil, a runner
// that checks nothing where there is none, and nothing where there is one.
func PlanRunners(repo string, c Choice) ([]Runner, error) {
	fastEmpty := false
	if c.Fast != nil {
		kept, _ := WithoutRunners(*c.Fast)
		fastEmpty = len(kept) == 0
	}
	plans := []struct {
		path    string
		suites  *[]Suite
		replace bool
		script  func([]Suite) string
	}{
		{FastRunner, c.Fast, c.ReplaceFast, FastScript},
		{FullRunner, c.Full, c.ReplaceFull, func(s []Suite) string { return FullScript(s, fastEmpty) }},
	}
	var runners []Runner
	for _, p := range plans {
		old, err := os.ReadFile(filepath.Join(repo, p.path))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		r := Runner{Path: p.path, Exists: err == nil, Replace: p.replace}
		if p.suites == nil && r.Exists {
			r.Script = string(old)
		} else {
			var suites []Suite
			if p.suites != nil {
				suites = *p.suites
			}
			r.Script = p.script(suites)
			_, r.Left = WithoutRunners(suites)
		}
		r.Differs = r.Exists && !bytes.Equal(old, []byte(r.Script))
		runners = append(runners, r)
	}
	return runners, nil
}

// RunnerCommands are the commands a runner's script runs, one per line, for a question to show: its
// lines without the comments and what init writes around the suites (set -e, the cd, the lock, the
// SuiteMarker lines).
func RunnerCommands(script string) []string {
	script = strings.Replace(script, lockFunctions, "", 1)
	var commands []string
	for line := range strings.Lines(script) {
		line = strings.TrimSpace(line)
		switch {
		case line == "", strings.HasPrefix(line, "#"), line == "set -e", line == `cd "$(dirname "$0")/.."`,
			line == "unlock", strings.HasPrefix(line, "lock ") && !strings.ContainsAny(line, ";&|"),
			isMarkerLine(line):
			continue
		}
		commands = append(commands, line)
	}
	return commands
}

// ApplyRunners writes the choice's runners, mode 755, keeping one that differs from what init would
// write unless the choice replaces it, and says what it did.
func ApplyRunners(repo string, c Choice) ([]Step, error) {
	runners, err := PlanRunners(repo, c)
	if err != nil {
		return nil, err
	}
	var steps []Step
	for _, r := range runners {
		label, flag := "check-fast", "--check-fast"
		if r.Path == FullRunner {
			label, flag = "check-full", "--check-full"
		}
		switch {
		case r.Exists && !r.Differs:
			steps = append(steps, Step{Kind: StepKept, Label: label, Detail: r.Path + " is there; left as it is"})
			continue
		case r.Differs && !r.Replace:
			steps = append(steps, Step{Kind: StepCaution, Label: label, Detail: r.Path +
				" differs from what init would write; kept (orchestra init " + flag + " \"<command>\" replaces it)"})
			continue
		}
		p := filepath.Join(repo, r.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return steps, err
		}
		if err := writeFile(p, []byte(r.Script), 0o755); err != nil {
			return steps, err
		}
		if err := os.Chmod(p, 0o755); err != nil { // a file replaced keeps its mode, and the umask may strip one
			return steps, err
		}
		detail := "wrote " + r.Path
		if r.Exists {
			detail = "replaced " + r.Path
		}
		if strings.HasSuffix(r.Script, "\nexit 0\n") {
			detail += ": it checks nothing yet (" + flag + " \"<command>\" gives it one)"
		}
		steps = append(steps, Step{Kind: StepDone, Label: label, Detail: detail, Commit: []string{r.Path}})
		for _, s := range r.Left {
			steps = append(steps, leftOutStep(label, r.Path, s))
		}
	}
	return steps, nil
}

// leftOutStep says that a runner was written without suite s, which runs a runner.
func leftOutStep(label, path string, s Suite) Step {
	why := "it runs a runner, and " + path + " would run itself until fork fails"
	if path == FullRunner && !namesPathIn(s.Command, FullRunner) {
		why = "it runs " + FastRunner + ", which " + path + " runs first already"
	}
	from := ""
	if f := oneLine(s.FoundIn); f != "" {
		from = " (from " + f + ")"
	}
	return Step{Kind: StepCaution, Label: label, Detail: "left out '" + oneLine(s.Command) + "'" + from + ": " + why}
}

// isRunner reports whether a check command is just the runner at path, which init has no suites for:
// the path, run by a shell ("sh", "bash") or not, from the root or at the end of a longer path
// ("./scripts/check-fast.sh", "$PWD/scripts/check-fast.sh").
func isRunner(check, path string) bool {
	words := strings.Fields(check)
	if len(words) == 2 && slices.Contains([]string{"sh", "bash", "exec"}, words[0]) {
		words = words[1:]
	}
	return len(words) == 1 && namesPath(words[0], path)
}

// FastCommand is the fast check as one command line, for a form to show and edit: Fast's commands
// joined by &&, and "" for none, or where Fast is nil (the runner kept as it is).
func (c Choice) FastCommand() string {
	if c.Fast == nil {
		return ""
	}
	var commands []string
	for _, s := range *c.Fast {
		commands = append(commands, strings.TrimSpace(s.Command))
	}
	return strings.Join(commands, " && ")
}
