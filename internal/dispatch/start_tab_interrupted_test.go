package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// tabCreateCut is Herdr's 'tab create' cut short by Ctrl+C: CreateTab presses it, then fails,
// saying what became of the tab Herdr opened.
type tabCreateCut struct{ cancel context.CancelFunc }

// tabCut is what tabCreateCut's CreateTab says became of the tab.
const tabCut = "tab w1:t10, which Herdr opened, is closed again"

func (c tabCreateCut) CreateTab(ctx context.Context, workspace, cwd, label string) (string, string, error) {
	c.cancel()
	return "", "", fmt.Errorf("herdr tab create: %w; %s", context.Canceled, tabCut)
}
func (tabCreateCut) CloseTab(ctx context.Context, tab string) error { return nil }
func (tabCreateCut) TabLabel(ctx context.Context, tab string) (string, bool, error) {
	return "", false, nil
}

// Ctrl+C while Herdr opens a worker's tab interrupts the run, and the log keeps what CreateTab said
// became of the tab.
func TestTabCreateCutByCtrlCIsLogged(t *testing.T) {
	noLeaks(t)
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 10, Concurrency: 1, WTRoot: "wts", LogPath: "log"},
		log, "", Deps{Tickets: readyTickets{{ID: "A"}}, Tabs: tabCreateCut{cancel: cancel},
			Agents: noAgents{}, Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}})
	o.SetSink(&recordSink{})
	if code := o.Run(ctx); code != ExitInterrupted {
		t.Errorf("exit code %d, want %d", code, ExitInterrupted)
	}
	if logged := read(t, logPath); !strings.Contains(logged, tabCut) {
		t.Errorf("log lacks %q:\n%s", tabCut, logged)
	}
}
