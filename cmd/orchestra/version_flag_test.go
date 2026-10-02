package main

import (
	"strings"
	"testing"
)

// -v and --version print the version and exit 0, and -version, which scripts may use, still does.
func TestVersionFlagForms(t *testing.T) {
	dir := t.TempDir()
	want := "orchestra " + buildVersion() + "\n"
	for _, arg := range []string{"-v", "--version", "-version"} {
		stdout, stderr, err := runIn(t, dir, nil, arg)
		if exitOf(err) != 0 || stdout != want || stderr != "" {
			t.Errorf("%s: exit %d (%v), stdout %q (want %q), stderr %q", arg, exitOf(err), err, stdout, want, stderr)
		}
	}
}

func TestHelpListsVAsTheVersionShorthand(t *testing.T) {
	_, stderr, err := runIn(t, t.TempDir(), nil, "-h")
	if err != nil {
		t.Fatalf("-h: %v", err)
	}
	for _, line := range []string{"  -v\tshorthand for --version\n", "  -version\n    \tprint the version and exit\n"} {
		if !strings.Contains(stderr, line) {
			t.Errorf("-h lacks %q:\n%s", line, stderr)
		}
	}
}
