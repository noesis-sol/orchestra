package project

import (
	"os"
	"path/filepath"
	"testing"
)

// Each answer talk has a folder of its own, one past the highest there, whatever else the run folder
// holds: two talks open at once write their questions and instructions apart, neither over the other's.
func TestAnswerTalksOpenAtOnceKeepTheirFilesApart(t *testing.T) {
	repo := runFolder(t)
	for _, name := range []string{"answer-prompt.md", "questions.md", "answer-x", "answer-"} {
		if err := os.WriteFile(filepath.Join(repo, RunPath(name)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, second := newAnswerFiles(t, repo), newAnswerFiles(t, repo)
	for i, f := range []AnswerFiles{first, second} {
		if want := []string{"answer-1", "answer-2"}[i]; f.folder != want {
			t.Errorf("talk %d has the folder %q, want %q", i+1, f.folder, want)
		}
	}
	firstQuestions, err := first.WriteQuestions([]byte("k-7\n"))
	if err != nil {
		t.Fatal(err)
	}
	firstPrompt, err := first.WritePrompt(false)
	if err != nil {
		t.Fatal(err)
	}
	secondQuestions, err := second.WriteQuestions([]byte("k-9\n"))
	if err != nil {
		t.Fatal(err)
	}
	secondPrompt, err := second.WritePrompt(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.WriteMore(2, []byte("k-10\n")); err != nil {
		t.Fatal(err)
	}
	if firstQuestions == secondQuestions || firstPrompt == secondPrompt {
		t.Fatalf("the talks share files: %s and %s, %s and %s", firstQuestions, secondQuestions, firstPrompt,
			secondPrompt)
	}
	for file, want := range map[string]string{firstQuestions: "k-7\n", secondQuestions: "k-9\n",
		".orchestra/run/answer-2/questions-2.md": "k-10\n"} {
		if b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file))); err != nil || string(b) != want {
			t.Errorf("%s holds %q, %v; want %q", file, b, err, want)
		}
	}
	if readText(t, firstPrompt) == readText(t, secondPrompt) {
		t.Error("the talk in a pane has the instructions of the one on orchestra's terminal")
	}
	if third := newAnswerFiles(t, repo); third.folder != "answer-3" {
		t.Errorf("a third talk has the folder %q, want answer-3", third.folder)
	}
}
