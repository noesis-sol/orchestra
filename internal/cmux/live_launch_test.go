package cmux

import (
	"os"
	"path/filepath"
	"testing"
)

// A launch into a terminal whose last launch still runs there, a Claude Code on its trust dialog
// say, types nothing into it, as a retried start would; nor while cmux can't say whether it runs.
// Once it has gone the launch goes in.
func TestLaunchIsRefusedWhileTheLastOneRuns(t *testing.T) {
	f := newFakeCmux(t)
	term := bookOf(t)
	launchedPID(t, term, workSF, 4242)
	f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
	f.answer("top", topJSON(workSF, 4242))
	if err := term.StartAgent(bg, "x-1", "claude", workSF, nil); err == nil {
		t.Error("a start into a terminal that runs claude")
	}
	if err := term.LaunchInPane(bg, workSF, "claude", nil); err == nil {
		t.Error("a launch into a terminal that runs claude")
	}
	if got := f.writes(); len(got) > 0 {
		t.Errorf("typed into a terminal that runs claude: %q", got)
	}
	if pid, _, ok := term.launched(workSF); !ok || pid != 4242 {
		t.Errorf("the running launch's process ID went: %d %v", pid, ok)
	}

	f.serve()
	f.fail("top", "Error: Command timed out")
	if err := term.LaunchInPane(bg, workSF, "claude", nil); err == nil {
		t.Error("a launch while cmux can't list the terminal's processes")
	}
	if got := f.writes(); len(got) > 0 {
		t.Errorf("typed into a terminal that may run claude: %q", got)
	}

	if err := os.Remove(filepath.Join(f.dir, "fail-top")); err != nil {
		t.Fatal(err)
	}
	f.answer("top", topJSON(workSF, 1))
	if err := term.LaunchInPane(bg, workSF, "claude", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.writes(); len(got) != 2 {
		t.Errorf("a launch once the last one ended: %q", got)
	}
}

// TypedPrompt is what Prompt puts in the input box: before 0.65 the line attaching the file a prompt
// of several lines is written to, and the prompt itself for one of one line; from 0.65, which
// pastes, the prompt.
func TestTypedPromptIsWhatPromptPutsInTheBox(t *testing.T) {
	several := "two\nlines"
	rel := filepath.Join(".orchestra", "run", "cmux-prompt-"+workSF+".md")
	for _, c := range []struct {
		name, version, prompt, want string
	}{
		{"0.64, several lines", version064, several,
			"My message to you is in @" + rel + " : read it, and take it as if I had typed it here."},
		{"0.64, one line", version064, "Continue with your ticket.", "Continue with your ticket."},
		{"0.65, several lines", version065, several, several},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeCmux(t)
			f.answer("version", c.version)
			term, _ := promptTerminal(t, f)
			got := term.TypedPrompt(bg, "x-1", c.prompt)
			if got != c.want {
				t.Errorf("TypedPrompt = %q, want %q", got, c.want)
			}
			if err := term.Prompt(bg, "x-1", c.prompt); err != nil {
				t.Fatal(err)
			}
			given := f.writes()[0]
			if given[0] == "paste" {
				given = append(given, f.pasted(1))
			}
			if typed := given[len(given)-1]; typed != got {
				t.Errorf("Prompt put %q in the box, TypedPrompt says %q", typed, got)
			}
		})
	}
	f := newFakeCmux(t)
	term, _ := promptTerminal(t, f)
	if got := term.TypedPrompt(bg, "x-9", several); got != several {
		t.Errorf("no such agent: %q", got)
	}
}
