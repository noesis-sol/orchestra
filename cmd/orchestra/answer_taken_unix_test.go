//go:build unix

package main

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// talkEnd is the talk's end the dashboard was sent last, failing the test where it was sent anything
// else but the answers taken before it.
func talkEnd(t *testing.T, sent []tea.Msg) tui.AnswerOver {
	t.Helper()
	if len(sent) == 0 {
		t.Fatal("the dashboard was sent nothing, want the talk's end")
	}
	for _, msg := range sent[:len(sent)-1] {
		if _, ok := msg.(tui.AnswerTaken); !ok {
			t.Fatalf("the dashboard was sent %+v before the talk's end", msg)
		}
	}
	over, ok := sent[len(sent)-1].(tui.AnswerOver)
	if !ok {
		t.Fatalf("the dashboard was sent %+v last, want the talk's end", sent[len(sent)-1])
	}
	return over
}

// taken is the questions the dashboard was told were answered while the talk was open, in order.
func taken(sent []tea.Msg) []string {
	var ids []string
	for _, msg := range sent {
		if a, ok := msg.(tui.AnswerTaken); ok {
			ids = append(ids, a.IDs...)
		}
	}
	return ids
}

// The talk tells the dashboard of each answer as bd closes its question, while it goes on with the
// others, each once: the hint counts it no longer.
func TestDashboardTalkTellsOfEachAnswerAsItIsRecorded(t *testing.T) {
	t.Parallel()
	repo, bd, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		var talks *answerTalks
		var sent []tea.Msg
		panes := &talkPanes{fakePanes: &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking,
			dispatch.StateIdle}}}
		panes.step = func(read int) {
			switch {
			case read == 2:
				fileQuestion(t, state)
				talks.queue()
			case read == 30:
				respond(t, bd, "k-7")
			case read == 60:
				if got := taken(sent); !slices.Equal(got, []string{"k-7"}) || len(sent) != 1 {
					t.Errorf("with k-7 answered and k-8 open, the dashboard was sent %+v, want k-7 taken", sent)
				}
				respond(t, bd, "k-8")
			case read > 200:
				t.Fatal("the talk never ended")
			}
		}
		var log *dispatch.Log
		talks, log = queueTalks(t, repo, panes, &sent)
		talks.start(false)
		talks.wg.Wait()
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		if got := taken(sent); !slices.Equal(got, []string{"k-7", "k-8"}) {
			t.Errorf("the dashboard was told of %q answered, want k-7 then k-8", got)
		}
		if over := talkEnd(t, sent); !slices.Equal(over.Answered, []string{"k-7", "k-8"}) || over.Err != nil {
			t.Errorf("the dashboard was sent %+v", over)
		}
	})
}
