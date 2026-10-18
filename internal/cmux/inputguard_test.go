package cmux

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// cmux 0.64.22 and 0.65.0 as cmux version prints them.
const (
	version064 = "cmux 0.64.22 (812) [5c0ffee]\n"
	version065 = "cmux 0.65.0 (901) [a1b2c3d]\n"
)

func TestFeaturesOfVersion(t *testing.T) {
	all := features{force: true, paste: true}
	for _, c := range []struct {
		out  string
		want features
	}{
		{version064, features{}},
		{"cmux 0.64.25 (840)", features{}},
		{version065, all},
		{"cmux 0.65 (901)", all},
		{"cmux 0.66.1 (950) [abc1234]", all},
		{"cmux 1.0.0", all},
		{"cmux build 901", features{}},
		{"cmux version unknown", features{}},
		{"", features{}},
	} {
		if got := featuresOf(c.out); got != c.want {
			t.Errorf("%q: got %+v, want %+v", c.out, got, c.want)
		}
	}
}

// From 0.65.0, keys and text go with --force, before the key or text, which cmux's input guard
// would otherwise refuse to send into a dialog of Claude Code's.
func TestCmux065ForcesKeysIntoDialogs(t *testing.T) {
	f := newFakeCmux(t)
	f.answer("version", version065)
	term, _ := promptTerminal(t, f)
	if err := term.SendKeys(bg, "x-1", "Down", "1", "enter"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"send-key", "--workspace", workWS, "--surface", workSF, "--force", "--", "down"},
		{"send", "--workspace", workWS, "--surface", workSF, "--force", "--", "1"},
		{"send-key", "--workspace", workWS, "--surface", workSF, "--force", "--", "enter"},
	}
	if got := f.writes(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("cmux got:\n%q\nwant:\n%q", got, want)
	}
}

// Before 0.65.0 --force is no flag, but text cmux would type: it is left out.
func TestCmux064TypesNoForce(t *testing.T) {
	f := newFakeCmux(t)
	f.answer("version", version064)
	term, _ := promptTerminal(t, f)
	if err := term.SendKeys(bg, "x-1", "1", "enter"); err != nil {
		t.Fatal(err)
	}
	f.answer("top", topJSON(workSF, 1)) // the agent launched before has ended, for another to launch
	if err := term.LaunchInPane(bg, workSF, "claude", nil); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.writes() {
		if slices.Contains(c, "--force") {
			t.Errorf("cmux 0.64 got %q", c)
		}
	}
}

// From 0.65.0 a prompt of several lines, with a backslash, is pasted whole, from cmux paste's stdin,
// and Enter pressed; no file is written for it.
func TestCmux065PastesThePrompt(t *testing.T) {
	f := newFakeCmux(t)
	f.answer("version", version065)
	term, wt := promptTerminal(t, f)
	prompt := "The check failed:\n\n  FAIL a\\nb\n\nFix it."
	if err := term.Prompt(bg, "x-1", prompt); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"paste", "--workspace", workWS, "--surface", workSF, "--force", "-"},
		{"send-key", "--workspace", workWS, "--surface", workSF, "--force", "--", "enter"},
	}
	if got := f.writes(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("cmux got:\n%q\nwant:\n%q", got, want)
	}
	if got := f.pasted(1); got != prompt {
		t.Errorf("pasted %q, want %q", got, prompt)
	}
	if _, err := os.Stat(filepath.Join(wt, ".orchestra", "run", "cmux-prompt-"+workSF+".md")); !os.IsNotExist(err) {
		t.Errorf("a file was written for the prompt: %v", err)
	}

	f.fail("paste", "Error: not_found: Terminal surface not found")
	if err := term.Prompt(bg, "x-1", "hello"); err == nil {
		t.Error("a paste cmux refused")
	}
}

// Before 0.65.0 a prompt of several lines still goes through a file, pointed at with @.
func TestCmux064WritesThePromptToAFile(t *testing.T) {
	f := newFakeCmux(t)
	f.answer("version", version064)
	term, wt := promptTerminal(t, f)
	if err := term.Prompt(bg, "x-1", "two\nlines"); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.ops(), "paste") {
		t.Errorf("cmux 0.64 was asked to paste: %v", f.ops())
	}
	rel := filepath.Join(".orchestra", "run", "cmux-prompt-"+workSF+".md")
	if b, err := os.ReadFile(filepath.Join(wt, rel)); err != nil || string(b) != "two\nlines" {
		t.Errorf("the file has %q, %v", b, err)
	}
	if line := cmdOf(f.call("send")); !strings.Contains(line[len(line)-1], "@"+rel+" ") {
		t.Errorf("typed %q", line)
	}
}

// cmux is asked its version once for each version of its CLI: again once the file changes, as the
// app's update replaces it. A CLI that can't say is taken for an older one, and asked again.
func TestFeaturesAreAskedOncePerCLI(t *testing.T) {
	f := newFakeCmux(t)
	term, _ := promptTerminal(t, f)
	f.fail("version", "Error: something went wrong")
	if got := term.features(bg); got != (features{}) {
		t.Errorf("a cmux that can't say its version: %+v", got)
	}
	if err := os.Remove(filepath.Join(f.dir, "fail-version")); err != nil {
		t.Fatal(err)
	}
	f.answer("version", version064)
	for range 3 {
		if got := term.features(bg); got != (features{}) {
			t.Errorf("cmux 0.64: %+v", got)
		}
	}
	f.answer("version", version065)
	if got := term.features(bg); got != (features{}) {
		t.Errorf("the same CLI asked again: %+v", got)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(Bin(bg), later, later); err != nil {
		t.Fatal(err)
	}
	if got := term.features(bg); got != (features{force: true, paste: true}) {
		t.Errorf("the CLI updated to 0.65: %+v", got)
	}
	n := 0
	for _, op := range f.ops() {
		if op == "version" {
			n++
		}
	}
	if n != 3 {
		t.Errorf("cmux version was called %d times, want 3", n)
	}
}
