//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// answerRepo is a repository set up for orchestra, with the fake bd keeping the issues given and a
// claude that fails on t's PATH; it returns the repository and the bd's issues' file.
func answerRepo(t *testing.T, issues map[string]*answerIssue) (repo, state string) {
	t.Helper()
	_, _, state = answerSetup(t, issues)
	return configFixture(t, `{"concurrent": 1}`), state
}

// orchestra answer runs the talk on its own terminal: with none to take, it says how to answer
// instead, the question left open, exit code 2.
func TestAnswerCommandTakesTheTerminal(t *testing.T) {
	t.Parallel()
	repo, state := answerRepo(t, answerBacklog())
	stdout, stderr, err := runIn(t, repo, nil, "answer")
	if code := exitOf(err); code != dispatch.ExitSetup ||
		!strings.Contains(stderr, "orchestra answer: answering questions needs you at a terminal") ||
		stdout != "Still open: k-7. orchestra answer asks it again.\n" {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	questions, err := os.ReadFile(filepath.Join(repo, project.RunPath("answer-1/questions.md")))
	if err != nil || !strings.Contains(string(questions), "## 1. k-7: Which encoding?") {
		t.Errorf("the questions (%v):\n%s", err, questions)
	}
	if q := readIssues(t, state)["k-7"]; q.Status != "open" {
		t.Errorf("the question: %+v", q)
	}
}

// With no question open, orchestra answer says so and starts nothing; one named that isn't a question
// is refused, and so are a question and --ticket together, or two questions.
func TestAnswerCommandWithNothingToAsk(t *testing.T) {
	t.Parallel()
	backlog := answerBacklog()
	backlog["k-7"].Status = "closed"
	repo, _ := answerRepo(t, backlog)
	if stdout, stderr, err := runIn(t, repo, nil, "answer"); exitOf(err) != dispatch.ExitOK ||
		stdout != "No question is open.\n" || stderr != "" {
		t.Errorf("exit %d\nstdout:\n%s\nstderr:\n%s", exitOf(err), stdout, stderr)
	}
	for _, tc := range []struct {
		args   []string
		stderr string
	}{
		{[]string{"answer", "k-3"}, "orchestra answer: k-3 isn't a question: it has no label human\n"},
		{[]string{"answer", "k-7"}, "orchestra answer: k-7 is answered already\n"},
		{[]string{"answer", "--ticket", "k-1", "k-7"}, "orchestra answer: give a question or --ticket, not both\n"},
		{[]string{"answer", "k-7", "k-9"}, "orchestra answer: unexpected argument \"k-9\" (see orchestra answer -h)\n"},
	} {
		if stdout, stderr, err := runIn(t, repo, nil, tc.args...); exitOf(err) != dispatch.ExitSetup ||
			stdout != "" || stderr != tc.stderr {
			t.Errorf("%q: exit %d\nstdout:\n%s\nstderr:\n%s", tc.args, exitOf(err), stdout, stderr)
		}
	}
}

// Outside a repository, orchestra answer says so.
func TestAnswerCommandOutsideARepository(t *testing.T) {
	t.Parallel()
	if _, stderr, err := runIn(t, t.TempDir(), nil, "answer"); exitOf(err) != dispatch.ExitSetup ||
		stderr != "orchestra answer cannot start:\n  - Not inside a git repository: cd into the project first.\n" {
		t.Errorf("exit %d\nstderr:\n%s", exitOf(err), stderr)
	}
}
