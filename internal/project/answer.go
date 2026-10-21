package project

import (
	"bytes"
	_ "embed"
	"fmt"
	"path/filepath"
	"text/template"
)

// The answer talk is a Claude Code session in the main checkout where the maintainer answers the
// questions workers asked (issues labelled human), each offered with its options: its instructions
// (answer-prompt.md) are appended to Claude Code's system prompt from .orchestra/run/, and its first
// message brings in the questions, from a file orchestra writes there. The instructions are a
// text/template, whose OnTerminal adds what a session on orchestra's own terminal says as it ends.
//
//go:embed answer-prompt.md
var answerPromptText string

// answerPrompt is answer-prompt.md, parsed.
var answerPrompt = template.Must(template.New(answerPromptName).Option("missingkey=error").Parse(answerPromptText))

// The answer talk's files in .orchestra/run/ of the main checkout.
const (
	answerPromptName = "answer-prompt.md" // its instructions
	// AnswerQuestionsName is the questions it asks, as its first message brings them in.
	AnswerQuestionsName = "questions.md"
)

// WriteAnswerPrompt writes the answer talk's instructions to .orchestra/run/ in the main checkout
// repo, over an earlier talk's, and returns their path: for a session in a pane beside orchestra's,
// or, with onTerminal, on orchestra's own terminal, which orchestra can only wait on to end.
func WriteAnswerPrompt(repo string, onTerminal bool) (string, error) {
	var text bytes.Buffer
	if err := answerPrompt.Execute(&text, struct{ OnTerminal bool }{onTerminal}); err != nil {
		return "", fmt.Errorf("cannot make the answer talk's instructions: %w", err)
	}
	return writeRunFile(repo, answerPromptName, text.Bytes())
}

// WriteAnswerQuestions writes text, the questions the answer talk asks, to .orchestra/run/ in the main
// checkout repo, over an earlier talk's, and returns its path relative to repo and slash-separated, as
// the talk's first message names it.
func WriteAnswerQuestions(repo string, text []byte) (string, error) {
	if _, err := writeRunFile(repo, AnswerQuestionsName, text); err != nil {
		return "", err
	}
	return filepath.ToSlash(RunPath(AnswerQuestionsName)), nil
}

// WriteAnswerMore writes text, questions queued into the answer talk while it is open, to
// .orchestra/run/ in the main checkout repo, in the file of its batch n (from 2: questions-2.md), and
// returns its path as WriteAnswerQuestions does.
func WriteAnswerMore(repo string, n int, text []byte) (string, error) {
	name := fmt.Sprintf("questions-%d.md", n)
	if _, err := writeRunFile(repo, name, text); err != nil {
		return "", err
	}
	return filepath.ToSlash(RunPath(name)), nil
}

// writeRunFile writes b to the file name in .orchestra/run/ of the checkout repo, over any earlier one,
// and returns its path.
func writeRunFile(repo, name string, b []byte) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := RunPath(name)
	if err := WriteRun(root, repo, rel, b, 0o644); err != nil {
		return "", err
	}
	return filepath.Join(repo, rel), nil
}
