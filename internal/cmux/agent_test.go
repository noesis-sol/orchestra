package cmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A launch runs the agent in the terminal's shell through a script, which goes to the terminal's
// folder, sets orchestra's PATH, turns cmux's own claude hooks off, writes its process ID and gives
// the agent its arguments exactly; the shell is given one line that runs it.
func TestLaunchInPaneRunsTheAgentThroughAScript(t *testing.T) {
	f := newFakeCmux(t)
	term := bookOf(t)
	t.Setenv("PATH", os.Getenv("PATH")+string(os.PathListSeparator)+"/opt/claude/bin")
	args := []string{"--settings", "/w/.orchestra/run/hooks.json", "--append-system-prompt-file", "/w/rules.md",
		"two\nlines, 'quotes' and a back\\slash"}
	if err := term.LaunchInPane(bg, strings.ToLower(workSF), "claude", args); err != nil {
		t.Fatal(err)
	}
	script := term.launchPath(workSF, ".sh")
	want := [][]string{
		{"send", "--workspace", workWS, "--surface", strings.ToLower(workSF), "--", " /bin/sh '" + script + "'"},
		{"send-key", "--workspace", workWS, "--surface", strings.ToLower(workSF), "--", "enter"},
	}
	if got := f.writes(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("cmux got:\n%q\nwant:\n%q", got, want)
	}
	b, _ := term.view()
	if e := b.Surfaces[workSF]; e.Kind != "claude" || e.Records != "/w/.orchestra/run" || e.Agent != "" {
		t.Errorf("the book has %+v", e)
	}
	// Run with printf in place of the agent, the script gives it its arguments, from its folder,
	// with orchestra's PATH and without cmux's hooks, and writes its process ID.
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	fake := strings.Replace(string(body), "exec 'claude'",
		`printf '%s\0' "$(pwd -P)" "$PATH" "$CMUX_CLAUDE_HOOKS_DISABLED"`, 1)
	out, err := exec.Command("/bin/sh", "-c", fake).Output()
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := filepath.EvalSymlinks(b.Surfaces[workSF].Dir)
	wantOut := append([]string{dir, os.Getenv("PATH"), "1"}, args...)
	if got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00"); !slices.Equal(got, wantOut) {
		t.Errorf("the agent would get %q, want %q", got, wantOut)
	}
	if _, _, ok := term.launched(workSF); !ok {
		t.Error("the script wrote no process ID")
	}
	if err := term.LaunchInPane(bg, userWS, "claude", nil); !Missing(err) {
		t.Errorf("a terminal orchestra didn't open: %v", err)
	}
}

// StartAgent names the agent, taking the name from a terminal that held it, and waits for it to be
// idle; one gone at once is an error.
func TestStartAgentNamesItAndWaitsForItToBeReady(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	idle := idleRecords(t)
	keep(t, term, book{Workspaces: map[string]workspaceEntry{workWS: {Label: "x-1"}},
		Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS, Dir: "/w"},
			splitSF: {Workspace: ownWS, Agent: "x-1", Kind: "claude"}}})
	f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
	f.answer("top", topJSON(workSF, 4242))
	f.write("after-send-key", "echo 4242 > '"+term.launchPath(workSF, ".pid")+"'")
	if err := term.StartAgent(bg, "x-1", "claude", workSF, []string{"--settings", filepath.Join(idle, "hooks.json")}); err != nil {
		t.Fatal(err)
	}
	b, _ := term.view()
	if b.Surfaces[workSF].Agent != "x-1" || b.Surfaces[splitSF].Agent != "" {
		t.Errorf("the book has %+v", b.Surfaces)
	}
	if got := cmdOf(f.call("top")); !slices.Equal(got, []string{"top", "--processes", "--workspace", workWS}) {
		t.Errorf("top got %q", got)
	}

	f.answer("top", topJSON(workSF, 1))
	if err := term.StartAgent(bg, "x-1", "claude", workSF, []string{"--settings", filepath.Join(idle, "hooks.json")}); !Missing(err) {
		t.Errorf("an agent that exited at once: %v", err)
	}
}

