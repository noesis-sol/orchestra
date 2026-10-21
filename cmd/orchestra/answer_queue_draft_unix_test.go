//go:build unix

package main

import (
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

// queuedTalk runs the dashboard's answer talk in repo through panes, with claude at work on its first
// reading and idle after, its turn over, until the talk ends; step is each reading's, and a reading past
// 200 fails the test. It returns what the dashboard was sent.
func queuedTalk(t *testing.T, repo string, panes *talkPanes, step func(talks *answerTalks, read int)) []tea.Msg {
	t.Helper()
	var talks *answerTalks
	panes.agent = []dispatch.AgentState{dispatch.StateWorking, dispatch.StateIdle}
	panes.step = func(read int) {
		if read > 200 {
			t.Fatal("the talk never ended")
		}
		step(talks, read)
	}
	var sent []tea.Msg
	var log *dispatch.Log
	talks, log = queueTalks(t, repo, panes, &sent)
	talks.start(false)
	talks.wg.Wait()
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	return sent
}

// talkOver is the talk's end the dashboard was sent, as talkEnd has it, failing the test where it failed.
func talkOver(t *testing.T, sent []tea.Msg) tui.AnswerOver {
	t.Helper()
	over := talkEnd(t, sent)
	if over.Err != nil {
		t.Fatalf("the dashboard was sent %+v", over)
	}
	return over
}

// A question that comes to wait while the talk waits on the maintainer, claude's turn over and the
// first question still open, is typed into nothing: the maintainer may be half-way through their
// answer in claude's input, which the message would be pasted after and sent with. The questions file
// gains it, for claude to read after the question it is on; it is given to claude in a message only
// once the first is answered.
func TestDashboardTalkTypesNothingWhileItWaitsOnTheMaintainer(t *testing.T) {
	t.Parallel()
	repo, bd, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		panes := &talkPanes{fakePanes: &fakePanes{}}
		var answered, given int
		sent := queuedTalk(t, repo, panes, func(talks *answerTalks, n int) {
			_, _, prompts := panes.seen()
			switch {
			case n == 2:
				fileQuestion(t, state)
				talks.queue()
			case n == 30:
				if file := read(t, filepath.Join(repo, project.RunPath(project.AnswerQuestionsName))); !strings.Contains(
					file, "## 1. k-7: Which encoding?") || !strings.Contains(file, "## 2. k-8: Which flag name?") {
					t.Errorf("the questions file, with k-7 still open:\n%s", file)
				}
				respond(t, bd, "k-7")
				answered = n
			case len(prompts) > 0 && given == 0:
				given = n
				respond(t, bd, "k-8")
			}
		})
		if given == 0 || given <= answered {
			t.Errorf("claude was given the question queued at reading %d, the first answered at %d", given, answered)
		}
		_, _, prompts := panes.seen()
		const more = "1 more open question has come in: let's answer it in @.orchestra/run/questions-2.md too, the same way."
		if len(prompts) != 1 || !strings.HasSuffix(prompts[0], ": "+more) {
			t.Errorf("claude was prompted %q, want %q", prompts, more)
		}
		if over := talkOver(t, sent); !slices.Equal(over.Asked, []string{"k-7", "k-8"}) ||
			!slices.Equal(over.Answered, over.Asked) {
			t.Errorf("the dashboard was sent %+v", over)
		}
	})
}

// A question claude takes up from the questions file, which it reads again after each question, is
// typed into nothing: once bd has closed both, the talk ends.
func TestDashboardTalkLeavesAQueuedQuestionTakenFromTheFile(t *testing.T) {
	t.Parallel()
	repo, bd, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		panes := &talkPanes{fakePanes: &fakePanes{}}
		sent := queuedTalk(t, repo, panes, func(talks *answerTalks, n int) {
			switch n {
			case 2:
				fileQuestion(t, state)
				talks.queue()
			case 10:
				respond(t, bd, "k-7")
				respond(t, bd, "k-8")
			}
		})
		if _, adopted, prompts := panes.seen(); len(adopted)+len(prompts) > 0 {
			t.Errorf("claude was named %q and prompted %q, want neither", adopted, prompts)
		}
		if over := talkOver(t, sent); !slices.Equal(over.Asked, []string{"k-7", "k-8"}) ||
			!slices.Equal(over.Answered, over.Asked) {
			t.Errorf("the dashboard was sent %+v", over)
		}
	})
}

// A talk left with /exit while it waits on the maintainer gives claude nothing: the question queued,
// never given and still open, wasn't asked, for the dashboard to queue into the next talk.
func TestDashboardTalkLeftOpenDoesNotAskTheQueuedQuestion(t *testing.T) {
	t.Parallel()
	repo, _, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		panes := &talkPanes{fakePanes: &fakePanes{}}
		sent := queuedTalk(t, repo, panes, func(talks *answerTalks, n int) {
			switch n {
			case 2:
				fileQuestion(t, state)
				talks.queue()
			case 20:
				panes.closed = true // the maintainer typed /exit
			}
		})
		if _, _, prompts := panes.seen(); len(prompts) > 0 {
			t.Errorf("claude was prompted %q, want nothing", prompts)
		}
		if over := talkOver(t, sent); !slices.Equal(over.Asked, []string{"k-7"}) || len(over.Answered) > 0 {
			t.Errorf("the dashboard was sent %+v", over)
		}
	})
}
