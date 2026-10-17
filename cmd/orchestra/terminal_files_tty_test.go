//go:build darwin || linux

package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// sizeThenQuit asks for the terminal's size and quits right after: the goroutine Bubble Tea starts to
// read the size (checkResize) may still be reading the terminal's descriptor as Run returns.
type sizeThenQuit struct{}

func (sizeThenQuit) Init() tea.Cmd                         { return tea.Sequence(tea.WindowSize(), tea.Quit) }
func (m sizeThenQuit) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (sizeThenQuit) View() string                          { return "" }

// A terminal closed as soon as a program on it has returned, as each terminal test's is as it ends,
// races none of the goroutines Bubble Tea leaves reading its descriptor: go test -race reported
// checkResize's Fd against the terminal's Close in the question's terminal tests (orchestra-fze2).
func TestTerminalClosedAfterAProgramRacesNothing(t *testing.T) {
	for range 20 {
		tty, _ := openTerminal(t)
		p := tea.NewProgram(sizeThenQuit{}, tui.WithTerminal(tty, tty), tea.WithoutSignalHandler())
		if _, err := p.Run(); err != nil {
			t.Fatal(err)
		}
		if err := tty.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
