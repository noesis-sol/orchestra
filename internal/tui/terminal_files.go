package tui

import (
	"io"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// WithTerminal has a Bubble Tea program read in and draw on out, as tea.WithInput and
// tea.WithOutput do, but gives it each file with its descriptor read once, now (see heldFile).
func WithTerminal(in io.Reader, out io.Writer) tea.ProgramOption {
	if f, ok := in.(*os.File); ok {
		in = heldFile{File: f, fd: f.Fd()}
	}
	if f, ok := out.(*os.File); ok {
		out = heldFile{File: f, fd: f.Fd()}
	}
	return func(p *tea.Program) {
		tea.WithInput(in)(p)
		tea.WithOutput(out)(p)
	}
}

// heldFile is a terminal's file whose Fd gives the descriptor read when it was made. Bubble Tea
// reads the descriptor from goroutines it doesn't wait for: checkResize, which tea.WindowSize starts
// (huh's forms ask it), and the input's read loop, which outlives a program ended by its context.
// os.File's Fd reads a field that the file's Close writes, so one of them still running as the file
// closes, once Run has returned, is a data race; this Fd reads only a field set before they start.
// What it gives them then is a closed descriptor, which their system calls fail on.
type heldFile struct {
	*os.File
	fd uintptr
}

// Fd returns the descriptor the file had when it was given to Bubble Tea.
func (f heldFile) Fd() uintptr { return f.fd }
