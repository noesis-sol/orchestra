package tmux

import (
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestCreateTabOpensANamedWindowThatKeepsItsName(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("new-window", "@4 %9\n")
	tab, pane, err := Terminal{}.CreateTab(bg, "orchestra-repo", "/work/a#b", "orchestra-x.1")
	if err != nil || tab != "@4" || pane != "%9" {
		t.Fatalf("got %q %q %v", tab, pane, err)
	}
	want := [][]string{
		{"new-window", "-d", "-t", "=orchestra-repo:", "-c", "/work/a##b", "-n", "orchestra-x.1", "-P", "-F",
			"#{window_id} #{pane_id}"},
		{"set-option", "-w", "-t", "@4", "automatic-rename", "off", ";", "set-option", "-w", "-t", "@4",
			"allow-rename", "off"},
	}
	if got := f.calls(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("tmux got:\n%q\nwant:\n%q", got, want)
	}
}

func TestCreateTabClosesAWindowItCannotUse(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("new-window", "@4 nonsense\n")
	_, _, err := Terminal{}.CreateTab(bg, "s", "/w", "x-1")
	if err == nil || !strings.Contains(err.Error(), "window @4, which tmux opened, is closed again") {
		t.Errorf("an answer naming the window but no pane: %v", err)
	}
	if got := f.call("kill-window"); !slices.Equal(got, []string{"kill-window", "-t", "@4"}) {
		t.Errorf("kill-window got %q", got)
	}

	f = newFakeTmux(t)
	f.answer("new-window", "@4 %9\n")
	f.fail("set-option", "invalid option")
	if _, _, err := (Terminal{}).CreateTab(bg, "s", "/w", "x-1"); err == nil ||
		!strings.Contains(err.Error(), "is closed again") {
		t.Errorf("a window whose name can't be kept: %v", err)
	}
	f.call("kill-window")

	f = newFakeTmux(t)
	f.answer("new-window", "garbled")
	if _, _, err := (Terminal{}).CreateTab(bg, "s", "/w", "x-1"); err == nil ||
		!strings.Contains(err.Error(), "may be left open") {
		t.Errorf("an answer naming no window: %v", err)
	}
	if slices.Contains(f.ops(), "kill-window") {
		t.Error("no window to close")
	}
}

func TestCreateTabFailures(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("new-window", "no server running on /tmp/tmux-501/default")
	_, _, err := Terminal{}.CreateTab(bg, "s", "/w", "x-1")
	if !NoServer(err) {
		t.Errorf("no server: %#v", err)
	}
	var te *Error
	if !errors.As(err, &te) || te.Op != "new-window" || te.Target != "s" {
		t.Errorf("the error should say what failed: %#v", err)
	}
	f.serve()
	f.fail("new-window", "can't find session: s")
	if _, _, err := (Terminal{}).CreateTab(bg, "s", "/w", "x-1"); err == nil || NoServer(err) || Missing(err) {
		t.Errorf("a server that refuses the call: %v", err)
	}

	t.Setenv("PATH", t.TempDir())
	_, _, err = Terminal{}.CreateTab(bg, "s", "/w", "x-1")
	if !errors.Is(err, exec.ErrNotFound) || NoServer(err) {
		t.Errorf("tmux missing: %v", err)
	}
}

func TestCloseTab(t *testing.T) {
	f := newFakeTmux(t)
	if err := (Terminal{}).CloseTab(bg, "@3"); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); len(got) != 1 || !slices.Equal(got[0], []string{"kill-window", "-t", "@3"}) {
		t.Errorf("tmux got %q", got)
	}

	f.serve()
	f.fail("kill-window", "can't find window: @3")
	f.answer("list-windows", "@1\torchestra\n@4\tx-2\n")
	if err := (Terminal{}).CloseTab(bg, "@3"); err != nil {
		t.Errorf("a window already gone: %v", err)
	}
	f.answer("list-windows", "@3\tx-1\n")
	if err := (Terminal{}).CloseTab(bg, "@3"); err == nil {
		t.Error("a window that is still there but wasn't closed")
	}

	f = newFakeTmux(t)
	f.fail("kill-window", "no server running")
	f.fail("list-windows", "no server running")
	if err := (Terminal{}).CloseTab(bg, "@3"); err != nil {
		t.Errorf("no server, so no window: %v", err)
	}

	if err := (Terminal{}).CloseTab(bg, ""); !Missing(err) {
		t.Errorf("no window ID: %v", err)
	}
}

