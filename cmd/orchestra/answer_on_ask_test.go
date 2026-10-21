package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The dashboard opens the answer talk by itself on a question only with answer_on_ask true in the
// settings: it is off by default.
func TestConfigAnswerOnAsk(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || c.answerOnAsk {
		t.Errorf("default: answer on ask %v, %v", c.answerOnAsk, p)
	}
	for settings, want := range map[string]bool{`{"concurrent": 2, "answer_on_ask": true}`: true,
		`{"concurrent": 2, "answer_on_ask": false}`: false} {
		if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if c, p := loadWith(t); len(p) > 0 || c.answerOnAsk != want {
			t.Errorf("%s: answer on ask %v, %v", settings, c.answerOnAsk, p)
		}
	}
}
