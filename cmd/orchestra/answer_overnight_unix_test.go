//go:build unix

package main

import (
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// An overnight run with "questions": "auto" doesn't open the answer talk by itself when its worker
// asks, as nobody is there to answer it: the run ends without waiting for a pane to close, and the
// question stays open for orchestra answer in the morning. A run that isn't overnight opens it.
func TestOvernightRunOpensNoAnswerTalkByItself(t *testing.T) {
	t.Parallel()
	backlog := answerBacklog()
	c := options{Config: dispatch.Config{Limit: 1, Overnight: true}, answerAuto: true}
	c.where.pane = "w1:p1"
	logged, out, panes := askedOnDashboard(t, c, backlog)
	if strings.Contains(logged, "the answer talk opens") {
		t.Errorf("the overnight run opened the talk:\n%s", logged)
	}
	if out != "" {
		t.Errorf("the run's end said %q, want it not to wait", out)
	}
	if panes.calls != nil {
		t.Errorf("the terminal was asked to %q", panes.calls)
	}

	// The question closed meanwhile, so that the talk opened ends at once, opening no pane.
	backlog = answerBacklog()
	backlog["k-7"].Status = "closed"
	c.Overnight = false
	if logged, _, _ := askedOnDashboard(t, c, backlog); !strings.Contains(logged,
		"the answer talk opens by itself, unfocused\n") {
		t.Errorf("a run not overnight didn't open the talk by itself:\n%s", logged)
	}
}

// askedOnDashboard runs the dashboard of the run c, its answer talks as runDashboard gives them, on
// backlog, through a worker asking k-7 and the loop's end: it returns what the log says, what finish
// says at the run's end, and the terminal the talk would open its pane in.
func askedOnDashboard(t *testing.T, c options, backlog map[string]*answerIssue) (logged, out string,
	panes *fakePanes) {
	t.Helper()
	repo, _, _ := answerSetup(t, backlog)
	c.Repo = repo
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := dispatch.OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	panes = &fakePanes{}
	talk := dashboardAnswerTalk(c, log)
	talk.panes, talk.claude = &talkPanes{fakePanes: panes}, claudeSession{repo: repo, tools: answerToolArgs()}
	var p *tea.Program
	talks := newAnswerTalks(runCtx(t), talk, "", log, func(msg tea.Msg) { p.Send(msg) })
	d := tui.NewDashboard(c.Config, func() {}, func(bool) {}, func(string) {}).
		WithAnswer(talks.callbacks(), talksByItself(c))
	p = tea.NewProgram(d, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()
	sink := tui.NewProgramSink(p)
	sink.Event(dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Limit: 1, Ticket: "k-1", Title: "Encode the output"})
	sink.Event(dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Title: "Encode the output",
		Detail: "k-7: Which encoding?"})
	p.Send(tui.Finished{})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	talks.dashboardClosed()
	var said strings.Builder
	talks.finish(&stopWatch{}, false, &said)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return read(t, logPath), said.String(), panes
}
