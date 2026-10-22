package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// readLoops counts the goroutines of Bubble Tea programs still reading their input.
func readLoops() int {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return strings.Count(string(buf[:n]), "bubbletea.(*Program).readLoop(")
		}
		buf = make([]byte, 2*len(buf))
	}
}

// The start question and the preflight form quit when their context ends, as the init form does,
// rather than being killed by it: a killed program returns before its goroutine reading the terminal
// has ended, and that goroutine then reads the terminal's file as it closes, a data race. Their input
// is a pipe, a file Bubble Tea can stop reading, as it can a terminal. On one processor a killed
// program's input goroutine, woken by its cancel, waits for the processor until after the form has
// returned and the test looks; serial, as it sets the process's GOMAXPROCS.
func TestFormsQuitWhenTheirContextEnds(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	for _, tc := range []struct {
		name, on string
		ask      func(ctx context.Context, in io.Reader, out io.Writer) error
	}{
		{"start question", "What should this run work on?", func(ctx context.Context, in io.Reader, out io.Writer) error {
			_, err := AskWork(ctx, in, out, ttyTerm, t.TempDir(), 3, nil)
			return err
		}},
		{"preflight", "Run without a limit?", func(ctx context.Context, in io.Reader, out io.Writer) error {
			_, err := AskPreflight(ctx, in, out, ttyTerm, []PreflightCheck{limitSet}, false)
			return err
		}},
	} {
		in, keys, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		before := readLoops()
		ctx, cancel := context.WithCancel(t.Context())
		term := &formTerminal{keys: keys, done: make(chan error, 1)}
		go func() { term.done <- tc.ask(ctx, in, &term.screen) }()
		term.waitFor(t, tc.on)
		cancel()
		err = term.end(t)
		if n := readLoops(); n > before {
			t.Errorf("%s: %d of Bubble Tea's input goroutines still read once the form returned", tc.name, n-before)
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("%s: the form returned %v; want %v", tc.name, err, context.Canceled)
		}
		_ = keys.Close()
		_ = in.Close()
	}
}

// idleModel is a program that waits until it is quit.
type idleModel struct{}

func (idleModel) Init() tea.Cmd                         { return nil }
func (m idleModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (idleModel) View() string                          { return "waiting" }

// runProgram, which the forms run through, quits a program when its context ends: Run returns no
// error, where a program killed by its context returns tea.ErrProgramKilled, and its input goroutine
// has ended.
func TestRunProgramQuitsWhenItsContextEnds(t *testing.T) {
	in, keys, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keys.Close(); _ = in.Close() }()
	before := readLoops()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := runProgram(ctx, idleModel{}, in, io.Discard); err != nil {
		t.Errorf("runProgram = %v; want the program quit, with no error", err)
	}
	if n := readLoops(); n > before {
		t.Errorf("%d of Bubble Tea's input goroutines still read once runProgram returned", n-before)
	}
}
