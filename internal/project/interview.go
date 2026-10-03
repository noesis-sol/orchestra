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

// The interview's files in .orchestra/run/ of the main checkout.
const (
	interviewPromptName = "interview-prompt.md"
	FeatureName         = "feature.json" // {"epic":"<id>"}, written by the session once it has filed the feature
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
