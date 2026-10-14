package tmux

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// A session whose name holds '.', as orchestra's own may, is found by its exact name, which tmux
// can't take as a target ("my.sess" is window "my", pane "sess"): the windows open in it by its ID.
func TestNewSessionFindsASessionNamedWithADot(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("list-sessions", "$0 my\n$4 my.sess\n$5 my.sess.old\n")
	id, err := Terminal{}.NewSession(bg, "my.sess", "/work")
	if err != nil || id != "$4" {
		t.Fatalf("NewSession = %q %v", id, err)
	}
	if got := f.ops(); !reflect.DeepEqual(got, []string{"list-sessions"}) {
		t.Errorf("called %q, want list-sessions alone", got)
	}
	f.answer("new-window", "@4 %9\n")
	if _, _, err := (Terminal{}).CreateTab(bg, id, "/work", "x-1"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("new-window"); !reflect.DeepEqual(got[1:4], []string{"-d", "-t", "$4:"}) {
		t.Errorf("new-window %q, want it in $4", got)
	}
}

// A session named with '#', as one named after a repository folder holding it may be, is started
// under that name: tmux expands -s as a format, so the '#' is escaped.
func TestNewSessionNamedWithAHash(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("new-session", "$2\n")
	if id, err := (Terminal{}).NewSession(bg, "orchestra-a#{b}", "/work"); err != nil || id != "$2" {
		t.Fatalf("NewSession = %q %v", id, err)
	}
	if got := f.call("new-session"); !reflect.DeepEqual(got[1:4], []string{"-d", "-s", "orchestra-a##{b}"}) {
		t.Errorf("new-session %q", got)
	}
}

// tmux 3.2 to 3.6 name a session asked for as foo.bar foo_bar: the next window opens in that one,
// rather than failing on new-session's duplicate.
func TestNewSessionFindsTheSessionOlderTmuxRenamed(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("list-sessions", "$0 t\n$7 foo_bar\n")
	f.fail("new-session", "duplicate session: foo_bar")
	if id, err := (Terminal{}).NewSession(bg, "foo.bar", "/work"); err != nil || id != "$7" {
		t.Fatalf("NewSession = %q %v", id, err)
	}
	for _, c := range f.calls() {
		if c[0] == "new-session" && c[3] != "foo.bar" {
			t.Errorf("started a session named %q", c[3])
		}
	}
}

// tmux 3.0, 3.1 and 3.7 refuse a session name holding '.': the session is the one SessionName names.
func TestNewSessionNamesASessionAsSessionNameWhereTmuxRefusesTheName(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("new-session", "invalid session name: foo.bar")
	f.answer("new-session", "$3\n")
	// tmux refuses the first new-session, for foo.bar, alone: it was looked for twice by then.
	f.after("list-sessions", `echo >> "$d/looks"; [ "$(wc -l < "$d/looks")" -ge 2 ] && rm -f "$d/fail-new-session"`)
	id, err := Terminal{}.NewSession(bg, "foo.bar", "/work")
	if err != nil || id != "$3" {
		t.Fatalf("NewSession = %q %v", id, err)
	}
	var names []string
	for _, c := range f.calls() {
		if c[0] == "new-session" {
			names = append(names, c[3])
		}
	}
	if strings.Join(names, " ") != "foo.bar foo_bar" {
		t.Errorf("started sessions named %q, want foo.bar, then foo_bar", names)
	}
}

// With the real tmux, worker windows open in a session named with '.', '#' or ':', whether orchestra
// starts it (--workspace, or the repository folder's name) or runs in it, whatever this tmux does with
// such a name (see SessionName). Each name gives one session, however often it is asked for.
func TestLiveSessionsNamedWithDotsAndHashes(t *testing.T) {
	l := newLive(t)
	for i, name := range []string{"my.sess", "a:b", "orchestra-work #1", "orchestra-#{pane_id}", "p##q",
		SessionName("orchestra-my.repo #2")} {
		id, err := l.term.NewSession(bg, name, l.dir)
		if err != nil {
			t.Fatalf("NewSession(%q): %v", name, err)
		}
		if again, err := l.term.NewSession(bg, name, l.dir); again != id || err != nil {
			t.Errorf("NewSession(%q) again = %q %v, want %s", name, again, err, id)
		}
		tab, pane, err := l.term.CreateTab(bg, id, l.dir, "x-"+strconv.Itoa(i))
		if err != nil {
			t.Fatalf("a window in session %q: %v", name, err)
		}
		if got := l.tmux("display-message", "-p", "-t", tab, "#{session_id}"); got != id {
			t.Errorf("session %q's window %s is in %s, want %s", name, tab, got, id)
		}

		// orchestra run in that session: its name, as tmux has it, is the session.
		own, err := l.term.PaneSession(bg, pane)
		if err != nil {
			t.Fatal(err)
		}
		if own != name && own != SessionName(name) {
			t.Errorf("session %q is named %q", name, own)
		}
		if got, err := l.term.NewSession(bg, own, l.dir); got != id || err != nil {
			t.Errorf("NewSession(%q) in it = %q %v, want %s", own, got, err, id)
		}
		if !strings.Contains(own, ":") {
			if tab, _, err := l.term.CreateTab(bg, own, l.dir, "y-"+strconv.Itoa(i)); err != nil {
				t.Errorf("a window in session %q by its name: %v", own, err)
			} else if got := l.tmux("display-message", "-p", "-t", tab, "#{session_id}"); got != id {
				t.Errorf("session %q's window %s by its name is in %s, want %s", own, tab, got, id)
			}
		}
	}
	if n := len(strings.Split(l.tmux("list-sessions", "-F", "#{session_id}"), "\n")); n != 7 {
		t.Errorf("tmux has %d sessions, want 7:\n%s", n, l.tmux("list-sessions", "-F", "#{session_name}"))
	}
}
