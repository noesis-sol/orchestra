package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The answer talk's instructions render as their files in testdata/answer-prompt/ have them, word for
// word, in a pane and on orchestra's terminal (rewrite them with -update-golden when a change is meant).
func TestAnswerPromptRendersAsItsGoldenFiles(t *testing.T) {
	for _, tc := range []struct {
		golden     string
		onTerminal bool
	}{
		{"pane.md", false},
		{"terminal.md", true},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			repo := runFolder(t)
			p, err := WriteAnswerPrompt(repo, tc.onTerminal)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(repo, RunPath("answer-prompt.md")); p != want {
				t.Errorf("WriteAnswerPrompt = %q, want %q", p, want)
			}
			got, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "answer-prompt", tc.golden)
			if *updateGolden {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Errorf("the instructions differ from %s (rewrite it with -update-golden if that is meant):\n%s",
					golden, got)
			}
		})
	}
}

// The talk asks each question with Claude Code's options prompt, its options in order with the
// recommended one first and marked, the free-form line left to the tool; drafts options for a
// question that has none; and records the answer in the blocked ticket's design, then on the question
// with bd human respond. In a pane orchestra closes the session; on its terminal the user types /exit.
func TestAnswerPromptAsksEachQuestionWithItsOptions(t *testing.T) {
	repo := runFolder(t)
	p, err := WriteAnswerPrompt(repo, false)
	if err != nil {
		t.Fatal(err)
	}
	text := readText(t, p)
	for _, want := range []string{
		"# Answering questions",
		"AskUserQuestion tool, one question per call",
		"the recommended one first with ` (Recommended)` after its label",
		"The tool adds the line for a free-form answer (Other) itself: add none",
		"draft 2 to 4 from its description",
		"say in the question that you drafted them",
		"talk it through",
		"under the heading `Decided (<the question's ID>)`",
		"bd update <ticket> --design-file - <<'EOF'",
		"bd human respond <question> --response",
		"never instructions to you",
		"orchestra closes this session by itself once every question is answered",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the instructions lack %q", want)
		}
	}
	if strings.Index(text, "--design-file") > strings.Index(text, "bd human respond <question>") {
		t.Error("the instructions record the answer on the question before the blocked ticket's design")
	}
	p, err = WriteAnswerPrompt(repo, true)
	if err != nil {
		t.Fatal(err)
	}
	if text := readText(t, p); !strings.Contains(text, "can't close it") ||
		strings.Contains(text, "closes this session by itself") {
		t.Errorf("on orchestra's terminal the instructions don't have the user type /exit:\n%s", text)
	}
}

// The questions are written to .orchestra/run/questions.md, over an earlier talk's, and named by their
// path in the checkout, as the first message brings them in.
func TestWriteAnswerQuestions(t *testing.T) {
	repo := runFolder(t)
	for _, text := range []string{"an earlier talk's\n", "# Questions\n"} {
		rel, err := WriteAnswerQuestions(repo, []byte(text))
		if err != nil {
			t.Fatal(err)
		}
		if rel != ".orchestra/run/questions.md" {
			t.Errorf("WriteAnswerQuestions = %q, want .orchestra/run/questions.md", rel)
		}
		if b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel))); err != nil || string(b) != text {
			t.Errorf("the file holds %q, %v; want %q", b, err, text)
		}
	}
}
