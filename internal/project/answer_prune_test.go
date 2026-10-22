package project

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// A new answer talk prunes the folders of talks whose folder and files were all last written over a day
// ago, and keeps those of talks written since, where claude may still be reading its questions, and
// anything in the run folder that isn't a talk's folder. It is numbered past the folders it prunes.
func TestNewAnswerTalkPrunesOldTalksFolders(t *testing.T) {
	repo := runFolder(t)
	old := time.Now().Add(-AnswerFolderAge - time.Hour)
	write := func(rel string, when time.Time) {
		t.Helper()
		p := filepath.Join(repo, RunPath(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("k-7\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	age := func(folder string, when time.Time) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(repo, RunPath(folder)), when, when); err != nil {
			t.Fatal(err)
		}
	}
	write("answer-1/questions.md", old) // a talk a day gone
	age("answer-1", old)
	write("answer-2/questions.md", old) // a talk open a day, its latest batch queued since
	write("answer-2/questions-2.md", time.Now())
	age("answer-2", old)
	write("answer-3/questions.md", old) // a folder written since, its files old
	write("answer-x/questions.md", old) // not a talk's folder
	age("answer-x", old)
	write("answer-9", old)                                                            // a file, not a folder
	if err := os.Mkdir(filepath.Join(repo, RunPath("answer-4")), 0o755); err != nil { // a talk just begun
		t.Fatal(err)
	}
	age("answer-3", time.Now())

	if f := newAnswerFiles(t, repo); f.folder != "answer-10" {
		t.Errorf("the new talk has the folder %q, want answer-10", f.folder)
	}
	entries, err := os.ReadDir(filepath.Join(repo, RunPath("")))
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if want := []string{"answer-10", "answer-2", "answer-3", "answer-4", "answer-9", "answer-x"}; !slices.Equal(left,
		want) {
		t.Errorf("the run folder holds %q, want %q", left, want)
	}
}
