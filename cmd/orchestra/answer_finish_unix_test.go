//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// signalledReview is the organs with a run report during which the stop signal sig comes, once the
// answer talk is under way (asked).
type signalledReview struct {
	fakeOrgans
	stops *stopWatch
	sig   os.Signal
	asked <-chan struct{}
}

func (o signalledReview) Review(ctx context.Context, _ int, _ string) (string, error) {
	<-o.asked
	o.stops.deliver(o.sig)
	<-ctx.Done()
	return "", ctx.Err()
}

// A stop signal during the organ phase skips it, and closes the answer talk still open at the run's end
// rather than wait for it: orchestra says it was closed, not that it waits.
func TestStopInTheOrganPhaseClosesTheAnswerTalk(t *testing.T) {
	t.Parallel()
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signalName(sig), func(t *testing.T) {
			t.Parallel()
			repo, _, _ := answerSetup(t, answerBacklog())
			logPath := filepath.Join(t.TempDir(), "orchestra.log")
			stops := catchStops()
			defer stops.release()
			var out strings.Builder
			synctest.Test(t, func(t *testing.T) {
				asked := make(chan struct{})
				panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking}, step: func(read int) {
					if read == 1 {
						close(asked)
					}
				}}
				var sent []tea.Msg
				talks, log := dashboardTalks(t, repo, logPath, panes, &sent)
				talks.start(false)
				talks.dashboardClosed()
				stopped := organPhase(context.Background(), signalledReview{stops: stops, sig: sig, asked: asked},
					options{Review: true}, stops, log, dispatch.ExitOK, "", tui.Printer{Out: &strings.Builder{}},
					func() {})
				if !stopped {
					t.Error("the organ phase didn't report its stop")
				}
				talks.finish(stops, stopped, &out)
				if err := log.Close(); err != nil {
					t.Fatal(err)
				}
			})
			if want := "The answer talk was closed.\nStill open: k-7. orchestra answer asks it again.\n"; out.String() != want {
				t.Errorf("orchestra's terminal reads\n%s\nwant\n%s", out.String(), want)
			}
		})
	}
}

// overReview is the organs with a run report that waits for the answer talk open to be over.
type overReview struct {
	fakeOrgans
	talks *answerTalks
}

func (o overReview) Review(ctx context.Context, code int, final string) (string, error) {
	for o.talks.open.Load() != nil {
		time.Sleep(time.Second)
	}
	return o.fakeOrgans.Review(ctx, code, final)
}

// A talk open as the dashboard closed that ends during the run report isn't waited for, and orchestra
// says how it ended all the same.
func TestAnswerTalkEndedDuringTheReportIsSaid(t *testing.T) {
	t.Parallel()
	repo, _, _ := answerSetup(t, answerBacklog())
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	stops := catchStops()
	defer stops.release()
	var out strings.Builder
	synctest.Test(t, func(t *testing.T) {
		var sent []tea.Msg
		talks, log := dashboardTalks(t, repo, logPath,
			&fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateGone}}, &sent)
		talks.start(false)
		talks.dashboardClosed()
		stopped := organPhase(context.Background(), overReview{talks: talks}, options{Review: true}, stops, log,
			dispatch.ExitOK, "", tui.Printer{Out: &strings.Builder{}}, func() {})
		talks.finish(stops, stopped, &out)
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
	})
	if want := "Still open: k-7. orchestra answer asks it again.\n"; out.String() != want {
		t.Errorf("orchestra's terminal reads\n%s\nwant\n%s", out.String(), want)
	}
}

// The talk the dashboard opens as soon as it hears of the last one's end counts as open: the run's end
// says it waits for it, and Ctrl+C closes it.
func TestNextAnswerTalkIsWaitedOn(t *testing.T) {
	t.Parallel()
	repo, _, _ := answerSetup(t, answerBacklog())
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	stops := catchStops()
	defer stops.release()
	var out strings.Builder
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateGone}}
		log, err := dispatch.OpenLog(logPath, false, "t")
		if err != nil {
			t.Fatal(err)
		}
		c := options{Config: dispatch.Config{Repo: repo}}
		c.where.pane = "w1:p1"
		talk := dashboardAnswerTalk(c, log)
		talk.panes, talk.claude = &talkPanes{fakePanes: panes}, claudeSession{repo: repo, tools: answerToolArgs()}
		var talks *answerTalks
		next := make(chan struct{})
		talks = newAnswerTalks(runCtx(t), talk, "", log, func(msg tea.Msg) {
			if _, ok := msg.(tui.AnswerOver); !ok {
				return
			}
			select {
			case <-next:
				return
			default:
			}
			// the a key, as the dashboard takes in the end: the next talk's pane stays open until a stop
			panes.closed, panes.agent = false, []dispatch.AgentState{dispatch.StateWorking}
			panes.step = func(read int) {
				if read == 6 {
					stops.deliver(os.Interrupt)
				}
			}
			talks.start(true)
			close(next)
		})
		talks.start(false)
		<-next
		talks.dashboardClosed()
		talks.finish(stops, false, &out)
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
	})
	want := "The answer talk is still open in the pane on the right: orchestra ends once it is over " +
		"(Ctrl+C closes it).\nThe answer talk was closed.\nStill open: k-7. orchestra answer asks it again.\n"
	if out.String() != want {
		t.Errorf("orchestra's terminal reads\n%s\nwant\n%s", out.String(), want)
	}
}
