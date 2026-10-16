package tmux

import (
	"slices"
	"testing"
)

// refuseDottedWindowNames has the fake tmux answer new-window as tmux 3.7 does a name holding '.'
// or ':'.
func refuseDottedWindowNames(f *fakeTmux) {
	f.before("new-window", `n=; p=; for a; do [ "$p" = -n ] && n=$a; p=$a; done
case $n in *[.:]*) echo "invalid window name: $n" >&2; exit 1;; esac
`)
}

// tmux 3.7 refuses a window name holding '.', as a child ticket's ID does: the window is named as
// SessionName gives, and keeps the ticket's ID as its label, which TabLabel gives.
func TestCreateTabNamesAWindowTmuxRefusesTheLabelOf(t *testing.T) {
	f := newFakeTmux(t)
	refuseDottedWindowNames(f)
	f.answer("new-window", "@4 %9\n")
	tab, pane, err := Terminal{}.CreateTab(bg, "$1", "/work", "proj-abc.1")
	if err != nil || tab != "@4" || pane != "%9" {
		t.Fatalf("CreateTab = %q %q %v", tab, pane, err)
	}
	var names []string
	for _, c := range f.calls() {
		if c[0] == "new-window" {
			names = append(names, c[slices.Index(c, "-n")+1])
		}
	}
	if !slices.Equal(names, []string{"proj-abc.1", "proj-abc_1"}) {
		t.Errorf("new-window named the window %q, want proj-abc.1, then proj-abc_1", names)
	}
	want := []string{"set-option", "-w", "-t", "@4", "automatic-rename", "off", ";", "set-option", "-w", "-t", "@4",
		"allow-rename", "off", ";", "set-option", "-w", "-t", "@4", "@orchestra_label", "proj-abc.1"}
	if got := f.call("set-option"); !slices.Equal(got, want) {
		t.Errorf("set-option got:\n%q\nwant:\n%q", got, want)
	}
}

// A tmux that refuses the window for another reason refuses it under either name: its first error is
// the one returned.
func TestCreateTabSaysWhyTmuxRefusedTheWindow(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("new-window", "can't find session: s")
	_, _, err := Terminal{}.CreateTab(bg, "s", "/w", "proj-abc.1")
	if err == nil {
		t.Fatal("a window tmux refuses")
	}
	if got := f.ops(); !slices.Equal(got, []string{"new-window", "new-window"}) {
		t.Errorf("called %q, want new-window twice", got)
	}
}

// With the real tmux, a window's label is the label option CreateTab gives it where tmux refused the
// label as its name, and its name otherwise.
func TestLiveTabLabelReadsTheLabelOption(t *testing.T) {
	l := newLive(t)
	tab, _ := l.tab("x-1.2")
	if label, open, err := l.term.TabLabel(bg, tab); label != "x-1.2" || !open || err != nil {
		t.Errorf("TabLabel = %q %v %v, want x-1.2", label, open, err)
	}
	l.tmux("set-option", "-w", "-t", tab, "@orchestra_label", "x-1.3")
	if label, _, err := l.term.TabLabel(bg, tab); label != "x-1.3" || err != nil {
		t.Errorf("TabLabel = %q %v, want the label option's x-1.3", label, err)
	}
}
