package tui

import (
	"testing"
)

// Plain output with no plain line prints nothing, for a caller that prints its own; the zero
// Printer, with no output at all, is one.
func TestBusyStepWithNoPlainLinePrintsNothing(t *testing.T) {
	busy := Printer{}.Busy("", "Screening the request with Claude…", "")
	busy.Done("Request screened")
}
