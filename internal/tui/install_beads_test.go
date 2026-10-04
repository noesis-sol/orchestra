package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
)

func TestInitFormAsksToInstallBeadsOfferingYes(t *testing.T) {
	const left, enter = "\x1b[D", "\r"
	for keys, want := range map[string]bool{enter: true, "n": false, left + enter: false} {
		c := project.Choice{InstallBeads: true,
			Install: project.BeadsInstall{Method: "Homebrew", Command: "brew install beads"}}
		term := askOn(t, func(in io.Reader, out io.Writer) error {
			return AskInit(in, out, &c, Ask{Install: true})
		})
		term.waitFor(t, "┃ Install Beads with Homebrew?")
		term.typeKeys(t, keys)
		if err := term.end(t); err != nil {
			t.Fatalf("%q: %v", keys, err)
		}
		if c.InstallBeads != want {
			t.Errorf("%q: install = %v, want %v", keys, c.InstallBeads, want)
		}
	}
}

func TestInstallFieldNamesTheMethodAndCommandWithYesSelected(t *testing.T) {
	c := project.Choice{InstallBeads: true, Install: project.FindBeadsInstall("freebsd")}
	view := ansi.Strip(installField(&c).WithTheme(huh.ThemeBase()).View())
	for _, want := range []string{"Install Beads with the Beads install script?", c.Install.Command, "Install it"} {
		if !strings.Contains(strings.Join(strings.Fields(view), " "), want) {
			t.Errorf("the field lacks %q:\n%s", want, view)
		}
	}
}
