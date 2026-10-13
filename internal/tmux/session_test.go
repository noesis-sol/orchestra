package tmux

import (
	"os"
	"reflect"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

func TestSessionNameIsAsTmuxKeepsIt(t *testing.T) {
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
	f.fail("has-session", "can't find session: orchestra-app")
	if err := (Terminal{}).NewSession(bg, "orchestra-app", "/work/a#b"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"has-session", "-t", "=orchestra-app"},
		{"new-session", "-d", "-s", "orchestra-app", "-c", "/work/a##b"},
	}
	if got := f.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("called %q, want %q", got, want)
	}
}

func TestNewSessionKeepsASessionThatIsThere(t *testing.T) {
	f := newFakeTmux(t)
	if err := (Terminal{}).NewSession(bg, "orchestra-app", "/work"); err != nil {
		t.Fatal(err)
	}
	if got := f.ops(); !reflect.DeepEqual(got, []string{"has-session"}) {
		t.Errorf("called %q, want has-session alone", got)
	}
}

// Another orchestra made the session between the look and the start: it is there, so no error.
func TestNewSessionMadeMeanwhileIsNoError(t *testing.T) {
	newFakeTmux(t)
	// A tmux whose has-session finds the session from its second call on.
	d := t.TempDir()
	faketool.Write(t, d, "tmux", `d='`+d+`'
case "$1" in
has-session) [ -f "$d/looked" ] && exit 0; : > "$d/looked"; echo "can't find session" >&2; exit 1 ;;
new-session) echo 'duplicate session: orchestra-app' >&2; exit 1 ;;
esac
exit 0
`)
	t.Setenv("PATH", d+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := (Terminal{}).NewSession(bg, "orchestra-app", "/work"); err != nil {
		t.Fatal(err)
	}
}

func TestNewSessionThatTmuxCannotStartFails(t *testing.T) {
	f := newFakeTmux(t)
	f.fail("has-session", "can't find session: orchestra-app")
	f.fail("new-session", "create window failed")
	err := Terminal{}.NewSession(bg, "orchestra-app", "/work")
	if err == nil || Missing(err) {
		t.Errorf("got %v, want new-session's failure", err)
	}
}
