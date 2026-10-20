package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
	"github.com/noesis-sol/orchestra/internal/hookterm"
)

func TestAdoptAgent(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", kind: "claude", records: workingRecords(t), command: "2.1.0"})
	st, err := Terminal{}.AdoptAgent(bg, "%5", "claude", "x-1")
	if st != agentio.StateWorking || err != nil {
		t.Fatalf("got %q %v", st, err)
	}
	if got := f.call("set-option"); !slices.Equal(got, []string{"set-option", "-p", "-t", "%5", "@orchestra-agent", "x-1"}) {
		t.Errorf("set-option got %q", got)
	}
	if _, err := (Terminal{}).AdoptAgent(bg, "%6", "claude", "x-1"); !Missing(err) {
		t.Errorf("no such pane: %v", err)
	}
	if _, err := (Terminal{}).AdoptAgent(bg, "%5", "codex", "x-1"); !Missing(err) {
		t.Errorf("another kind: %v", err)
	}
}

func TestPaneAgent(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idleRecords(t), command: "2.1.0"},
		paneLine{id: "%6", command: "zsh"})
	if n, k, st, err := (Terminal{}).PaneAgent(bg, "%5"); n != "x-1" || k != "claude" || st != agentio.StateIdle || err != nil {
		t.Errorf("an agent: %q %q %q %v", n, k, st, err)
	}
	for _, p := range []string{"%6", "%7"} {
		if n, k, st, err := (Terminal{}).PaneAgent(bg, p); n != "" || k != "" || st != agentio.StateGone || err != nil {
			t.Errorf("no agent in %s: %q %q %q %v", p, n, k, st, err)
		}
	}
	f.serve()
	f.fail("list-panes", "lost server")
	if _, _, st, err := (Terminal{}).PaneAgent(bg, "%5"); st != "" || err == nil {
		t.Errorf("tmux failing says nothing of the agent: %q %v", st, err)
	}
}

func TestRenameAgentAndFreeName(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"}, paneLine{id: "%6", agent: "x-1-1", kind: "claude"},
		paneLine{id: "%7", kind: "claude"})
	if got := (Terminal{}).FreeName(bg, "x-1"); got != "x-1-2" {
		t.Errorf("FreeName = %q", got)
	}
	if err := (Terminal{}).RenameAgent(bg, "x-1", "x-1-2"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("set-option"); !slices.Equal(got, []string{"set-option", "-p", "-t", "%5", "@orchestra-agent", "x-1-2"}) {
		t.Errorf("by name: %q", got)
	}
	f = newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"}, paneLine{id: "%7", kind: "claude"})
	if err := (Terminal{}).RenameAgent(bg, "%7", "x-1"); err != nil {
		t.Fatal(err)
	}
	want := []string{"set-option", "-p", "-u", "-t", "%5", "@orchestra-agent", ";",
		"set-option", "-p", "-t", "%7", "@orchestra-agent", "x-1"}
	if got := f.call("set-option"); !slices.Equal(got, want) {
		t.Errorf("by pane: %q", got)
	}
	if err := (Terminal{}).RenameAgent(bg, "y-2", "y"); !Missing(err) {
		t.Errorf("no such agent: %v", err)
	}
	if err := (Terminal{}).RenameAgent(bg, "%7", "x;"); err == nil || NoServer(err) {
		t.Errorf("a name tmux would take for the end of a command: %v", err)
	}

	f.fail("list-panes", "no server running")
	if got := (Terminal{}).FreeName(bg, "x-1"); got != "x-1-1" {
		t.Errorf("no server holds no names: %q", got)
	}
	f.serve()
	if got := (Terminal{}).FreeName(bg, "x-1"); got != "" {
		t.Errorf("unknown names may be taken: %q", got)
	}
}

