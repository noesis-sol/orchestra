package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// A feature typed at the start of a run is talked through in a Claude Code session in the main
// checkout, with the spec breakdown's instructions in their Feature mode (spec.go, RefineFeature).
// Its first message brings the feature's description in from a file, and the session hands the epic
// it filed back to orchestra in .orchestra/run/feature.json.

// The feature's files in .orchestra/run/ of the main checkout.
const (
	featureRequestName = "feature-request.md" // the description, for a session whose first message brings it in
	FeatureName        = "feature.json"       // {"epic":"<id>"}, written by the session once it has filed the feature
)

// FeatureRunKey is the metadata key that every issue a feature's session files carries, with the
// feature run its first message names: what orchestra finds them by when the session ends without
// handing the epic back.
const FeatureRunKey = "feature_run"

// WriteFeatureRequest writes the feature's description, request, to .orchestra/run/ in the main
// checkout repo, in place of an earlier session's, and returns its path relative to repo. It is
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

// ErrNoFeature is FiledFeature's error when the session handed nothing back: it wrote no feature.json.
var ErrNoFeature = errors.New("no " + FeatureName)

// FiledFeature returns the epic a feature's session in the main checkout repo filed, as its
// feature.json names it. Without the file the error is ErrNoFeature; a file that can't be read, or
// names no epic, is an error saying so.
func FiledFeature(repo string) (string, error) {
	return handedBack(repo, FeatureName, ErrNoFeature)
}

// handedBack returns the epic a session in the main checkout repo handed back to orchestra in the
// file name in .orchestra/run/, {"epic":"<id>"}. Without the file the error is none; a file that
// can't be read, or names no epic, is an error saying so.
func handedBack(repo, name string, none error) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // read-only
	rel := RunPath(name)
	b, err := root.ReadFile(rel)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", none
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
