package organ

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// newerHelp is the part of a claude's --help that lists the flags that confine the scout, as Claude
// Code 2.1.289 prints it.
const newerHelp = `Options:
  --permission-prompts <target>         Who answers permission prompts with
                                        --print: "host" (the SDK host or
  -p, --print                           Print response and exit (useful for
  --restricted                          Restricted mode: removes the built-in
`

// olderHelp is an older claude's: neither flag, though its text mentions them.
const olderHelp = `Options:
  --permission-mode <mode>              Permission mode to use for the session
  -p, --print                           Print response and exit, without --restricted
`

// fakeHelpClaude is a fake claude that prints help to --help, exiting with helpExit, and otherwise
// records its working directory, arguments and stdin to record and answers with an empty scouting.
func fakeHelpClaude(t *testing.T, help string, helpExit int) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	record = filepath.Join(dir, "record")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = --help ]; then\ncat <<'HELP'\n" + help + "HELP\nexit " + strconv.Itoa(helpExit) + "\nfi\n" +
		"{ pwd; for a in \"$@\"; do printf '[%s]\\n' \"$a\"; done; echo '--- stdin'; cat; } > " + record + "\n" +
		`echo '{"is_error":false,"structured_output":{"suites":[],"note":"n"}}'` + "\n"
	return faketool.Write(t, dir, "claude", script), record
}

// scoutArgs runs the scout on a fake claude whose --help is help and returns its call's arguments.
func scoutArgs(t *testing.T, help string, helpExit int) string {
	t.Helper()
	bin, record := fakeHelpClaude(t, help, helpExit)
	if _, err := (Client{Bin: bin}).Scout(context.Background(), scoutRepo(t, false)); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	args, _, _ := strings.Cut(string(b), "--- stdin")
	return args
}

// A claude that knows --restricted and --permission-prompts gets both: its file tools can't read
// outside the repository whatever the settings allow, and nothing waits on a prompt.
func TestScoutIsConfinedToTheRepository(t *testing.T) {
	args := scoutArgs(t, newerHelp, 0)
	for _, want := range []string{"\n[--restricted]\n", "\n[--permission-prompts]\n[none]\n",
		"\n[--tools]\n[Read,Glob,Grep]\n"} {
		if !strings.Contains(args, want) {
			t.Errorf("the scout was not called with %q:\n%s", want, args)
		}
	}
}

// An older claude rejects flags it doesn't know, so it gets only those its --help lists; a --help
// that fails gives none, and the scout still runs.
func TestScoutPassesOnlyTheFlagsClaudeKnows(t *testing.T) {
	for name, c := range map[string]struct {
		help                    string
		exit                    int
		restricted, noPrompting bool
	}{
		"older":           {olderHelp, 0, false, false},
		"restricted only": {"  --restricted   Restricted mode\n", 0, true, false},
		"prompts only":    {"  --permission-prompts <target>   Who answers\n", 0, false, true},
		"help fails":      {newerHelp, 1, false, false},
	} {
		args := scoutArgs(t, c.help, c.exit)
		if got := strings.Contains(args, "[--restricted]"); got != c.restricted {
			t.Errorf("%s: --restricted passed = %v, want %v:\n%s", name, got, c.restricted, args)
		}
		if got := strings.Contains(args, "[--permission-prompts]\n[none]"); got != c.noPrompting {
			t.Errorf("%s: --permission-prompts none passed = %v, want %v:\n%s", name, got, c.noPrompting, args)
		}
	}
}

// The other organs have no tools to confine and don't ask claude for its --help.
func TestOtherOrgansAreNotGivenTheScoutsFlags(t *testing.T) {
	bin, record := fakeHelpClaude(t, newerHelp, 0)
	if _, err := (Client{Bin: bin}).Ask(context.Background(), "test", hung, "low", "S", "E", ""); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(record)
	if strings.Contains(string(b), "--restricted") || strings.Contains(string(b), "--permission-prompts") {
		t.Errorf("an organ without tools got the scout's flags:\n%s", b)
	}
}

func TestListsFlag(t *testing.T) {
	for flag, want := range map[string]bool{
		"--restricted": true, "--permission-prompts": true, "--print": true,
		"--permission-mode": false, "--host": false,
	} {
		if got := listsFlag(newerHelp, flag); got != want {
			t.Errorf("listsFlag(newerHelp, %q) = %v, want %v", flag, got, want)
		}
	}
}
