package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/noesis-sol/orchestra/internal/claudetest"
	"github.com/noesis-sol/orchestra/internal/cmux"
	"github.com/noesis-sol/orchestra/internal/cmuxtest"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The tests in this file run the real cmux, an app of the test's own (cmuxtest.Start), whose first
// workspace and terminal stand for orchestra's own, where it would run: what orchestra does in cmux
// beyond a run's workers, the interview and the dashboard's keys.

var _ = faketool.Func("claude", claudetest.Claude)

// The interview opens claude in a terminal split to the right of orchestra's, with its hooks; once
// claude has handed the epic back and its turn has ended, the split closes, and orchestra's terminal
// has the focus back.
func TestCmuxInterviewInASplit(t *testing.T) {
	// Not parallel: cmuxtest sets the process's PATH.
	app := cmuxtest.Start(t)
	repo, _ := gitRepo(t)
	bin, claudeDir := t.TempDir(), t.TempDir()
	faketool.WriteFunc(t, bin, "claude", claudetest.Env(claudeDir))
	onPath(t, bin)
	script := []string{"start", "sleep 2s", "write " + project.RunPath(project.FeatureName) + ` {"epic":"f-1"}`, "stop",
		"idle"}
	if err := os.WriteFile(filepath.Join(claudeDir, "script"), []byte(strings.Join(script, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	term := cmux.Terminal{Book: filepath.Join(repo, project.RunPath(cmux.BookName)), Own: app.Own}
	var out, errOut strings.Builder
	var split []string // orchestra's workspace's terminals, while claude talks the feature through
	b := refineBreakdown{repo: repo, pane: app.OwnSurface, hooks: true, epics: filedEpic{}, out: &out, err: &errOut,
		panes: watchedPanes{term, func() { split = app.Tree().Surfaces[app.Own] }},
		terminal: func(context.Context, string, project.RefineMode) error {
			t.Error("the interview fell back to orchestra's terminal")
			return nil
		}}
	epic, code := featureTalk{request: paneRequest, repo: repo, breakdown: b, tracker: filedEpic{}, out: &out,
		err: &errOut}.run(t.Context())
	logged, _ := os.ReadFile(filepath.Join(claudeDir, "log")) // none reads as nothing
	if epic != "f-1" || code != dispatch.ExitOK || errOut.Len() > 0 {
		t.Fatalf("the interview filed %q, exit %d; stderr:\n%s\nclaude's log:\n%s", epic, code, errOut.String(), logged)
	}
	if len(split) != 2 || split[0] != app.OwnSurface {
		t.Errorf("while claude ran, orchestra's workspace had terminals %v, want orchestra's, %s, and the split to "+
			"its right", split, app.OwnSurface)
	}
	if !strings.Contains(string(logged), " startup\n") {
		t.Errorf("claude never started in the split:\n%s", logged)
	}
	tree := app.Tree()
	if want := []string{app.OwnSurface}; !slices.Equal(tree.Surfaces[app.Own], want) {
		t.Errorf("orchestra's workspace has terminals %v once the feature is filed, want %v", tree.Surfaces[app.Own], want)
	}
	if tree.Focused != app.OwnSurface {
		t.Errorf("the focus is on %s, want orchestra's terminal, %s", tree.Focused, app.OwnSurface)
	}
}

// watchedPanes is the interview's terminal, with looked called once claude has started in the split.
type watchedPanes struct {
	interviewPanes
	looked func()
}

func (w watchedPanes) PaneAgent(ctx context.Context, pane string) (string, string, dispatch.AgentState, error) {
	name, kind, st, err := w.interviewPanes.PaneAgent(ctx, pane)
	if err == nil && kind == "claude" && st != dispatch.StateGone {
		w.looked()
	}
	return name, kind, st, err
}

// On the dashboard, a worker's number selects its workspace in cmux.
func TestCmuxDashboardNumbersSelectWorkspaces(t *testing.T) {
	// Not parallel: cmuxtest sets the process's PATH.
	app := cmuxtest.Start(t)
	repo := t.TempDir()
	term := cmux.Terminal{Book: filepath.Join(repo, project.RunPath(cmux.BookName)), Own: app.Own}
	log, err := dispatch.OpenLog(filepath.Join(repo, "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }() // nothing is read back from it
	keys, typed := io.Pipe()
	p := tea.NewProgram(tui.NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, focusTab(t.Context(), log, term)),
		tea.WithInput(keys), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	sink := tui.NewProgramSink(p)
	tabs := map[string]string{}
	began := time.Now()
	for i, id := range []string{"tx-1", "tx-2"} {
		tab, _, err := term.CreateTab(t.Context(), "", repo, id)
		if err != nil {
			t.Fatal(err)
		}
		tabs[id] = tab
		sink.Status(dispatch.Status{Ticket: id, Tab: tab, Agent: dispatch.StateWorking,
			Started: began.Add(time.Duration(i) * time.Second)})
	}
	for _, c := range []struct{ key, ticket string }{{"2", "tx-2"}, {"1", "tx-1"}} {
		if _, err := typed.Write([]byte(c.key)); err != nil {
			t.Fatal(err)
		}
		for deadline := time.Now().Add(10 * time.Second); app.Tree().Selected != tabs[c.ticket]; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("key %s shows workspace %s, want %s's, %s", c.key, app.Tree().Selected, c.ticket, tabs[c.ticket])
			}
		}
	}
	p.Quit()
	_ = typed.Close() // the dashboard has quit, and reads no more
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
