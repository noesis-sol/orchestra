package tmux

import (
	"reflect"
	"testing"
)

func TestSessionNameIsOneEveryTmuxKeeps(t *testing.T) {
	for name, want := range map[string]string{
		"orchestra-app":     "orchestra-app",
		"orchestra-my.site": "orchestra-my_site",
		"orchestra-a:b;":    "orchestra-a_b_",
	} {
		if got := SessionName(name); got != want {
			t.Errorf("SessionName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPaneSessionNamesTheSessionOfThePane(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("display-message", "work\n")
	name, err := Terminal{}.PaneSession(bg, "%3")
	if err != nil || name != "work" {
		t.Fatalf("got %q %v", name, err)
	}
	if want := []string{"display-message", "-p", "-t", "%3", "#{session_name}"}; !reflect.DeepEqual(f.call("display-message"), want) {
		t.Errorf("called %q, want %q", f.call("display-message"), want)
	}
}

// tmux prints nothing for a pane it doesn't have, and exits 0.
func TestPaneSessionOfAPaneTmuxDoesNotHaveIsMissing(t *testing.T) {
	newFakeTmux(t)
	if _, err := (Terminal{}).PaneSession(bg, "%3"); !Missing(err) {
		t.Errorf("an empty answer gave %v, want a missing pane", err)
	}
	if _, err := (Terminal{}).PaneSession(bg, ""); !Missing(err) {
		t.Errorf("no pane gave %v, want a missing pane", err)
	}
}

func TestPaneSessionFailsWithTmux(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("display-message", "no server running on /tmp/tmux-501/default")
	_, err := Terminal{}.PaneSession(bg, "%3")
	if !NoServer(err) {
		t.Errorf("got %v, want no server", err)
	}
}

func TestNewSessionStartsADetachedSession(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("list-sessions", "$0 orchestra-app-2\n")
	f.answer("new-session", "$1\n")
	id, err := Terminal{}.NewSession(bg, "orchestra-app", "/work/a#b")
	if err != nil || id != "$1" {
		t.Fatalf("NewSession = %q %v", id, err)
	}
	want := [][]string{
		{"list-sessions", "-F", "#{session_id} #{session_name}"},
		{"new-session", "-d", "-s", "orchestra-app", "-c", "/work/a##b", "-P", "-F", "#{session_id}"},
	}
	if got := f.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("called %q, want %q", got, want)
	}
}

func TestNewSessionKeepsASessionThatIsThere(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("list-sessions", "$0 orchestra\n$3 orchestra-app\n")
	if id, err := (Terminal{}).NewSession(bg, "orchestra-app", "/work"); err != nil || id != "$3" {
		t.Fatalf("NewSession = %q %v", id, err)
	}
	if got := f.ops(); !reflect.DeepEqual(got, []string{"list-sessions"}) {
		t.Errorf("called %q, want list-sessions alone", got)
	}
}

// Another orchestra made the session between the look and the start: it is there, so no error.
func TestNewSessionMadeMeanwhileIsNoError(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("new-session", "duplicate session: orchestra-app")
	f.after("list-sessions", `echo '$2 orchestra-app' > "$d/out-list-sessions"`)
	if id, err := (Terminal{}).NewSession(bg, "orchestra-app", "/work"); err != nil || id != "$2" {
		t.Fatalf("NewSession = %q %v", id, err)
	}
}

func TestNewSessionThatTmuxCannotStartFails(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("list-sessions", "no server running on /tmp/tmux-501/default")
	f.fail("new-session", "create window failed")
	_, err := Terminal{}.NewSession(bg, "orchestra-app", "/work")
	if err == nil || Missing(err) {
		t.Errorf("got %v, want new-session's failure", err)
	}
}

func TestNewSessionWhoseAnswerCannotBeReadFails(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("new-session", "garbled\n")
	if id, err := (Terminal{}).NewSession(bg, "orchestra-app", "/work"); err == nil {
		t.Errorf("NewSession = %q, want an error", id)
	}
}
