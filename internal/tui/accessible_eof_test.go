package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
	"go.uber.org/goleak"
)

// In huh's accessible form (TERM=dumb), the end of input (Ctrl+D) cancels the form, as Esc does in
// the terminal's, rather than taking each remaining question's default answer; and ctx ending stops
// it at once.

// typedThenEnd is the answers to an accessible form, then the end of input with no line after them.
func typedThenEnd(lines ...string) io.Reader {
	if len(lines) == 0 {
		return strings.NewReader("")
	}
	return typed(lines...)
}

func TestAccessibleWorkFormCancelsAtTheEndOfInput(t *testing.T) {
	t.Setenv("TERM", "dumb")
	for _, tc := range []struct {
		name    string
		answers []string
	}{
		{"at the first question", nil},
		{"at the description", []string{"2"}},
		{"after an empty description", []string{"2", ""}},
	} {
		var out strings.Builder
		got, err := AskWork(context.Background(), typedThenEnd(tc.answers...), &out, 3, nil)
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("%s: AskWork = %q, %v; want %v\n%s", tc.name, got, err, ErrCancelled, ansi.Strip(out.String()))
		}
	}
}

func TestAccessibleInitFormCancelsAtTheEndOfInput(t *testing.T) {
	t.Setenv("TERM", "dumb")
	for _, tc := range []struct {
		name    string
		answers []string // install Beads, tickets at the same time, union
	}{
		{"at the first question", nil},
		{"at the second", []string{"y"}},
		{"at the last", []string{"y", "2"}},
	} {
		c := project.Choice{Concurrent: 1, InstallBeads: true, Union: true,
			Install: project.BeadsInstall{Method: "Homebrew", Command: "brew install beads"}}
		var out strings.Builder
		err := AskInit(typedThenEnd(tc.answers...), &out, &c, Ask{Install: true, Concurrent: true, Union: true})
		if !errors.Is(err, huh.ErrUserAborted) {
			t.Errorf("%s: AskInit = %v; want %v\n%s", tc.name, err, huh.ErrUserAborted, ansi.Strip(out.String()))
		}
	}
}

func TestAccessibleInitFormStillTakesEveryAnswer(t *testing.T) {
	t.Setenv("TERM", "dumb")
	c := project.Choice{Concurrent: 1, InstallBeads: true, Union: true,
		Install: project.BeadsInstall{Method: "Homebrew", Command: "brew install beads"}}
	var out strings.Builder
	err := AskInit(typed("n", "3", "n"), &out, &c, Ask{Install: true, Concurrent: true, Union: true})
	if err != nil || c.InstallBeads || c.Concurrent != 3 || c.Union {
		t.Errorf("AskInit: %v, install %v, concurrent %d, union %v; want nil, false, 3, false\n%s",
			err, c.InstallBeads, c.Concurrent, c.Union, ansi.Strip(out.String()))
	}
}

func TestAccessibleWorkFormStopsWhenCtxEnds(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	t.Setenv("TERM", "dumb")
	in, typing := io.Pipe() // nothing typed: the form waits for its first answer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := AskWork(ctx, in, io.Discard, 3, nil)
		done <- err
	}()
	time.Sleep(10 * time.Millisecond) // the form is reading by now, or reads an ended input
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("AskWork = %v, want %v", err, context.Canceled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskWork still waits for an answer after ctx ended")
	}
	_ = typing.Close() // ends the read left waiting on in
}

func TestFormInputKeepsTheAnswersInOrder(t *testing.T) {
	got, err := io.ReadAll(&formInput{ctx: t.Context(), in: iotest.HalfReader(strings.NewReader("1\n2\nthree\n"))})
	if err != nil || string(got) != "1\n2\nthree\n" {
		t.Errorf("read %q, %v; want the input as it is", got, err)
	}
}
