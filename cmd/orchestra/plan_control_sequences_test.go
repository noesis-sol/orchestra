package main

import (
	"strings"
	"testing"
	"unicode"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// A plan's titles, files and notes are shown without the escape sequences an organ could put in
// them (a clipboard write, a cleared screen, a window title), and the plan filed keeps its text.
func TestShowPlanDropsControlSequences(t *testing.T) {
	osc52, csi, osc0 := "\x1b]52;c;cm0gLXJmIH4K\x07", "\x1b[2J", "\x1b]0;pwned\x07"
	p := organ.FeaturePlan{
		Epic: organ.PlannedEpic{Title: "JSON" + osc52 + " output", Description: "Machine" + csi + "-readable.\nFor\r scripts."},
		Tickets: []organ.PlannedTicket{
			{Key: "t1" + osc0, Title: "Add the" + osc52 + " encoder", Type: "feature", Priority: 1, Files: []string{"enc.go" + csi}},
			{Key: "t2", Title: "Add the flag", Type: "task", Priority: 2, BlockedBy: []string{"t1" + osc0}},
		},
		Notes: []string{"dropped " + osc0 + "x.go"},
	}
	var b strings.Builder
	showPlan(&b, p)
	for _, r := range b.String() {
		if r != '\n' && unicode.IsControl(r) {
			t.Fatalf("%q came through:\n%q", r, b.String())
		}
	}
	for _, want := range []string{"Epic: JSON output", "Machine-readable.", "t1  feature P1  Add the encoder", "files: enc.go",
		"after: t1", "dropped x.go"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the plan should show %q:\n%s", want, b.String())
		}
	}
	if p.Tickets[0].Title != "Add the"+osc52+" encoder" {
		t.Errorf("showing the plan changed it: %q", p.Tickets[0].Title)
	}
}
