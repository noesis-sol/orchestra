package project

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// A feature typed at the start of a run is talked through in a Claude Code session in the main
// checkout, its instructions (interview-prompt.md, adapted from mattpocock/skills under the MIT
// License, whose notice it carries) appended to Claude Code's system prompt. The session files the
// feature in Beads as an epic and its tickets, and names the epic in .orchestra/run/feature.json
// for orchestra to run.

//go:embed interview-prompt.md
var interviewPrompt string

// interviewOnTerminal is what the instructions add for a session on orchestra's own terminal (see
// WriteTerminalInterview).
const interviewOnTerminal = "\n## On orchestra's terminal\n\n" +
	"orchestra runs this session on its own terminal, not in a pane beside its own, and can't close it: it\n" +
	"waits for the session to end. Once the feature is filed, tell the user to type `/exit` to hand back\n" +
	"to orchestra, which then shows the tickets and asks whether to start the run on them.\n"

// The interview's files in .orchestra/run/ of the main checkout.
const (
	interviewPromptName = "interview-prompt.md"
	featureRequestName  = "feature-request.md" // the description, for a session whose first message brings it in
	FeatureName         = "feature.json"       // {"epic":"<id>"}, written by the session once it has filed the feature
)

// WriteInterview readies the main checkout repo for a feature interview: it removes the
// feature.json an earlier interview left, so that only this one's counts, and writes the
// interview's instructions to .orchestra/run/. It returns their path.
func WriteInterview(repo string) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	if err := RemoveRun(root, repo, RunPath(FeatureName)); err != nil {
		return "", err
	}
	rel := RunPath(interviewPromptName)
	if err := WriteRun(root, repo, rel, []byte(interviewPrompt), 0o644); err != nil {
		return "", err
	}
	return filepath.Join(repo, rel), nil
}

// WriteTerminalInterview writes the instructions for an interview on orchestra's own terminal over
// those WriteInterview wrote to the main checkout repo, and returns their path, the same. orchestra
// closes a session in a pane beside its own once the feature is filed, but can only wait for one on
// its terminal to end: these instructions add that claude tells the user to type /exit then.
func WriteTerminalInterview(repo string) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := RunPath(interviewPromptName)
	if err := WriteRun(root, repo, rel, []byte(interviewPrompt+interviewOnTerminal), 0o644); err != nil {
		return "", err
	}
	return filepath.Join(repo, rel), nil
}

// WriteFeatureRequest writes the feature's description, request, to .orchestra/run/ in the main
// checkout repo, in place of an earlier interview's, and returns its path relative to repo. It is
// for a session started by typing its command into a shell (a Herdr pane's), which takes no
// argument with line breaks: its first message is one line that brings the file in.
func WriteFeatureRequest(repo, request string) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := RunPath(featureRequestName)
	if err := WriteRun(root, repo, rel, []byte(strings.TrimRight(request, "\n")+"\n"), 0o644); err != nil {
		return "", err
	}
	return rel, nil
}

// ErrNoFeature is FiledFeature's error when the interview filed nothing: it wrote no feature.json.
var ErrNoFeature = errors.New("no " + FeatureName)

// FiledFeature returns the epic a feature interview in the main checkout repo filed, as its
// feature.json names it. Without the file the error is ErrNoFeature; a file that can't be read, or
// names no epic, is an error saying so.
func FiledFeature(repo string) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // read-only
	rel := RunPath(FeatureName)
	b, err := root.ReadFile(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", ErrNoFeature
	case err != nil:
		return "", RunError(repo, rel, err)
	}
	var f struct {
		Epic string `json:"epic"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return "", fmt.Errorf("%s: %w", rel, err)
	}
	if f.Epic = strings.TrimSpace(f.Epic); f.Epic == "" {
		return "", fmt.Errorf("%s names no epic", rel)
	}
	return f.Epic, nil
}
