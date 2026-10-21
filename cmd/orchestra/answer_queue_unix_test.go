//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// talkPanes is fakePanes as the answer talk uses it: a pane split aside is w1:p2 too, recorded among
// the calls as "aside"; the pane focused, the agent named and its prompts are recorded apart, as the
// dashboard's a key and the questions queued reach them from goroutines of their own. promptErr is
// what each prompt returns.
type talkPanes struct {
	*fakePanes
	promptErr error

	mu      sync.Mutex
	focused []string // the panes focused
	adopted []string // "<pane> <kind> <name>"
	prompts []string // "<name>: <prompt>"
}

func (f *talkPanes) SplitPaneAside(_ context.Context, pane, cwd string) (string, error) {
	f.calls = append(f.calls, "aside "+pane+" "+cwd)
	if f.splitErr != nil {
		return "", f.splitErr
	}
	return "w1:p2", nil
}

func (f *talkPanes) FocusPane(_ context.Context, pane string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.focused = append(f.focused, pane)
	return nil
}

func (f *talkPanes) AdoptAgent(_ context.Context, pane, kind, name string) (dispatch.AgentState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adopted = append(f.adopted, pane+" "+kind+" "+name)
	return dispatch.StateIdle, nil
}

func (f *talkPanes) Prompt(_ context.Context, name, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, name+": "+prompt)
	return f.promptErr
}

// seen returns what was focused, named and prompted so far.
func (f *talkPanes) seen() (focused, adopted, prompts []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.focused), slices.Clone(f.adopted), slices.Clone(f.prompts)
}

// queueTalks is the answer talks of a run in repo under the dashboard, through panes, as
// dashboardTalks has them.
func queueTalks(t *testing.T, repo string, panes *talkPanes, sent *[]tea.Msg) (*answerTalks, *dispatch.Log) {
	t.Helper()
	log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	c := options{Config: dispatch.Config{Repo: repo}}
	c.where.pane = "w1:p1"
	talk := dashboardAnswerTalk(c, log)
	talk.panes, talk.claude = panes, claudeSession{repo: repo, tools: answerToolArgs()}
	return newAnswerTalks(runCtx(t), talk, "", log, func(msg tea.Msg) { *sent = append(*sent, msg) }), log
}

// fileQuestion adds the question k-8, blocking k-2, to the fake bd's issues, as a worker asking it
// does.
func fileQuestion(t *testing.T, state string) {
	t.Helper()
	issues := readIssues(t, state)
	issues["k-2"] = &answerIssue{ID: "k-2", Title: "Name the flag", Status: "open", Deps: map[string]string{"k-8": "blocks"}}
	issues["k-8"] = &answerIssue{ID: "k-8", Title: "Which flag name?", Status: "open", Labels: []string{dispatch.HumanLabel},
		Description: "--json or --format?"}
	writeIssues(t, state, issues)
}

// writeIssues writes the fake bd's issues to its file.
func writeIssues(t *testing.T, state string, issues map[string]*answerIssue) {
	t.Helper()
	b, err := json.Marshal(issues)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// respond answers the question q through the fake bd, as claude in the talk does.
func respond(t *testing.T, bd, q string) {
	t.Helper()
	if out, err := exec.Command(bd, "human", "respond", q, "--response", "yes").CombinedOutput(); err != nil {
		t.Errorf("bd human respond %s: %v\n%s", q, err, out)
	}
}

// Opened by itself, the talk's pane is split aside, leaving the focus in orchestra's, and the a key
// goes to it. A question that comes to wait while it is open is queued into it: once claude's turn is
// over, with the first answered, it is given the question, from a file of its own, and the pane closes
// only once both are answered. The dashboard is told the talk asked both.
func TestDashboardTalkOpensAsideAndTakesQueuedQuestions(t *testing.T) {
	t.Parallel()
	repo, bd, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		var talks *answerTalks
		var answered bool
		panes := &talkPanes{fakePanes: &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking,
			dispatch.StateWorking, dispatch.StateIdle}}}
		panes.step = func(read int) {
			switch _, _, prompts := panes.seen(); {
			case read == 2:
				talks.focus()
				respond(t, bd, "k-7")
				fileQuestion(t, state)
				talks.queue()
			case len(prompts) > 0 && !answered:
				respond(t, bd, "k-8")
				answered = true
			case read > 200:
				t.Fatal("the talk never ended")
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
		if want := []string{"aside w1:p1 " + repo, "launch w1:p2 claude", "close w1:p2"}; !slices.Equal(panes.calls, want) {
			t.Errorf("the terminal was asked to %q, want %q", panes.calls, want)
		}
		focused, adopted, prompts := panes.seen()
		if !slices.Equal(focused, []string{"w1:p2"}) {
			t.Errorf("the a key focused %q, want the talk's pane", focused)
		}
		if len(adopted) != 1 || !strings.HasPrefix(adopted[0], "w1:p2 claude answer-w1_p1") {
			t.Errorf("claude was named %q", adopted)
		}
		const more = "1 more open question has come in: let's answer it in @.orchestra/run/questions-2.md too, the same way."
		if len(prompts) != 1 || !strings.HasSuffix(prompts[0], ": "+more) {
			t.Errorf("claude was prompted %q, want %q", prompts, more)
		}
		if len(sent) != 1 {
			t.Fatalf("the dashboard was sent %v, want the talk's end", sent)
		}
		over, ok := sent[0].(tui.AnswerOver)
		if !ok || !slices.Equal(over.Asked, []string{"k-7", "k-8"}) || !slices.Equal(over.Answered, over.Asked) ||
			over.Err != nil {
			t.Errorf("the dashboard was sent %+v", sent[0])
		}
	})
	queued := read(t, filepath.Join(repo, project.RunPath("questions-2.md")))
	if !strings.Contains(queued, "## 1. k-8: Which flag name?") || strings.Contains(queued, "k-7:") {
		t.Errorf("the queued question's file:\n%s", queued)
	}
}

// A question claude couldn't be given is asked no longer: the pane closes once the first is answered,
// and the dashboard is told the talk didn't ask the second, for the next talk to take it up.
func TestDashboardTalkDropsAQuestionClaudeCouldNotBeGiven(t *testing.T) {
	t.Parallel()
	repo, bd, state := answerSetup(t, answerBacklog())
	synctest.Test(t, func(t *testing.T) {
		var talks *answerTalks
		panes := &talkPanes{fakePanes: &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking,
			dispatch.StateIdle}}, promptErr: errors.New("tmux: no such agent")}
		panes.step = func(read int) {
			switch {
			case read == 1:
				respond(t, bd, "k-7")
				fileQuestion(t, state)
				talks.queue()
			case read > 200:
				t.Fatal("the talk never ended")
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
		if len(sent) != 1 {
			t.Fatalf("the dashboard was sent %v, want the talk's end", sent)
		}
		if over, ok := sent[0].(tui.AnswerOver); !ok || !slices.Equal(over.Asked, []string{"k-7"}) || over.Err != nil {
			t.Errorf("the dashboard was sent %+v", sent[0])
		}
	})
	if q := readIssues(t, state)["k-8"]; q.Status != "open" {
		t.Errorf("the question claude wasn't given: %+v", q)
	}
}
