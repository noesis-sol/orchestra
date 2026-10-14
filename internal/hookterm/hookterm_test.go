package hookterm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// TestMain fails the tests if a goroutine one of them started is still running once they have all
// returned.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

func TestErrorText(t *testing.T) {
	cmdErr := &command.Error{Name: "tmux", Args: []string{"kill-pane", "-t", "%5"}, Err: errors.New("exit status 1")}
	for _, c := range []struct {
		err  error
		want string
	}{
		{&Error{Tool: "tmux", Op: "list-panes", Err: errors.New("boom")}, "tmux list-panes: boom"},
		{&Error{Tool: "cmux", Op: "send", Target: "x-1", Err: errors.New("boom")}, "cmux send x-1: boom"},
		{&Error{Tool: "tmux", Op: "kill-pane", Target: "%5", Err: cmdErr}, cmdErr.Error()},
		{NoSuch("tmux", "rename", "x-1", "agent"), "tmux rename x-1: no such agent"},
		{Odd("cmux", "tree", "", " what?\n"), `cmux tree: unexpected output: "what?"`},
	} {
		if got := c.err.Error(); got != c.want {
			t.Errorf("%#v: got %q, want %q", c.err, got, c.want)
		}
	}
}

func TestNoServerAndMissing(t *testing.T) {
	for _, c := range []struct {
		err               error
		noServer, missing bool
	}{
		{nil, false, false},
		{errors.New("boom"), false, false},
		{&Error{Tool: "tmux", Op: "list-panes", NoServer: true, Err: errors.New("boom")}, true, false},
		{fmt.Errorf("wrapped: %w", &Error{Tool: "tmux", NoServer: true}), true, false},
		{NoSuch("cmux", "adopt", "x", "surface"), false, true},
		{fmt.Errorf("wrapped: %w", NoSuch("cmux", "adopt", "x", "surface")), false, true},
		{Odd("cmux", "tree", "", "?"), false, false},
	} {
		if NoServer(c.err) != c.noServer || Missing(c.err) != c.missing {
			t.Errorf("%v: NoServer %t, Missing %t; want %t, %t", c.err, NoServer(c.err), Missing(c.err),
				c.noServer, c.missing)
		}
	}
}

func TestFreeName(t *testing.T) {
	upTo := func(n int) func(string) bool {
		return func(name string) bool {
			for i := 1; i <= n; i++ {
				if name == fmt.Sprintf("x-1-%d", i) {
					return true
				}
			}
			return false
		}
	}
	for _, c := range []struct {
		taken int
		want  string
	}{{0, "x-1-1"}, {1, "x-1-2"}, {19, "x-1-20"}, {20, ""}} {
		if got := FreeName("x-1", upTo(c.taken)); got != c.want {
			t.Errorf("%d taken: got %q, want %q", c.taken, got, c.want)
		}
	}
}

func TestParseKey(t *testing.T) {
	for _, c := range []struct {
		k    string
		want Key
		ok   bool
	}{
		{"enter", Key{Name: "enter"}, true},
		{"Return", Key{Name: "enter"}, true},
		{"ESC", Key{Name: "esc"}, true},
		{"escape", Key{Name: "esc"}, true},
		{"shift+tab", Key{Name: "shift+tab"}, true},
		{"PageDown", Key{Name: "pagedown"}, true},
		{"ctrl+c", Key{Name: "c", Ctrl: true}, true},
		{"Ctrl+C", Key{Name: "c", Ctrl: true}, true},
		{"alt+x", Key{Name: "x", Alt: true}, true},
		{"ctrl+é", Key{Name: "é", Ctrl: true}, true},
		{"ctrl+tab", Key{}, false},
		{"ctrl+", Key{}, false},
		{"alt+xy", Key{}, false},
		{"hello", Key{}, false},
		{"", Key{}, false},
	} {
		if got, ok := ParseKey(c.k); got != c.want || ok != c.ok {
			t.Errorf("ParseKey(%q) = %+v, %t; want %+v, %t", c.k, got, ok, c.want, c.ok)
		}
	}
	for _, k := range Keys {
		if got, ok := ParseKey(k); !ok || got != (Key{Name: k}) {
			t.Errorf("ParseKey(%q) = %+v, %t", k, got, ok)
		}
	}
}

func TestKeyNamesName(t *testing.T) {
	n := KeyNames{Ctrl: "C-", Alt: "M-", Names: map[string]string{"enter": "Enter", "esc": "Escape"}}
	for _, c := range []struct {
		k, want string
		ok      bool
	}{
		{"enter", "Enter", true},
		{"RETURN", "Enter", true},
		{"escape", "Escape", true},
		{"ctrl+c", "C-c", true},
		{"alt+X", "M-x", true},
		{"hello", "", false},
	} {
		if got, ok := n.Name(c.k); got != c.want || ok != c.ok {
			t.Errorf("Name(%q) = %q, %t; want %q, %t", c.k, got, ok, c.want, c.ok)
		}
	}
}

