package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The Checks step offers the setup command found for the project's lockfiles, which Enter takes and
// which can be changed or emptied.
func TestInitFormOffersTheSetupCommandForTheLockfiles(t *testing.T) {
	const onSetup = "┃ Setup command"
	for _, tc := range []struct {
		name, keys, want string
	}{
		{"taken", "\r", "npm ci"},
		{"changed", "\x15  npm install  \r", "npm install"}, // Ctrl+U empties the line
		{"emptied", "\x15\r", ""},
	} {
		c := project.Choice{SetupOffer: "npm ci", SetupFrom: []string{"package-lock.json"}}
		term := askOn(t, func(in io.Reader, out io.Writer) error { return AskInit(in, out, &c, Ask{Setup: true}) })
		term.waitFor(t, "Found package-lock.json.")
		term.typeSteps(t, []keysOn{{onSetup, tc.keys}})
		if err := term.end(t); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if c.Setup != tc.want {
			t.Errorf("%s: setup = %q, want %q", tc.name, c.Setup, tc.want)
		}
		if screen := term.screen.String(); !strings.Contains(screen, "Checks") || strings.Contains(screen, "Step ") {
			t.Errorf("%s: want the Checks header alone:\n%s", tc.name, screen)
		}
	}
}
