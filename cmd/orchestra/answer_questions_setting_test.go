package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The dashboard opens the answer talk by itself unless "questions" is "key" in the settings: "auto" is
// the default, and any other value a problem.
func TestConfigQuestions(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || !c.answerAuto {
		t.Errorf("default: auto %v, %v", c.answerAuto, p)
	}
	for settings, want := range map[string]bool{`{"concurrent": 2, "questions": "auto"}`: true,
		`{"concurrent": 2, "questions": "key"}`: false} {
		if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if c, p := loadWith(t); len(p) > 0 || c.answerAuto != want {
			t.Errorf("%s: auto %v, %v", settings, c.answerAuto, p)
		}
	}
	if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"),
		[]byte(`{"concurrent": 2, "questions": "never"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "questions") {
		t.Errorf("an unknown value: %v", p)
	}
}