func TestStatus(t *testing.T) {
	trust := "Quick safety check: is this a project you created or one you trust?\n\n❯ 1. Yes, I trust this folder\n  2. No, exit\n"
	for _, c := range []struct {
		name    string
		records string
		pid     int // the launch's, 0 for none yet
		running []int
		gone    bool // cmux has no such terminal
		screen  string
		want    dispatch.AgentState
	}{
		{"idle", idleRecords(t), 7, []int{7}, false, "", dispatch.StateIdle},
		{"working", workingRecords(t), 7, []int{7}, false, trust, dispatch.StateWorking},
		{"session ended", endedRecords(t), 7, []int{7}, false, "", dispatch.StateGone},
		{"killed", workingRecords(t), 7, []int{3}, false, "", dispatch.StateGone},
		{"its terminal closed", workingRecords(t), 7, []int{7}, true, "", dispatch.StateGone},
		{"not launched yet", records(t), 0, nil, false, "", dispatch.StateUnknown},
		{"trust dialog", records(t), 7, []int{7}, false, trust, dispatch.StateBlocked},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeCmux(t)
			term := terminal(t)
			keep(t, term, book{Surfaces: map[string]surfaceEntry{
				workSF: {Workspace: workWS, Agent: "x-1", Kind: "claude", Records: c.records}}})
			if c.pid != 0 {
				launchedPID(t, term, workSF, c.pid)
			}
			if c.gone {
				f.answer("tree", treeJSON(ws{workWS, "x-1", nil}))
			} else {
				f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
			}
			f.answer("top", topJSON(workSF, c.running...))
			f.answer("read-screen", c.screen)
			if st, err := term.Status(bg, "x-1"); st != c.want || err != nil {
				t.Errorf("got %s %v, want %s", st, err, c.want)
			}
		})
	}
	f := newFakeCmux(t)
	term := terminal(t)
	if st, err := term.Status(bg, "x-9"); st != dispatch.StateGone || err != nil {
		t.Errorf("no such agent: %s %v", st, err)
	}
	keep(t, term, book{Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS, Agent: "x-1", Kind: "claude"}}})
	f.serve()
	f.fail("tree", "Error: Command timed out")
	if st, err := term.Status(bg, "x-1"); st != "" || err == nil {
		t.Errorf("cmux not answering: %s %v", st, err)
	}
}

// promptTerminal is a terminal whose book has agent x-1, working once prompted, in its worktree.
func promptTerminal(t *testing.T, f *fakeCmux) (Terminal, string) {
	t.Helper()
	pasteSettle = 0
	t.Cleanup(func() { pasteSettle = 300e6 })
	term := terminal(t)
	wt := t.TempDir()
	keep(t, term, book{Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS, Dir: wt, Agent: "x-1",
		Kind: "claude", Records: workingRecords(t)}}})
	launchedPID(t, term, workSF, 7)
	f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
	f.answer("top", topJSON(workSF, 7))
	return term, wt
}

// A prompt of one line is typed into the input box, and Enter pressed.
func TestPromptTypesALine(t *testing.T) {
	f := newFakeCmux(t)
	term, _ := promptTerminal(t, f)
	if err := term.Prompt(bg, "x-1", "Continue with your ticket."); err != nil {
		t.Fatal(err)
	}
	sent := f.writes()
	want := [][]string{
		{"send", "--workspace", workWS, "--surface", workSF, "--", "Continue with your ticket."},
		{"send-key", "--workspace", workWS, "--surface", workSF, "--", "enter"},
	}
	if !slices.EqualFunc(sent, want, slices.Equal) {
		t.Errorf("cmux got %q, want %q", sent, want)
	}
	if err := term.Prompt(bg, "x-9", "hello"); !Missing(err) {
		t.Errorf("no such agent: %v", err)
	}
}

// A prompt of several lines, or with a backslash, which cmux would type as Enter, is written to a
// file in the worktree's .orchestra/run/, and the agent is pointed at it.
func TestPromptOfSeveralLinesGoesThroughAFile(t *testing.T) {
	f := newFakeCmux(t)
	term, wt := promptTerminal(t, f)
	prompt := "The check failed:\n\n  FAIL a\\nb\n\nFix it."
	if err := term.Prompt(bg, "x-1", prompt); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join(".orchestra", "run", "cmux-prompt-"+workSF+".md")
	if b, err := os.ReadFile(filepath.Join(wt, rel)); err != nil || string(b) != prompt {
		t.Errorf("the file has %q, %v", b, err)
	}
	line := cmdOf(f.call("send"))
	if !strings.Contains(line[len(line)-1], "@"+rel+" ") || !typeable(line[len(line)-1]) {
		t.Errorf("typed %q", line)
	}
}

