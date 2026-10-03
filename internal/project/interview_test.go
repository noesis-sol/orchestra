package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WriteInterview writes the interview's instructions, with the notice of the work they adapt, and
// removes the feature.json an earlier interview left, so that FiledFeature only reads this one's.
func TestWriteInterviewWritesTheInstructionsAndForgetsTheLastFeature(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, Dir, RunName), 0o755); err != nil {
		t.Fatal(err)
	}
	feature := filepath.Join(repo, RunPath(FeatureName))
	if err := os.WriteFile(feature, []byte(`{"epic":"old-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if epic, err := FiledFeature(repo); err != nil || epic != "old-1" {
		t.Fatalf("the earlier feature reads %q, %v", epic, err)
	}
	p, err := WriteInterview(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || p != filepath.Join(repo, RunPath("interview-prompt.md")) {
		t.Fatalf("instructions at %s: %v", p, err)
	}
	for _, want := range []string{"MIT License", "Copyright (c) 2026 Matt Pocock", "mattpocock/skills",
		"# Feature interview", ".orchestra/run/" + FeatureName} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the instructions lack %q", want)
		}
	}
	if _, err := FiledFeature(repo); !errors.Is(err, ErrNoFeature) {
		t.Errorf("after WriteInterview: %v, want ErrNoFeature", err)
	}
}

// FiledFeature reads the epic from feature.json, and says what is wrong with one that names none.
func TestFiledFeatureReadsTheEpic(t *testing.T) {
	for _, tc := range []struct {
		file, epic, err string
	}{
		{`{"epic":"f-1"}` + "\n", "f-1", ""},
		{`{"epic":" f-2 "}`, "f-2", ""},
		{`{"epic":""}`, "", "names no epic"},
		{`{}`, "", "names no epic"},
		{`f-1`, "", "invalid character"},
	} {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, Dir, RunName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, RunPath(FeatureName)), []byte(tc.file), 0o644); err != nil {
			t.Fatal(err)
		}
		epic, err := FiledFeature(repo)
		if epic != tc.epic || (tc.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%q: got %q, %v", tc.file, epic, err)
		}
	}
}
