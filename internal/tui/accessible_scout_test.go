package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// huh's accessible form focuses each field before it runs it. Focusing the scout's field doesn't
// start the scout there, so a scout that ends at once can't hide its field first: the field still
// says what it found.
func TestAccessibleScoutThatEndsAtOnceSaysWhatItFound(t *testing.T) {
	failed := &organ.ScoutError{Failure: organ.ScoutNoClaude, Err: errors.New("claude not found")}
	stage := &initStage{}
	s := addChecks(stage, &project.Choice{}, Ask{Check: true, Scout: finds(organ.Scouting{}, failed)}, true)
	field := stage.fields[0]
	_ = field.Focus()
	if s.scout.started {
		t.Fatal("focusing the scout's field in the accessible form started the scout")
	}
	var out strings.Builder
	if err := field.RunAccessible(&out, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "The scout can't run: claude not found.") {
		t.Errorf("the scout's field said:\n%s", out.String())
	}
}
