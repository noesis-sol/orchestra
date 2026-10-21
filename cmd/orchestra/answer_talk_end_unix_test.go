//go:build unix

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"

	tea "github.com/charmbracelet/bubbletea"
)

// A run that ends with the answer talk open waits for it, and then says on orchestra's terminal how it
// ended, below the summary and the report that still have the ticket waiting: the questions answered,
// whose tickets come back to the next run, and those still open, or that a stop signal closed it.
func TestRunEndSaysHowTheAnswerTalkEnded(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		agent  []dispatch.AgentState
		answer bool // the maintainer answers k-7 as claude reads the questions
		stop   bool // a stop signal comes as claude reads them
		want   string
	}{
		{"answered", []dispatch.AgentState{dispatch.StateWorking, dispatch.StateWorking, dispatch.StateIdle}, true,
			false, "Answered k-7: the tickets waiting on it come back to the next run.\n"},
		{"left open", []dispatch.AgentState{dispatch.StateWorking, dispatch.StateGone}, false, false,
			"Still open: k-7. orchestra answer asks it again.\n"},
		{"stopped", []dispatch.AgentState{dispatch.StateWorking}, false, true,
			"The answer talk was closed.\nStill open: k-7. orchestra answer asks it again.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			repo, bd, _ := answerSetup(t, answerBacklog())
			logPath := filepath.Join(t.TempDir(), "orchestra.log")
			stops := catchStops()
			defer stops.release()
			var out strings.Builder
			synctest.Test(t, func(t *testing.T) {
				panes := &fakePanes{agent: tc.agent, step: func(read int) {
					if read == 1 && tc.stop {
						stops.deliver(syscall.SIGTERM)
					}
					if read != 2 || !tc.answer {
						return
					}
					if out, err := exec.Command(bd, "human", "respond", "k-7", "--response", "JSON").CombinedOutput(); err != nil {
						t.Errorf("bd human respond: %v\n%s", err, out)
					}
				}}
				var sent []tea.Msg
				talks, log := dashboardTalks(t, repo, logPath, panes, &sent)
				talks.start(false)
				talks.dashboardClosed()
				talks.finish(stops, false, &out)
				if err := log.Close(); err != nil {
					t.Fatal(err)
				}
			})
			want := "The answer talk is still open in the pane on the right: orchestra ends once it is over " +
				"(Ctrl+C closes it).\n" + tc.want
			if out.String() != want {
				t.Errorf("orchestra's terminal reads\n%s\nwant\n%s", out.String(), want)
			}
		})
	}
}

// A talk the run's stop closed already is only waited for: nothing more is said of it.
func TestRunEndSaysNothingOfATalkItsStopClosed(t *testing.T) {
	t.Parallel()
	repo, _, _ := answerSetup(t, answerBacklog())
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	stops := catchStops()
	defer stops.release()
	var out strings.Builder
	synctest.Test(t, func(t *testing.T) {
		var sent []tea.Msg
		talks, log := dashboardTalks(t, repo, logPath, &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking}},
			&sent)
		talks.start(false)
		talks.dashboardClosed()
		talks.cancel()
		talks.finish(stops, false, &out)
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
	})
	if out.Len() != 0 {
		t.Errorf("orchestra's terminal reads\n%s", out.String())
	}
}
