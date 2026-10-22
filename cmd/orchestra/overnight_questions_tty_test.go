//go:build darwin || linux

package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// In a terminal, an overnight run's pre-flight opens the answer talk on the open questions before the
// run starts, and says those the maintainer left open with the tickets they keep waiting.
func TestOvernightOnATerminalAsksEveryQuestionBeforeTheRunStarts(t *testing.T) {
	t.Parallel()
	preflightWith(t, passing().check)
	dir := nothingTools(t, backlog{})
	f := talkAnswering(t, dir, "k-7")
	term, repo, exit := runOnTerminal(t, "--overnight")
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	if want := []string{"Let's answer the 2 open questions in @.orchestra/run/answer-1/questions.md, one at a time."}; !slices.Equal(
		f.talks, want) {
		t.Errorf("the talks: %q, want %q", f.talks, want)
	}
	if !slices.Equal(f.started, []bool{false}) {
		t.Errorf("the run had started by the talk: %v", f.started)
	}
	out := screenOf(term)
	for _, want := range []string{"◆ Answered 1 question", "◆ Overnight pre-flight: 1 question left open",
		"! k-9 (Which flag name?): k-5 waits on it.", "✓ All done"} {
		if !strings.Contains(out, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "k-7 (") {
		t.Errorf("the terminal lists k-7, answered, as left open:\n%s", out)
	}
	if rec := startRecord(t, repo); rec["overnight"] != true {
		t.Errorf("the start record doesn't say overnight: %v", rec)
	}
}
