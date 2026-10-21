//go:build unix

package main

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// The dashboard's a key pressed as the run ends opens the answer talk, whose end the dashboard, closed
// on the loop's Finished, never sees: the log says the talk opened, and why it opened no pane, here
// that bd had the question answered meanwhile.
func TestAnswerKeyAsTheRunEndsIsLogged(t *testing.T) {
	t.Parallel()
	backlog := answerBacklog()
	backlog["k-7"].Status = "closed"
	repo, _, _ := answerSetup(t, backlog)
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := dispatch.OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	c := options{Config: dispatch.Config{Repo: repo, Limit: 1}}
	c.where.pane = "w1:p1"
	panes := &fakePanes{}
	talk := dashboardAnswerTalk(c, log)
	talk.panes, talk.claude = &talkPanes{fakePanes: panes}, claudeSession{repo: repo, tools: answerToolArgs()}
	var p *tea.Program
	talks := newAnswerTalks(runCtx(t), talk, "", log, func(msg tea.Msg) { p.Send(msg) })
	d := tui.NewDashboard(c.Config, func() {}, func(bool) {}, func(string) {}).WithAnswer(talks.callbacks(), false)
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
	p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	p.Send(tui.Finished{})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	talks.dashboardClosed()
	var out strings.Builder
	talks.finish(&stopWatch{}, false, &out)
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if panes.calls != nil {
		t.Errorf("the terminal was asked to %q", panes.calls)
	}
	logged := read(t, logPath)
	for _, want := range []string{"the answer talk opens on the a key\n",
		"the answer talk: bd has no question open in the run's scope"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
}

// A talk the a key opened just before the loop's Finished goes on once the dashboard has closed
// (the dashboard's side is internal/tui's TestAnswerKeyThenFinished): orchestra says it waits for it,
// waits, and the log says how it left its question.
func TestAnswerTalkOpenAsTheRunEndsIsWaitedFor(t *testing.T) {
	t.Parallel()
	repo, bd, state := answerSetup(t, answerBacklog())
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{
			agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateWorking, dispatch.StateIdle},
			step: func(read int) {
				if read != 2 {
					return
				}
				if out, err := exec.Command(bd, "human", "respond", "k-7", "--response", "JSON").CombinedOutput(); err != nil {
					t.Errorf("bd human respond: %v\n%s", err, out)
				}
			},
		}
		var sent []tea.Msg
		talks, log := dashboardTalks(t, repo, logPath, panes, &sent)
		talks.start(true) // the a key, the dashboard closing on Finished just after
		talks.dashboardClosed()
		var out strings.Builder
		talks.finish(&stopWatch{}, false, &out)
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "The answer talk is still open in the pane on the right") {
			t.Errorf("orchestra said %q", out.String())
		}
		if talks.open.Load() != nil {
			t.Error("the talk is still counted open")
		}
	})
	if q := readIssues(t, state)["k-7"]; q.Status != "closed" {
		t.Errorf("the question: %+v", q)
	}
	logged := read(t, logPath)
	for _, want := range []string{"the answer talk opens on the a key\n", "the answer talk answered k-7\n"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
}
