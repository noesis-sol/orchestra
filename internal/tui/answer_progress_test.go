package tui

import (
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The answer talk's line says how many of its questions bd has closed, once it has said.
func TestInterviewProgressLineCountsTheAnswers(t *testing.T) {
	for _, tc := range []struct {
		p    InterviewProgress
		want string
	}{
		{InterviewProgress{Agent: dispatch.StateIdle, Elapsed: time.Minute}, "claude is waiting for you  1m0s"},
		{InterviewProgress{Agent: dispatch.StateIdle, Elapsed: time.Minute, Questions: 3, Answered: 1},
			"claude is waiting for you  1m0s · 1 of 3 questions answered"},
		{InterviewProgress{Agent: dispatch.StateWorking, Elapsed: time.Second, Questions: 1},
			"claude is working  1s · 0 of 1 question answered"},
	} {
		if got := ansi.Strip(tc.p.Line(80)); got != tc.want {
			t.Errorf("line = %q, want %q", got, tc.want)
		}
	}
}
