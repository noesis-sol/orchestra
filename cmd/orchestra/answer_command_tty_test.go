//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra answer hands its terminal to claude, with the answer talk's tools, its instructions for
// the terminal and the questions; a claude that answers through bd closes the question, and orchestra
// says it was answered once claude exits.
func TestAnswerCommandRunsTheTalkOnTheTerminal(t *testing.T) {
	t.Parallel()
	_, _, state := answerSetup(t, answerBacklog())
	dir := t.TempDir()
	faketool.Write(t, dir, "claude", `#!/bin/sh
d='`+dir+`'
printf '[%s]\n' "$@" > "$d/claude-args"
[ -t 0 ] && [ -t 1 ] && echo yes > "$d/claude-tty"
cp "$2" "$d/claude-prompt"
bd human respond k-7 --response JSON
echo 'fake claude: bye'
`)
	onPath(t, dir)
	term, _, exit := runOnTerminal(t, "answer")
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	screen := screenOf(term)
	for _, want := range []string{
		"Answering the questions with claude, which asks each with its options. Type /exit to come back.\n",
		"fake claude: bye\n",
		"Answered k-7: the tickets waiting on it come back to the run going on, or to the next.\n",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, screen)
		}
	}
	if read(t, filepath.Join(dir, "claude-tty")) != "yes\n" {
		t.Error("claude didn't have the terminal")
	}
	args := read(t, filepath.Join(dir, "claude-args"))
	for _, want := range []string{"[--tools]\n[" + answerTools + "]\n", "[--permission-mode]\n[default]\n",
		"[Let's answer the 1 open question in @.orchestra/run/questions.md, one at a time.]\n"} {
		if !strings.Contains(args, want) {
			t.Errorf("claude's arguments lack %q:\n%s", want, args)
		}
	}
	if prompt, err := os.ReadFile(filepath.Join(dir, "claude-prompt")); err != nil ||
		!strings.Contains(string(prompt), "AskUserQuestion") || !strings.Contains(string(prompt), "/exit") {
		t.Errorf("claude's instructions (%v):\n%s", err, prompt)
	}
	if q := readIssues(t, state)["k-7"]; q.Status != "closed" {
		t.Errorf("the question: %+v", q)
	}
}