func TestTabLabel(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("list-windows", "@1\torchestra\n@3\tx-1.2\n@4\tname\twith tab\n")
	for tab, want := range map[string]struct {
		label string
		open  bool
	}{"@3": {"x-1.2", true}, "@4": {"name\twith tab", true}, "@5": {"", false}, "": {"", false}, "x": {"", false}} {
		label, open, err := Terminal{}.TabLabel(bg, tab)
		if label != want.label || open != want.open || err != nil {
			t.Errorf("TabLabel(%q) = %q %v %v", tab, label, open, err)
		}
	}
	if got := f.call("list-windows"); !slices.Equal(got, []string{"list-windows", "-a", "-F",
		"#{window_id}\t#{?@orchestra_label,#{@orchestra_label},#{window_name}}"}) {
		t.Errorf("list-windows got %q", got)
	}

	f.answer("list-windows", "garbled\n")
	if _, _, err := (Terminal{}).TabLabel(bg, "@3"); err == nil {
		t.Error("odd output should be an error")
	}

	f = newFakeTmux(t)
	f.fail("list-windows", "no server running")
	if _, open, err := (Terminal{}).TabLabel(bg, "@3"); open || err != nil {
		t.Errorf("no server, so no window: %v %v", open, err)
	}
	f.serve()
	if _, _, err := (Terminal{}).TabLabel(bg, "@3"); err == nil || NoServer(err) {
		t.Errorf("a server that fails the call says nothing of the window: %v", err)
	}
}

func TestFocusTab(t *testing.T) {
	f := newFakeTmux(t)
	if err := (Terminal{}).FocusTab(bg, "@3"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("select-window"); !slices.Equal(got, []string{"select-window", "-t", "@3"}) {
		t.Errorf("select-window got %q", got)
	}
}

func TestSplitAndClosePane(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("split-window", "%12\n")
	pane, err := Terminal{}.SplitPane(bg, "%2", "/repo#1")
	if err != nil || pane != "%12" {
		t.Fatalf("got %q %v", pane, err)
	}
	want := []string{"split-window", "-h", "-t", "%2", "-c", "/repo##1", "-P", "-F", "#{pane_id}"}
	if got := f.call("split-window"); !slices.Equal(got, want) {
		t.Errorf("split-window got %q", got)
	}
	f.answer("split-window", "oops\n")
	if _, err := (Terminal{}).SplitPane(bg, "%2", "/repo"); err == nil {
		t.Error("odd output should be an error")
	}

	if err := (Terminal{}).ClosePane(bg, "%12"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("kill-pane"); !slices.Equal(got, []string{"kill-pane", "-t", "%12"}) {
		t.Errorf("kill-pane got %q", got)
	}
	f.serve()
	f.fail("kill-pane", "can't find pane: %12")
	f.listPanes(paneLine{id: "%2"})
	if err := (Terminal{}).ClosePane(bg, "%12"); err != nil {
		t.Errorf("a pane already gone: %v", err)
	}
	f.listPanes(paneLine{id: "%12"})
	if err := (Terminal{}).ClosePane(bg, "%12"); err == nil {
		t.Error("a pane still there but not closed")
	}
}

func TestPaneOpen(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%1"}, paneLine{id: "%7", dead: true})
	for p, want := range map[string]bool{"%1": true, "%7": true, "%8": false} {
		if open, err := (Terminal{}).PaneOpen(bg, p); open != want || err != nil {
			t.Errorf("PaneOpen(%s) = %v %v", p, open, err)
		}
	}
	f.answer("list-panes", "%1\t@1\tmaybe\t0\t\t\t\t\tzsh\t/\n")
	if _, err := (Terminal{}).PaneOpen(bg, "%1"); err == nil {
		t.Error("odd output should be an error")
	}
	f.answer("list-panes", "%1\t@1\t0\tmaybe\t\t\t\t\tzsh\t/\n")
	if _, err := (Terminal{}).PaneOpen(bg, "%1"); err == nil {
		t.Error("an odd launched should be an error")
	}
	f.answer("list-panes", "%1\t@1\t0\n")
	if _, err := (Terminal{}).PaneOpen(bg, "%1"); err == nil {
		t.Error("too few fields should be an error")
	}
	f.fail("list-panes", "no server running")
	if open, err := (Terminal{}).PaneOpen(bg, "%1"); open || err != nil {
		t.Errorf("no server, so no pane: %v %v", open, err)
	}
}