func TestStatus(t *testing.T) {
	trust := "Quick safety check: is this a project you created or one you trust?\n\n❯ 1. Yes, I trust this folder\n  2. No, exit\n"
	mcp := "New MCP server found in this project: github\n❯ 1. Use this and all future MCP servers\n"
	for _, c := range []struct {
		name   string
		pane   paneLine
		screen string
		want   agentio.State
	}{
		{"idle", paneLine{records: idleRecords(t)}, "", agentio.StateIdle},
		{"working", paneLine{records: workingRecords(t)}, trust, agentio.StateWorking},
		{"session ended", paneLine{records: endedRecords(t)}, "", agentio.StateGone},
		{"exited", paneLine{records: workingRecords(t), dead: true}, "", agentio.StateGone},
		{"back at its shell", paneLine{records: workingRecords(t), command: "-zsh"}, "", agentio.StateGone},
		{"its shell, starting", paneLine{records: records(t), command: "sh"}, "", agentio.StateUnknown},
		{"launched, a wrapper script", paneLine{records: workingRecords(t), command: "bash", launched: true}, "",
			agentio.StateWorking},
		{"launched, a wrapper script, idle", paneLine{records: idleRecords(t), command: "sh", launched: true}, "",
			agentio.StateIdle},
		{"launched, exited", paneLine{records: workingRecords(t), command: "bash", launched: true, dead: true}, "",
			agentio.StateGone},
		{"no hook yet", paneLine{records: records(t)}, "Welcome to Claude Code", agentio.StateUnknown},
		{"trust dialog", paneLine{records: records(t)}, trust, agentio.StateBlocked},
		{"MCP dialog", paneLine{records: idleRecords(t)}, mcp, agentio.StateBlocked},
		{"no agent launched", paneLine{agent: "x-1", command: "zsh"}, "", agentio.StateGone},
	} {
		f := newFakeTmux(t)
		p := c.pane
		p.id, p.agent = "%5", "x-1"
		if p.kind == "" && c.name != "no agent launched" {
			p.kind = "claude"
		}
		if p.command == "" {
			p.command = "2.1.0"
		}
		f.listPanes(paneLine{id: "%1", agent: "x-2", kind: "claude"}, p)
		f.answer("capture-pane", c.screen)
		if st, err := (Terminal{}).Status(bg, "x-1"); st != c.want || err != nil {
			t.Errorf("%s: %q %v, want %q", c.name, st, err, c.want)
		}
	}
}

func TestStatusWithoutTheAgentOrTmux(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%1", agent: "x-2", kind: "claude"})
	if st, err := (Terminal{}).Status(bg, "x-1"); st != agentio.StateGone || err != nil {
		t.Errorf("no pane holds it: %q %v", st, err)
	}
	f.answer("list-panes", "%1 @1 0\n")
	if st, err := (Terminal{}).Status(bg, "x-1"); st != "" || err == nil {
		t.Errorf("odd output: %q %v", st, err)
	}
	f.fail("list-panes", "no server running")
	if st, err := (Terminal{}).Status(bg, "x-1"); st != agentio.StateGone || err != nil {
		t.Errorf("no server: %q %v", st, err)
	}
	f.serve()
	if st, err := (Terminal{}).Status(bg, "x-1"); st != "" || err == nil || NoServer(err) {
		t.Errorf("a server that fails the call: %q %v", st, err)
	}
	t.Setenv("PATH", t.TempDir())
	if st, err := (Terminal{}).Status(bg, "x-1"); st != "" || !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("tmux missing: %q %v", st, err)
	}
}

