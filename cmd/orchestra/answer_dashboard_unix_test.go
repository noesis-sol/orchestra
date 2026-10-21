//go:build unix

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// dashboardTalks is the answer talks the dashboard of a run in repo opens, in a pane split off
// orchestra's (w1:p1) through panes, saying what they would print in the log at logPath; the messages
// they send the dashboard are in *sent.
func dashboardTalks(t *testing.T, repo, logPath string, panes *fakePanes, sent *[]tea.Msg) (*answerTalks,
	*dispatch.Log) {
	t.Helper()
	log, err := dispatch.OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	c := options{Config: dispatch.Config{Repo: repo}}
	c.where.pane = "w1:p1"
	talk := dashboardAnswerTalk(c, log)
	talk.panes, talk.claude = &talkPanes{fakePanes: panes}, claudeSession{repo: repo, tools: answerToolArgs()}
	return newAnswerTalks(runCtx(t), talk, "", log, func(msg tea.Msg) { *sent = append(*sent, msg) }), log
}

// The dashboard's key opens the answer talk in a pane beside orchestra's, in the background, while the
// dashboard goes on: claude answers through bd, which closes the question, the pane closes, and the
// dashboard is told which questions were answered. The log says so; nothing goes to orchestra's
// terminal, which the dashboard holds.
func TestDashboardOpensTheAnswerTalkInAPane(t *testing.T) {
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
		talks.start(true)
		talks.wg.Wait()
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		if len(sent) != 1 {
			t.Fatalf("the dashboard was sent %v, want the talk's end", sent)
		}
		if over, ok := sent[0].(tui.AnswerOver); !ok || !slices.Equal(over.Answered, []string{"k-7"}) || over.Err != nil {
			t.Errorf("the dashboard was sent %+v", sent[0])
		}
		if want := []string{"split w1:p1 " + repo, "launch w1:p2 claude", "close w1:p2"}; !slices.Equal(panes.calls, want) {
			t.Errorf("the terminal was asked to %q, want %q", panes.calls, want)
		}
		if talks.open.Load() != nil {
			t.Error("the talk is still counted open")
		}
	})
	if q := readIssues(t, state)["k-7"]; q.Status != "closed" {
		t.Errorf("the question: %+v", q)
	}
	logged := read(t, logPath)
	for _, want := range []string{"Answering 1 question with claude in the pane on the right",
		"the answer talk answered k-7\n"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the log lacks %q:\n%s", want, logged)
		}
	}
}

// Where no pane can be opened beside orchestra's, the talk doesn't take the dashboard's terminal: the
// dashboard is told to have orchestra answer run in another, and the log says why the pane failed.
func TestDashboardAnswerTalkWithoutAPane(t *testing.T) {
	t.Parallel()
	repo, _, state := answerSetup(t, answerBacklog())
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	synctest.Test(t, func(t *testing.T) {
		var sent []tea.Msg
		talks, log := dashboardTalks(t, repo, logPath, &fakePanes{splitErr: errors.New("herdr: no such pane")}, &sent)
		talks.start(true)
		talks.wg.Wait()
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		if len(sent) != 1 {
			t.Fatalf("the dashboard was sent %v, want the talk's end", sent)
		}
		if over, ok := sent[0].(tui.AnswerOver); !ok || over.Answered != nil || !errors.Is(over.Err, errAnswerNoPane) {
			t.Errorf("the dashboard was sent %+v", sent[0])
		}
	})
	if q := readIssues(t, state)["k-7"]; q.Status != "open" {
		t.Errorf("the question: %+v", q)
	}
	if logged := read(t, logPath); !strings.Contains(logged, "orchestra couldn't open the answer talk in a pane "+
		"beside its own, so claude takes this terminal: herdr: no such pane") {
		t.Errorf("the log:\n%s", logged)
	}
}

// With every question answered meanwhile some other way, the talk asks nothing, and the dashboard is
// told why it didn't open.
func TestDashboardAnswerTalkWithNothingOpen(t *testing.T) {
	t.Parallel()
	backlog := answerBacklog()
	backlog["k-7"].Status = "closed"
	repo, _, _ := answerSetup(t, backlog)
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	synctest.Test(t, func(t *testing.T) {
		var sent []tea.Msg
		panes := &fakePanes{}
		talks, log := dashboardTalks(t, repo, logPath, panes, &sent)
		talks.start(true)
		talks.wg.Wait()
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		if over, ok := sent[0].(tui.AnswerOver); !ok || !errors.Is(over.Err, errNothingToAnswer) || panes.calls != nil {
			t.Errorf("the dashboard was sent %+v; the terminal was asked to %q", sent[0], panes.calls)
		}
	})
}

// The log's line on how the talk left its questions.
func TestAnsweredLine(t *testing.T) {
	for _, tc := range []struct {
		r    answerResult
		want string
	}{
		{answerResult{Answered: []string{"k-7", "k-9"}}, "the answer talk answered k-7, k-9"},
		{answerResult{Answered: []string{"k-7"}, Open: []string{"k-9"}}, "the answer talk answered k-7; still open: k-9"},
		{answerResult{Open: []string{"k-9"}}, "the answer talk answered no question; still open: k-9"},
	} {
		if got := answeredLine(tc.r); got != tc.want {
			t.Errorf("answeredLine(%+v) = %q, want %q", tc.r, got, tc.want)
		}
	}
}