func TestSendKeysMapsHerdrsNames(t *testing.T) {
	f := newFakeCmux(t)
	term, _ := promptTerminal(t, f)
	if err := term.SendKeys(bg, "x-1", "Esc", "ctrl+c", "shift+tab", "Down", "y", "enter"); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range f.writes() {
		got = append(got, c[0]+" "+c[len(c)-1])
	}
	want := []string{"send-key escape", "send-key ctrl+c", "send-key shift+tab", "send-key down", "send y",
		"send-key enter"}
	if !slices.Equal(got, want) {
		t.Errorf("cmux got %q, want %q", got, want)
	}
	if err := term.SendKeys(bg, "x-1", "two\nlines"); err == nil {
		t.Error("text cmux would take for Enter typed")
	}
}

// The screen of a worker at work is its visible screen; otherwise the last 60 lines.
func TestScreen(t *testing.T) {
	f := newFakeCmux(t)
	term, _ := promptTerminal(t, f)
	f.answer("read-screen", "❯ \n\n\n")
	if got := term.Screen(bg, "x-1", dispatch.StateWorking); got != "❯ \n" {
		t.Errorf("got %q", got)
	}
	if got := cmdOf(f.call("read-screen")); !slices.Equal(got, []string{"read-screen", "--workspace", workWS,
		"--surface", workSF}) {
		t.Errorf("read-screen got %q", got)
	}
	_ = term.Screen(bg, "x-1", dispatch.StateIdle)
	last := cmdOf(f.calls()[len(f.calls())-1])
	if !slices.Equal(last[len(last)-3:], []string{"--scrollback", "--lines", "60"}) {
		t.Errorf("read-screen got %q", last)
	}
	f.fail("read-screen", "Error: not_found: Workspace not found")
	if got := term.Screen(bg, "x-1", dispatch.StateIdle); got != "" {
		t.Errorf("an unreadable screen: %q", got)
	}
}

func TestNamingAgents(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	keep(t, term, book{Surfaces: map[string]surfaceEntry{
		workSF:  {Workspace: workWS, Kind: "claude", Records: workingRecords(t)},
		splitSF: {Workspace: ownWS, Agent: "x-1", Kind: "claude"},
		ownSF:   {Workspace: ownWS, Agent: "x-1-1"},
	}})
	launchedPID(t, term, workSF, 7)
	f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
	f.answer("top", topJSON(workSF, 7))

	if name, kind, st, err := term.PaneAgent(bg, workSF); name != "" || kind != "claude" ||
		st != dispatch.StateWorking || err != nil {
		t.Errorf("PaneAgent: %q %q %s %v", name, kind, st, err)
	}
	if _, _, st, err := term.PaneAgent(bg, userWS); st != dispatch.StateGone || err != nil {
		t.Errorf("PaneAgent of a terminal orchestra didn't open: %s %v", st, err)
	}
	if free := term.FreeName(bg, "x-1"); free != "x-1-2" {
		t.Errorf("FreeName: %q", free)
	}
	if _, err := term.AdoptAgent(bg, workSF, "codex", "x-1"); !Missing(err) {
		t.Errorf("adopting an agent of another kind: %v", err)
	}
	if st, err := term.AdoptAgent(bg, workSF, "claude", "x-1"); st != dispatch.StateWorking || err != nil {
		t.Errorf("AdoptAgent: %s %v", st, err)
	}
	b, _ := term.view()
	if b.Surfaces[workSF].Agent != "x-1" || b.Surfaces[splitSF].Agent != "" {
		t.Errorf("the book has %+v", b.Surfaces)
	}
	if err := term.RenameAgent(bg, strings.ToLower(workSF), "x-1-3"); err != nil {
		t.Fatal(err)
	}
	if err := term.RenameAgent(bg, "x-1-3", "x-1"); err != nil {
		t.Fatal(err)
	}
	if err := term.RenameAgent(bg, "x-7", "x-8"); !Missing(err) {
		t.Errorf("renaming no agent: %v", err)
	}
	if b, _ := term.view(); b.Surfaces[workSF].Agent != "x-1" {
		t.Errorf("the book has %+v", b.Surfaces)
	}
}