// An agent launched without hooks (none passed) is read from its worktree's records.
func TestStatusOfAnAgentLaunchedWithoutHooks(t *testing.T) {
	f := newFakeTmux(t)
	wt := t.TempDir()
	run := filepath.Join(wt, ".orchestra", "run")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "turn.json"), []byte(`{"hook_event_name":"UserPromptSubmit"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", command: "2.1.0", dir: wt})
	if st, err := (Terminal{}).Status(bg, "x-1"); st != agentio.StateWorking || err != nil {
		t.Errorf("got %q %v", st, err)
	}
}

func TestScreen(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"})
	f.answer("capture-pane", "⏺ Bash(go test)\n\n❯ \n\n\n\n")
	for st, want := range map[agentio.State][]string{
		agentio.StateWorking: {"capture-pane", "-p", "-J", "-t", "%5"},
		agentio.StateBlocked: {"capture-pane", "-p", "-J", "-t", "%5"},
		agentio.StateIdle:    {"capture-pane", "-p", "-J", "-t", "%5", "-S", "-80"},
		"":                   {"capture-pane", "-p", "-J", "-t", "%5", "-S", "-80"},
	} {
		if err := os.Remove(filepath.Join(f.dir, "calls")); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if got := (Terminal{}).Screen(bg, "x-1", st); got != "⏺ Bash(go test)\n\n❯ \n" {
			t.Errorf("Screen(%q) = %q", st, got)
		}
		if got := f.call("capture-pane"); !slices.Equal(got, want) {
			t.Errorf("Screen(%q) ran %q", st, got)
		}
	}
	if got := (Terminal{}).Screen(bg, "x-2", ""); got != "" {
		t.Errorf("no such agent: %q", got)
	}
	f.fail("capture-pane", "boom")
	if got := (Terminal{}).Screen(bg, "x-1", ""); got != "" {
		t.Errorf("unreadable: %q", got)
	}
}

// noPasteSettle has Prompt press Enter at once, for the test.
func noPasteSettle(t *testing.T) {
	was := hookterm.PasteSettle
	hookterm.PasteSettle = 0
	t.Cleanup(func() { hookterm.PasteSettle = was })
}

func TestPromptPastesItAndWaitsForTheAgentToStart(t *testing.T) {
	noPasteSettle(t)
	f := newFakeTmux(t)
	rec := idleRecords(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: rec, command: "2.1.0"})
	// Enter submits it: the hook records the prompt.
	f.after("send-keys", `printf '%s' '{"hook_event_name":"UserPromptSubmit"}' > '`+rec+`/turn.json'`)
	prompt := "do the ticket;\nline two with 'quotes'"
	if err := (Terminal{}).Prompt(bg, "x-1", prompt); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "buffer")); string(b) != prompt {
		t.Errorf("the buffer holds %q", b)
	}
	if got := f.ops(); !slices.Equal(got, []string{"list-panes", "load-buffer", "paste-buffer", "send-keys", "list-panes"}) {
		t.Errorf("tmux got %v", got)
	}
	for _, want := range [][]string{
		{"load-buffer", "-b", "orchestra-5", "-"},
		{"paste-buffer", "-d", "-p", "-b", "orchestra-5", "-t", "%5"},
		{"send-keys", "-t", "%5", "Enter"},
	} {
		if got := f.call(want[0]); !slices.Equal(got, want) {
			t.Errorf("got %q, want %q", got, want)
		}
	}

	f = newFakeTmux(t)
	f.listPanes()
	if err := (Terminal{}).Prompt(bg, "x-1", "hi"); !Missing(err) {
		t.Errorf("no such agent: %v", err)
	}
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idleRecords(t), command: "2.1.0"})
	f.fail("paste-buffer", "no buffer")
	if err := (Terminal{}).Prompt(bg, "x-1", "hi"); err == nil || slices.Contains(f.ops(), "send-keys") {
		t.Errorf("a failed paste: %v, %v", err, f.ops())
	}
}

// Prompt fails once the agent is gone rather than wait out its 30 seconds.
func TestPromptToAnAgentThatExits(t *testing.T) {
	noPasteSettle(t)
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: idleRecords(t), command: "2.1.0"})
	f.after("send-keys", `printf '%s\n' '`+paneLine{id: "%5", agent: "x-1", kind: "claude", dead: true}.String()+
		`' > "$d/out-list-panes"`)
	if err := (Terminal{}).Prompt(bg, "x-1", "hi"); !Missing(err) {
		t.Errorf("got %v", err)
	}
}

func TestSendKeysByHerdrsNames(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"})
	if err := (Terminal{}).SendKeys(bg, "x-1", "enter", "esc", "ctrl+c", "Up", "shift+tab", "y", "yes"); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "%5", "Enter", ";", "send-keys", "-t", "%5", "Escape", ";",
		"send-keys", "-t", "%5", "C-c", ";", "send-keys", "-t", "%5", "Up", ";", "send-keys", "-t", "%5", "BTab", ";",
		"send-keys", "-t", "%5", "-l", "--", "y", ";", "send-keys", "-t", "%5", "-l", "--", "yes"}
	if got := f.call("send-keys"); !slices.Equal(got, want) {
		t.Errorf("send-keys got:\n%q\nwant:\n%q", got, want)
	}
	if err := (Terminal{}).SendKeys(bg, "x-2", "enter"); !Missing(err) {
		t.Errorf("no such agent: %v", err)
	}
}

func TestWaits(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: waits several polls for the fake panes' states")
	}
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "idle", kind: "claude", records: idleRecords(t), command: "2.1.0"},
		paneLine{id: "%6", agent: "working", kind: "claude", records: workingRecords(t), command: "2.1.0"},
		paneLine{id: "%7", agent: "dead", kind: "claude", dead: true})
	term := Terminal{}
	if !term.WaitReady(bg, "idle") || term.WaitReady(bg, "dead") || term.WaitReady(bg, "none") {
		t.Error("WaitReady: idle is ready, gone isn't")
	}
	if !term.WaitStarted(bg, "working") || term.WaitStarted(bg, "dead") {
		t.Error("WaitStarted: working has started, gone hasn't")
	}
	ctx, cancel := context.WithTimeout(bg, 3*hookterm.Poll)
	defer cancel()
	if term.WaitStarted(ctx, "idle") {
		t.Error("an idle agent hasn't started")
	}
	if ctx.Err() == nil {
		t.Error("it should have waited until stopped")
	}
}
