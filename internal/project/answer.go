package project

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

// The answer talk is a Claude Code session in the main checkout where the maintainer answers the
// questions workers asked (issues labelled human), each offered with its options: its instructions
// (answer-prompt.md) are appended to Claude Code's system prompt from .orchestra/run/, and its first
// message brings in the questions, from a file orchestra writes there. The instructions are a
// text/template, whose OnTerminal adds what a session on orchestra's own terminal says as it ends.
//
// Each talk has its files in a folder of its own, answer-<n>, so that two talks open at once (the
// dashboard's, and orchestra answer in another terminal) read only their own questions and
// instructions, and neither rewrites the other's.
//
//go:embed answer-prompt.md
var answerPromptText string

// answerPrompt is answer-prompt.md, parsed.
var answerPrompt = template.Must(template.New(answerPromptName).Option("missingkey=error").Parse(answerPromptText))

// The answer talk's files in its folder in .orchestra/run/ of the main checkout.
const (
	answerPromptName = "answer-prompt.md" // its instructions
	// AnswerQuestionsName is the questions it asks, as its first message brings them in.
	AnswerQuestionsName = "questions.md"
	// AnswerFolderPrefix begins the name of each talk's folder: answer-1, answer-2 and so on.
	AnswerFolderPrefix = "answer-"
)

// AnswerFiles is one answer talk's files, in its own folder in .orchestra/run/ of the main checkout.
type AnswerFiles struct {
	repo   string
	folder string // its folder's name in .orchestra/run/: answer-<n>
}

// NewAnswerFiles makes a folder of its own in .orchestra/run/ of the main checkout repo for an answer
// talk's files: answer-<n>, n one past the highest there, so that a talk started beside another, by
// this orchestra or another, writes over none of its files.
func NewAnswerFiles(repo string) (AnswerFiles, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return AnswerFiles{}, err
	}
	defer func() { _ = root.Close() }() // nothing written: only a folder made
	entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(RunPath("")))
	if err != nil {
		return AnswerFiles{}, RunError(repo, RunPath(""), err)
	}
	n := 0
	for _, e := range entries {
		if num, ok := strings.CutPrefix(e.Name(), AnswerFolderPrefix); ok {
			if i, err := strconv.Atoi(num); err == nil {
				n = max(n, i)
			}
		}
	}
	for {
		n++
		folder := AnswerFolderPrefix + strconv.Itoa(n)
		switch err := root.Mkdir(RunPath(folder), 0o755); {
		case err == nil:
			return AnswerFiles{repo: repo, folder: folder}, nil
		case !errors.Is(err, fs.ErrExist): // one made since it was read is passed over
			return AnswerFiles{}, RunError(repo, RunPath(folder), err)
		}
	}
}

// WritePrompt writes the talk's instructions to its folder, over any it wrote before, and returns their
// path: for a session in a pane beside orchestra's, or, with onTerminal, on orchestra's own terminal,
// which orchestra can only wait on to end.
func (f AnswerFiles) WritePrompt(onTerminal bool) (string, error) {
	var text bytes.Buffer
	if err := answerPrompt.Execute(&text, struct{ OnTerminal bool }{onTerminal}); err != nil {
		return "", fmt.Errorf("cannot make the answer talk's instructions: %w", err)
	}
	return writeRunFile(f.repo, filepath.Join(f.folder, answerPromptName), text.Bytes())
}

// WriteQuestions writes text, the questions the talk asks, to its folder, over those it wrote before,
// and returns the file's path relative to the checkout and slash-separated, as the talk's first message
// names it.
func (f AnswerFiles) WriteQuestions(text []byte) (string, error) {
	return f.writeNamed(AnswerQuestionsName, text)
}

// WriteMore writes text, questions queued into the talk while it is open, to its folder, in the file of
// its batch n (from 2: questions-2.md), and returns its path as WriteQuestions does.
func (f AnswerFiles) WriteMore(n int, text []byte) (string, error) {
	return f.writeNamed(fmt.Sprintf("questions-%d.md", n), text)
}

// writeNamed writes text to the file name in the talk's folder and returns its path as WriteQuestions
// does.
func (f AnswerFiles) writeNamed(name string, text []byte) (string, error) {
	rel := filepath.Join(f.folder, name)
	if _, err := writeRunFile(f.repo, rel, text); err != nil {
		return "", err
	}
	return filepath.ToSlash(RunPath(rel)), nil
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
