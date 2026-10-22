package tui

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Tests from scratch with check-fast.sh kept: the check-full.sh that runs it is what init would
// write, and isn't asked about; with check-fast.sh replaced, check-full.sh would check nothing, and
// is asked about.
func TestCheckFullBesideACheckFastKeptFromScratch(t *testing.T) {
	old := project.FastScript([]project.Suite{{Command: "make old"}})
	full := project.FullScript(nil, false)
	for _, tc := range []struct {
		keys      string
		askedFull bool
	}{{"\r", false}, {down + "\r", true}} {
		c := project.Choice{}
		ask := Ask{Questions: project.Questions{Check: true}, Scout: finds(organ.Scouting{Suites: []organ.Suite{}}, nil),
			Runners: []project.Runner{{Path: project.FastRunner, Script: old, Exists: true},
				{Path: project.FullRunner, Script: full, Exists: true}}}
		term := askChecks(t, &c, ask)
		steps := []keysOn{{onScratch, "\r"},
			{"┃ scripts/check-fast.sh is there, and differs from the checks chosen", ""}, {"make old", tc.keys}}
		if tc.askedFull {
			steps = append(steps, keysOn{"┃ scripts/check-full.sh is there, and differs from the checks chosen", "\r"})
		}
		term.typeSteps(t, steps)
		if err := term.end(t); err != nil {
			t.Fatal(err)
		}
		asked := strings.Contains(term.screen.String(), "check-full.sh is there, and differs")
		if asked != tc.askedFull || c.ReplaceFull {
			t.Errorf("%q: asked about check-full %v, replaced %v; want %v, false", tc.keys, asked, c.ReplaceFull,
				tc.askedFull)
		}
	}
}