// statuses is a terminal's Status that gives the states in turn, its last one from then on.
func statuses(sts ...dispatch.AgentState) func(context.Context, string) (dispatch.AgentState, error) {
	n := 0
	return func(context.Context, string) (dispatch.AgentState, error) {
		st := sts[min(n, len(sts)-1)]
		n++
		return st, nil
	}
}

func TestWait(t *testing.T) {
	boom := errors.New("boom")
	failing := func(context.Context, string) (dispatch.AgentState, error) { return "", boom }
	for _, c := range []struct {
		name    string
		status  func(context.Context, string) (dispatch.AgentState, error)
		want    string // the error's text, "" for none
		missing bool
	}{
		{"there at once", statuses(dispatch.StateIdle), "", false},
		{"there in time", statuses(dispatch.StateUnknown, dispatch.StateWorking, dispatch.StateDone), "", false},
		{"gone", statuses(dispatch.StateWorking, dispatch.StateGone), "tmux start x-1: the agent is gone", true},
		{"not in time", statuses(dispatch.StateWorking),
			"tmux start x-1: the agent was not idle or done within 1m, but working", false},
		{"not readable", failing, "agent x-1 was not idle or done within 1m: boom", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				a := Agents{Tool: "tmux", Status: c.status}
				err := a.Wait(t.Context(), "start", "x-1", time.Minute, dispatch.StateIdle, dispatch.StateDone)
				switch {
				case c.want == "" && err != nil:
					t.Errorf("got %v", err)
				case c.want != "" && (err == nil || err.Error() != c.want):
					t.Errorf("got %v, want %s", err, c.want)
				case Missing(err) != c.missing:
					t.Errorf("Missing: %t", Missing(err))
				}
			})
		})
	}
}

func TestWaitStopsWithItsContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 3*Poll)
		defer cancel()
		a := Agents{Tool: "cmux", Status: statuses(dispatch.StateIdle)}
		if a.WaitStarted(ctx, "x-1") {
			t.Error("an idle agent hasn't started")
		}
		if ctx.Err() == nil {
			t.Error("it should have waited until stopped")
		}
	})
}

func TestWaitReadyAndStarted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := Agents{Tool: "tmux", Status: statuses(dispatch.StateDone)}
		if !a.WaitReady(t.Context(), "x-1") || a.WaitStarted(t.Context(), "x-1") {
			t.Error("a done agent is ready and hasn't started")
		}
		a.Status = statuses(dispatch.StateBlocked)
		if a.WaitReady(t.Context(), "x-1") || !a.WaitStarted(t.Context(), "x-1") {
			t.Error("a blocked agent has started and isn't ready")
		}
	})
}

func TestStartAgent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("boom")
		a := Agents{Tool: "tmux", Status: statuses(dispatch.StateIdle)}
		if err := a.StartAgent(t.Context(), "x-1", func() error { return boom }); !errors.Is(err, boom) {
			t.Errorf("a launch that fails: %v", err)
		}
		if err := a.StartAgent(t.Context(), "x-1", func() error { return nil }); err != nil {
			t.Errorf("an agent idle once launched: %v", err)
		}
		a.Status = statuses(dispatch.StateGone)
		if err := a.StartAgent(t.Context(), "x-1", func() error { return nil }); !Missing(err) {
			t.Errorf("an agent gone once launched: %v", err)
		}
	})
}

func TestPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var did []string
		start := time.Now()
		var settled time.Duration
		put := func() error { did = append(did, "put"); return nil }
		enter := func() error { did = append(did, "enter"); settled = time.Since(start); return nil }
		a := Agents{Tool: "cmux", Status: statuses(dispatch.StateIdle, dispatch.StateWorking)}
		if err := a.Prompt(t.Context(), "x-1", put, enter); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(did, []string{"put", "enter"}) || settled != PasteSettle {
			t.Errorf("did %v, pressing Enter after %s", did, settled)
		}

		boom := errors.New("boom")
		did = nil
		if err := a.Prompt(t.Context(), "x-1", func() error { return boom }, enter); !errors.Is(err, boom) ||
			len(did) != 0 {
			t.Errorf("a prompt that can't be put: %v, did %v", err, did)
		}
		a.Status = statuses(dispatch.StateIdle)
		err := a.Prompt(t.Context(), "x-1", put, enter)
		if want := "cmux prompt x-1: the agent was not working or blocked within 30s, but idle"; err == nil ||
			err.Error() != want {
			t.Errorf("an agent that doesn't start: %v", err)
		}
	})
}

func TestNames(t *testing.T) {
	var n Names
	if n.AgentName("ab-1") != "ab-1" {
		t.Error("a hook-driven terminal names an agent by its ticket")
	}
}
