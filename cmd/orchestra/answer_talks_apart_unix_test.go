//go:build unix

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// talkFiles is the instructions and the questions file claude was started with in panes, as its
// arguments and first message name them, read while the talk is open.
func talkFiles(t *testing.T, repo string, panes *fakePanes) (prompt, questions string) {
	t.Helper()
	i := slices.Index(panes.launched, "--append-system-prompt-file")
	if i < 0 || len(panes.launched) == 0 {
		t.Fatalf("claude started with %q", panes.launched)
	}
	message := panes.launched[len(panes.launched)-1]
	_, file, ok := strings.Cut(message, "@")
	if !ok {
		t.Fatalf("the first message names no file: %q", message)
	}
	file, _, _ = strings.Cut(file, ",")
	return panes.launched[i+1], read(t, filepath.Join(repo, filepath.FromSlash(file)))
}

// Two answer talks open at once, the dashboard's and orchestra answer's in another terminal, each read
// only their own questions and instructions: the second, opened on k-8, writes over nothing of the
// first's, on k-7, which still asks only k-7 once the second is open.
func TestAnswerTalksOpenAtOnceReadOnlyTheirOwnFiles(t *testing.T) {
	t.Parallel()
	repo, _, state := answerSetup(t, answerBacklog())
	fileQuestion(t, state)
	synctest.Test(t, func(t *testing.T) {
		var firstPrompt, secondPrompt string
		second := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateGone}}
		second.step = func(read int) {
			if read != 1 {
				return
			}
			var questions string
			secondPrompt, questions = talkFiles(t, repo, second)
			if !strings.Contains(questions, "k-8: Which flag name?") || strings.Contains(questions, "k-7:") {
				t.Errorf("the second talk was given\n%s", questions)
			}
		}
		first := &fakePanes{agent: []dispatch.AgentState{dispatch.StateWorking, dispatch.StateWorking,
			dispatch.StateGone}}
		first.step = func(read int) {
			if read != 1 {
				return
			}
			firstPrompt, _ = talkFiles(t, repo, first)
			var out, errOut strings.Builder
			if _, err := paneAnswerTalk(t, repo, second, &out, &errOut).answer(runCtx(t), "", "k-8"); err != nil {
				t.Errorf("the second talk: %v\n%s", err, errOut.String())
			}
			_, questions := talkFiles(t, repo, first)
			if !strings.Contains(questions, "k-7: Which encoding?") || strings.Contains(questions, "k-8:") {
				t.Errorf("with the second talk open, the first talk's questions are\n%s", questions)
			}
		}
		var out, errOut strings.Builder
		if _, err := paneAnswerTalk(t, repo, first, &out, &errOut).answer(runCtx(t), "", "k-7"); err != nil {
			t.Fatalf("the first talk: %v\n%s", err, errOut.String())
		}
		if firstPrompt == "" || secondPrompt == "" || firstPrompt == secondPrompt {
			t.Errorf("the talks' instructions are %q and %q, want a file each", firstPrompt, secondPrompt)
		}
		for _, p := range []string{firstPrompt, secondPrompt} {
			if b, err := os.ReadFile(p); err != nil || !strings.Contains(string(b), "AskUserQuestion") {
				t.Errorf("the instructions at %s aren't the answer talk's (%v)", p, err)
			}
		}
	})
}
