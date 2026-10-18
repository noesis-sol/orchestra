package tmux

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The interfaces the run loop reaches the terminal through, and the interview's panes.
var (
	_ dispatch.Tabs    = Terminal{}
	_ dispatch.Starter = Terminal{}
	_ dispatch.Namer   = Terminal{}
	_ dispatch.Agents  = Terminal{}
	_ interface {
		FocusTab(ctx context.Context, tab string) error
		SplitPane(ctx context.Context, pane, cwd string) (string, error)
		LaunchInPane(ctx context.Context, pane, kind string, args []string) error
		PaneAgent(ctx context.Context, pane string) (name, kind string, state dispatch.AgentState, err error)
		PaneOpen(ctx context.Context, pane string) (bool, error)
		ClosePane(ctx context.Context, pane string) error
	} = Terminal{}
)

var bg = context.Background()

func TestSocketNamesTheServer(t *testing.T) {
	f := newFakeTmux(t)
	if err := (Terminal{Socket: "orch-test"}).FocusTab(bg, "@1"); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); len(got) != 1 || !slices.Equal(got[0], []string{"-L", "orch-test", "select-window", "-t", "@1"}) {
		t.Errorf("tmux got %q", got)
	}
	uid := strconv.Itoa(os.Getuid())
	if got, want := (Terminal{Socket: "orch-test"}).socketPath(bg), filepath.Join(os.Getenv("TMUX_TMPDIR"), "tmux-"+uid, "orch-test"); got != want {
		t.Errorf("socket %q, want %q", got, want)
	}
	t.Setenv("TMUX", "/private/tmp/tmux-501/work,1234,0")
	if got := (Terminal{}).socketPath(bg); got != "/private/tmp/tmux-501/work" {
		t.Errorf("inside tmux, its server: %q", got)
	}
	if got := (Terminal{Socket: "x"}).socketPath(bg); !strings.HasSuffix(got, "/x") {
		t.Errorf("a socket named overrides $TMUX: %q", got)
	}
}

func TestErrorText(t *testing.T) {
	err := missing("rename", "x-1", "agent")
	if err.Error() != "tmux rename x-1: no such agent" {
		t.Errorf("got %q", err)
	}
	if got := (&Error{Tool: tool, Op: "list-panes", Err: errors.New("boom")}).Error(); got != "tmux list-panes: boom" {
		t.Errorf("got %q", got)
	}
}
