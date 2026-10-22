package project

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// The answer talk is a Claude Code session in the main checkout where the maintainer answers the
// questions workers asked (issues labelled human), each offered with its options: its instructions
// (answer-prompt.md) are appended to Claude Code's system prompt from .orchestra/run/, and its first
// message brings in the questions, from a file orchestra writes there. The instructions are a
// text/template, whose OnTerminal adds what a session on orchestra's own terminal says as it ends.
//
// Each talk has its files in a folder of its own, answer-<n>, so that two talks open at once (the
// dashboard's, and orchestra answer in another terminal) read only their own questions and
// instructions, and neither rewrites the other's. A talk's folder outlives it, as claude may still be
// open in a pane orchestra didn't close, reading its questions again; a new talk prunes those last
// written over AnswerFolderAge ago.
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

// AnswerFolderAge is how long since a talk's folder or a file in it was last written before a new talk
// prunes it: a talk still open writes each batch of questions queued into it to its folder.
const AnswerFolderAge = 24 * time.Hour

// AnswerFiles is one answer talk's files, in its own folder in .orchestra/run/ of the main checkout.
type AnswerFiles struct {
	repo   string
	folder string // its folder's name in .orchestra/run/: answer-<n>
}

// NewAnswerFiles makes a folder of its own in .orchestra/run/ of the main checkout repo for an answer
// talk's files: answer-<n>, n one past the highest there, so that a talk started beside another, by
// this orchestra or another, writes over none of its files. It removes the folders of talks last written
// over AnswerFolderAge ago, past the one it makes, so that a long-lived checkout keeps no folder a talk
// a day gone left.
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
	var talks []string
	for _, e := range entries {
		if num, ok := strings.CutPrefix(e.Name(), AnswerFolderPrefix); ok {
			if i, err := strconv.Atoi(num); err == nil {
				n = max(n, i)
				if e.IsDir() {
					talks = append(talks, e.Name())
				}
			}
		}
	}
	for {
		n++
		folder := AnswerFolderPrefix + strconv.Itoa(n)
		switch err := root.Mkdir(RunPath(folder), 0o755); {
		case err == nil:
			pruneAnswerFolders(root, talks, time.Now().Add(-AnswerFolderAge))
			return AnswerFiles{repo: repo, folder: folder}, nil
		case !errors.Is(err, fs.ErrExist): // one made since it was read is passed over
			return AnswerFiles{}, RunError(repo, RunPath(folder), err)
		}
	}
}

// pruneAnswerFolders removes from the run folder in root those of the talks' folders whose folder and
// files were all last written before cutoff. The numbers of the folders it removes are passed over by the
// talk being made, which is numbered past them, and may come round again only once all are gone.
func pruneAnswerFolders(root *os.Root, talks []string, cutoff time.Time) {
	for _, folder := range talks {
		if lastWritten(root, RunPath(folder)).Before(cutoff) {
			// An old folder left behind holds the new talk back in nothing: it is pruned again next time.
			_ = root.RemoveAll(RunPath(folder))
		}
	}
}

// lastWritten is when the folder rel in root, or a file in it, was last written; now where it or
// something in it can't be read, so that a folder isn't pruned on a guess.
func lastWritten(root *os.Root, rel string) time.Time {
	fi, err := root.Lstat(rel)
	if err != nil {
		return time.Now()
	}
	latest := fi.ModTime()
	entries, err := fs.ReadDir(root.FS(), filepath.ToSlash(rel))
	if err != nil {
		return time.Now()
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return time.Now()
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
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
