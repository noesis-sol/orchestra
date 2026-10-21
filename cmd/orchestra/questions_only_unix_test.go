//go:build unix

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// onlyQuestions is a backlog whose one open ticket, k-1, waits on the question k-q.
var onlyQuestions = backlog{
	unclosed: `[{"id":"k-q","status":"open","issue_type":"task","labels":["human"]},` +
		`{"id":"k-1","status":"open","issue_type":"task"}]`,
	questions: `[{"id":"k-q","status":"open","issue_type":"task","labels":["human"]}]`,
	dependents: map[string]string{
		"k-q": `[{"id":"k-1","status":"open","issue_type":"task","dependency_type":"blocks"}]`,
	},
}

// A run with nothing ready but a question goes on to its loop, which opens the answer talk on it, when
// it can open one: on the dashboard, in a pane, and not overnight. Otherwise it ends at once, saying why
// and pointing at orchestra answer.
func TestNothingReadyButQuestionsGoesOnToTheTalk(t *testing.T) {
	t.Parallel()
	repo := configFixture(t, `{"concurrent": 1}`)
	nothingTools(t, onlyQuestions) // after the fixture's tools, on PATH
	for _, tc := range []struct {
		name      string
		pane      string
		overnight bool
		plain     bool
		ends      bool
	}{
		{name: "in a pane", pane: "%1"},
		{name: "plain", pane: "%1", plain: true, ends: true},
		{name: "in no pane", ends: true},
		{name: "overnight", pane: "%1", overnight: true, ends: true},
	} {
		c := options{Config: dispatch.Config{Repo: repo, Base: "main", Limit: 10, Overnight: tc.overnight}}
		c.where.pane = tc.pane
		log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
		if err != nil {
			t.Fatal(err)
		}
		var out strings.Builder
		if ended := nothingToRun(testCtx(context.Background(), t), c, log, &out, tc.plain); ended != tc.ends {
			t.Errorf("%s: ended %v, want %v\n%s", tc.name, ended, tc.ends, out.String())
		}
		if tc.ends && !strings.Contains(out.String(), "1 question waits for your answer: orchestra answer") {
			t.Errorf("%s: the run doesn't point at orchestra answer:\n%s", tc.name, out.String())
		}
	}
}

// The loop reads the questions in the run's scope, each with the tickets not closed it blocks, as the
// answer talk asks them.
func TestRunAnswersListsTheQuestionsAndWhatTheyHold(t *testing.T) {
	t.Parallel()
	qs, err := runAnswers{tracker: asked(), scope: "k-1"}.Questions(context.Background())
	if err != nil || len(qs) != 1 || qs[0].ID != "k-7" || strings.Join(qs[0].Holds, ",") != "k-1.2" {
		t.Errorf("Questions = %+v, %v; want k-7, holding k-1.2", qs, err)
	}
	if over := (runAnswers{tracker: asked()}).Talk(qs, ""); over != nil {
		t.Error("with no talk to open, Talk gave a channel to wait on")
	}
}

// The answer talk opened with nothing else ready says how many tickets are deferred, and where, and
// that they are released only when asked.
func TestAnswerTalkQuestionsFileMentionsTheDeferred(t *testing.T) {
	t.Parallel()
	if got := deferredText(""); got != "" {
		t.Errorf("no deferred tickets: %q", got)
	}
	got := deferredText("35 deferred under tutti-kkb")
	for _, want := range []string{"# Deferred tickets", "35 deferred under tutti-kkb", "They are not questions",
		"release none unless the user asks you to", "bd undefer <id>"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, got)
		}
	}
}
