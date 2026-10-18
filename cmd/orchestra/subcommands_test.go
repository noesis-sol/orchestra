package main

import (
	"flag"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestUsageLines(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		prefix, name, synopsis, want string
	}{
		{"Usage: ", "plan", "[--apply]", "Usage: orchestra plan [--apply]\n"},
		{"  ", "x", "[--a]\n[--b]", "  orchestra x [--a]\n              [--b]\n"},
		{"       ", "init", "[--a]\n[--b]\n[--c]",
			"       orchestra init [--a]\n                      [--b]\n                      [--c]\n"},
	} {
		if got := usageLines(tc.prefix, tc.name, tc.synopsis); got != tc.want {
			t.Errorf("usageLines(%q, %q, %q) = %q, want %q", tc.prefix, tc.name, tc.synopsis, got, tc.want)
		}
	}
}

func TestFindSubcommand(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ok   bool
	}{{"init", true}, {"plan", true}, {"update", true}, {"", false}, {"-h", false}, {"run", false}, {"Init", false}} {
		c, ok := findSubcommand(tc.name)
		if ok != tc.ok || ok && (c.name != tc.name || c.synopsis == "" || c.run == nil) {
			t.Errorf("findSubcommand(%q) = %+v, %v", tc.name, c.name, ok)
		}
	}
}

func TestParseArgs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		args     []string
		wantCode int
		wantOK   bool
		wantOut  string
	}{
		{"none", nil, dispatch.ExitOK, true, ""},
		{"a flag", []string{"--apply"}, dispatch.ExitOK, true, ""},
		{"help", []string{"-h"}, dispatch.ExitOK, false, "Usage of plan"},
		{"a bad flag", []string{"--nope"}, dispatch.ExitSetup, false, "flag provided but not defined: -nope"},
		{"an argument", []string{"--apply", "now"}, dispatch.ExitSetup, false,
			"orchestra plan: unexpected argument \"now\" (see orchestra plan -h)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			fs := flag.NewFlagSet("plan", flag.ContinueOnError)
			fs.SetOutput(&out)
			fs.Bool("apply", false, "")
			code, ok := parseArgs(fs, tc.args, "plan")
			if code != tc.wantCode || ok != tc.wantOK || !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("parseArgs(%q) = %d, %v; output %q", tc.args, code, ok, out.String())
			}
		})
	}
}

// Each subcommand's synopsis is the one its -h, the main usage and the hint for flags given before it
// show.
func TestSubcommandSynopsisShownEverywhere(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, mainUsage, err := runIn(t, dir, nil, "-h")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range subcommands {
		if !strings.Contains(mainUsage, usageLines("       ", c.name, c.synopsis)) {
			t.Errorf("orchestra -h lacks %s's synopsis:\n%s", c.name, mainUsage)
		}
		if _, own, err := runIn(t, dir, nil, c.name, "-h"); err != nil ||
			!strings.HasPrefix(own, usageLines("Usage: ", c.name, c.synopsis)+"\n") {
			t.Errorf("orchestra %s -h: %v\n%s", c.name, err, own)
		}
		want := "orchestra: " + c.name + " comes before its flags:\n" + usageLines("  ", c.name, c.synopsis)
		if _, hint, err := runIn(t, dir, nil, "--plain", c.name); exitOf(err) != 2 || hint != want {
			t.Errorf("orchestra --plain %s: exit %d\n%s", c.name, exitOf(err), hint)
		}
	}
}

// A subcommand's synopsis names each of its flags, its aliases and shorthands aside.
func TestSubcommandSynopsisNamesEveryFlag(t *testing.T) {
	t.Parallel()
	flagLine := regexp.MustCompile(`(?m)^  -+(\S+).*\n\s+(.*)$`)
	named := regexp.MustCompile(`\[--([a-z-]+)`)
	for _, c := range subcommands {
		_, help, err := runIn(t, t.TempDir(), nil, c.name, "-h")
		if err != nil {
			t.Fatal(err)
		}
		var flags []string
		for _, m := range flagLine.FindAllStringSubmatch(help, -1) {
			if !strings.HasPrefix(m[2], "alias of ") && !strings.HasPrefix(m[2], "shorthand for ") {
				flags = append(flags, m[1])
			}
		}
		var listed []string
		for _, m := range named.FindAllStringSubmatch(c.synopsis, -1) {
			listed = append(listed, m[1])
		}
		slices.Sort(flags)
		slices.Sort(listed)
		if len(flags) == 0 || !slices.Equal(flags, listed) {
			t.Errorf("%s: flags %q, synopsis lists %q", c.name, flags, listed)
		}
	}
}

// close undoes what the session set up, the last first, each given the run's result.
func TestSessionCloseUndoesLastFirst(t *testing.T) {
	t.Parallel()
	var got []string
	s := &session{}
	for _, name := range []string{"lock", "stops", "log", "newer"} {
		s.onClose(func(err error) { got = append(got, name+":"+strconv.Itoa(exitCode(err))) })
	}
	err := status(dispatch.ExitSetup)
	s.close(&err)
	if want := []string{"newer:2", "log:2", "stops:2", "lock:2"}; !slices.Equal(got, want) {
		t.Errorf("close ran %q, want %q", got, want)
	}

	got = nil
	(&session{}).close(new(error)) // nothing set up: nothing to undo
	if len(got) != 0 {
		t.Errorf("an empty session's close ran %q", got)
	}
}
