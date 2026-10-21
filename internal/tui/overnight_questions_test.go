package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The questions an overnight run's pre-flight leaves open, each a warning under a heading that counts them.

func TestQuestionsLeftCountsThemAndGivesEachAsAWarning(t *testing.T) {
	questions := []string{"k-7 left open (Which format?): k-1.2 waits on it", "k-9 left open (Where?): k-5 waits on it"}
	var plain strings.Builder
	Printer{Out: &plain}.QuestionsLeft(questions)
	want := "Overnight pre-flight: 2 questions left open\n" +
		"  warning: k-7 left open (Which format?): k-1.2 waits on it\n" +
		"  warning: k-9 left open (Where?): k-5 waits on it\n"
	if got := stamped(plain.String()); got != want {
		t.Errorf("plain:\n%s\nwant:\n%s", got, want)
	}
	var styled strings.Builder
	Printer{Out: &styled, Styled: true, Width: 100}.QuestionsLeft(questions[:1])
	for _, line := range []string{"◆ Overnight pre-flight: 1 question left open", "  ! k-7 left open"} {
		if !strings.Contains(ansi.Strip(styled.String()), line) {
			t.Errorf("styled lacks %q:\n%s", line, ansi.Strip(styled.String()))
		}
	}
}
