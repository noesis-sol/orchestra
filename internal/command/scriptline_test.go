package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

// shellArgs are the shells a pane may run that are installed here, each with the arguments that
// have it run a line without its startup files. fish and nushell, which can take most of a second to
// start, are left out under -short.
func shellArgs(t *testing.T) map[string][]string {
	t.Helper()
	shells := map[string][]string{}
	for name, args := range map[string][]string{
		"sh": {"-c"}, "bash": {"--norc", "--noprofile", "-c"}, "zsh": {"-f", "-c"}, "dash": {"-c"},
		"fish": {"--no-config", "-c"}, "nu": {"--no-config-file", "-c"},
	} {
		if testing.Short() && (name == "fish" || name == "nu") {
			continue
		}
		if path, err := exec.LookPath(name); err == nil {
			shells[path] = args
		}
	}
	return shells
}

// The line that runs a launch script in a repository whose path holds a quote holds nothing any
// shell quotes, so the pane's shell, POSIX or not, runs the script the same way: the script, not
// the shell, quotes the arguments.
func TestScriptLineDoesNotDependOnThePanesShellQuoting(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "Bob's app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	arg := filepath.Join(repo, "it's \\ a \"rules\" file.md")
	for shell, args := range shellArgs(t) {
		t.Run(filepath.Base(shell), func(t *testing.T) {
			path := filepath.Join(repo, "launch.sh")
			line, err := ScriptLine(path, "exec printf '%s' "+ShellQuote(arg)+"\n")
			if err != nil {
				t.Fatal(err)
			}
			if !regexp.MustCompile(`^ /bin/sh [A-Za-z0-9_./-]+$`).MatchString(line) {
				t.Fatalf("the line %q holds what a shell may quote or expand", line)
			}
			script := line[len(" /bin/sh "):]
			out, err := exec.Command(shell, append(args, line)...).Output()
			if err != nil || string(out) != arg {
				t.Errorf("%s, given %q, printed %q (%v), want %q", shell, line, out, err, arg)
			}
			if _, err := os.Stat(script); !os.IsNotExist(err) {
				t.Errorf("the script %s outside the repository is still there once run (%v)", script, err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("a script was written at %s, which no shell takes as it is (%v)", path, err)
			}
		})
	}
}

// A path every shell takes as it is is where the script goes, and stays.
func TestScriptLineWritesToAPlainPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch.sh")
	line, err := ScriptLine(path, "printf ran\n")
	if err != nil {
		t.Fatal(err)
	}
	if line != " /bin/sh "+path {
		t.Fatalf("ScriptLine(%q) = %q", path, line)
	}
	out, err := exec.Command("/bin/sh", "-c", line).Output()
	if err != nil || string(out) != "ran" {
		t.Errorf("the line printed %q (%v)", out, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the script is gone once run: %v", err)
	}
}
