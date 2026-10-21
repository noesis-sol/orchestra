//go:build unix

package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// leftOpen has the next talk through panes left with its questions open: claude reads them, and the
// maintainer types /exit.
func leftOpen(panes *fakePanes) {
	panes.agent, panes.reads, panes.closed, panes.calls = []dispatch.AgentState{dispatch.StateWorking,
		dispatch.StateGone}, 0, false, nil
}

// lastAsked is the questions the talk last over told the dashboard it asked.
func lastAsked(t *testing.T, sent []tea.Msg) []string {
	t.Helper()
	if len(sent) == 0 {
		t.Fatal("the dashboard was sent nothing")
	}
	over, ok := sent[len(sent)-1].(tui.AnswerOver)
	if !ok {
		t.Fatalf("the dashboard was sent %+v, want the talk's end", sent[len(sent)-1])
	}
	return over.Asked
}

// A question left open in a talk now over is put to the maintainer: the loop counts it so, a later talk
// that opens by itself leaves it out and asks only the question asked since, and the a key's talk asks
// both.
func TestLaterTalkLeavesOutTheQuestionsAnEarlierLeftOpen(t *testing.T) {
	t.Parallel()
	repo, _, state := answerSetup(t, answerBacklog())
	questionsFile := filepath.Join(repo, project.RunPath("questions.md"))
	synctest.Test(t, func(t *testing.T) {
		panes := &fakePanes{}
		var sent []tea.Msg
		talks, log := dashboardTalks(t, repo, filepath.Join(t.TempDir(), "orchestra.log"), panes, &sent)
		defer func() {
			if err := log.Close(); err != nil {
				t.Error(err)
			}
		}()
		leftOpen(panes)
		talks.start(false)
		talks.wg.Wait()
		if got := lastAsked(t, sent); !slices.Equal(got, []string{"k-7"}) {
			t.Errorf("the first talk asked %q, want k-7", got)
		}
		qs, err := runAnswers{tracker: talks.talk.tracker, talks: talks}.Questions(testCtx(context.Background(), t))
		if err != nil || len(qs) != 1 || !qs[0].Put {
			t.Errorf("the loop read %+v, %v; want k-7, put to the maintainer", qs, err)
		}

		fileQuestion(t, state)
		leftOpen(panes)
		talks.start(false)
		talks.wg.Wait()
		if got := lastAsked(t, sent); !slices.Equal(got, []string{"k-8"}) {
			t.Errorf("the talk opened by itself asked %q, want only k-8", got)
		}
		if asked := read(t, questionsFile); strings.Contains(asked, "k-7:") || !strings.Contains(asked, "k-8:") {
			t.Errorf("the talk opened by itself was given\n%s", asked)
		}

		leftOpen(panes)
		talks.start(true)
		talks.wg.Wait()
		if got := lastAsked(t, sent); !slices.Equal(got, []string{"k-7", "k-8"}) {
			t.Errorf("the a key's talk asked %q, want k-7 and k-8", got)
		}
	})
}

// A question queued into the talk as it ends, before claude was given it, wasn't asked there: the
// dashboard is told the talk didn't ask it, and it isn't put to the maintainer, for the next talk to
// take up.
func TestQuestionQueuedAsTheTalkEndsIsNotCountedAsked(t *testing.T) {
	t.Parallel()
	repo, _, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		var talks *answerTalks
		// claude works on until the maintainer types /exit: its turn is never over to be given k-8
		panes := &talkPanes{fakePanes: &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking,
			dispatch.StateWorking, dispatch.StateWorking, dispatch.StateGone}}}
		panes.step = func(read int) {
			if read == 1 {
				fileQuestion(t, state)
				talks.queue()
			}
		}
		var sent []tea.Msg
		var log *dispatch.Log
		talks, log = queueTalks(t, repo, panes, &sent)
		talks.start(false)
		talks.wg.Wait()
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, prompts := panes.seen(); len(prompts) != 0 {
			t.Fatalf("claude was given %q, want nothing: its turn was never over", prompts)
		}
		if got := lastAsked(t, sent); !slices.Equal(got, []string{"k-7"}) {
			t.Errorf("the talk asked %q, want only k-7", got)
		}
		if !talks.wasPut("k-7") || talks.wasPut("k-8") {
			t.Errorf("put: k-7 %v, k-8 %v; want k-7 only", talks.wasPut("k-7"), talks.wasPut("k-8"))
		}
	})
}
