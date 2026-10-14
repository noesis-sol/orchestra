package tmux

import (
	"slices"
	"testing"
)

// Text that starts with '-' is typed, not read as send-keys options; text that ends in ';', which
// tmux takes for the end of a command, has those semicolons sent by their code; return is Enter, as
// in cmux.
func TestSendKeysTypesDashesAndSemicolons(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude"})
	if err := (Terminal{}).SendKeys(bg, "x-1", "-x", "a;;", ";", "return"); err != nil {
		t.Fatal(err)
	}
	want := []string{"send-keys", "-t", "%5", "-l", "--", "-x", ";", "send-keys", "-t", "%5", "-l", "--", "a", ";",
		"send-keys", "-t", "%5", "-H", "3b", "3b", ";", "send-keys", "-t", "%5", "-H", "3b", ";",
		"send-keys", "-t", "%5", "Enter"}
	if got := f.call("send-keys"); !slices.Equal(got, want) {
		t.Errorf("send-keys got:\n%q\nwant:\n%q", got, want)
	}
}

// In a real tmux, such text arrives as it was given.
func TestLiveSendKeysTypesDashesAndSemicolons(t *testing.T) {
	l := newLive(t)
	l.script(false, "start", "keys", "idle")
	_, pane := l.tab("x-1")
	if err := l.term.StartAgent(bg, "x-1", "claude", pane, l.hooks()); err != nil {
		t.Fatal(err)
	}
	keys := []string{"-x --y", "return", "a;", `b \;`, ";", "return", "exit", "enter"}
	if err := l.term.SendKeys(bg, "x-1", keys...); err != nil {
		t.Fatal(err)
	}
	const want = "-x --y\na;b " + `\;;` + "\nexit\n"
	l.eventually("the keys never arrived", func() bool { return l.file("keys") == want })
}
