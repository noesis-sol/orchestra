package tui

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// syncBuffer is what a form draws, written from Bubble Tea's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	return s.since(0)
}

// len is how much has been drawn: a mark for since.
func (s *syncBuffer) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// since is what was drawn after the mark len gave. Each write is a whole frame or more of the
// renderer's, so a mark never splits an escape sequence.
func (s *syncBuffer) since(mark int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.Strip(s.b.String()[mark:])
}

// formTerminal is where a form is asked in a test: the screen it draws on, and the keys typed at it,
// as someone would, once the screen shows what they answer. Keys typed all at once race the form:
// huh moves the focus on a later message, so the keys after an Enter can reach the field it left.
type formTerminal struct {
	screen syncBuffer
	typed  int // the screen's mark when keys were last typed
	keys   io.Writer
	done   chan error
}

// keysOn are keys typed once the screen shows on: text that only the keys before them bring up, such
// as a field's title beside the bar huh draws on the left of the focused field.
type keysOn struct{ on, keys string }

// askOn runs ask, which asks with a form reading in and drawing on out, on a fake terminal.
func askOn(t *testing.T, ask func(in io.Reader, out io.Writer) error) *formTerminal {
	t.Helper()
	in, keys := io.Pipe()
	t.Cleanup(func() { _ = keys.Close() }) // ends the input should the form not end
	term := &formTerminal{keys: keys, done: make(chan error, 1)}
	go func() {
		err := ask(in, &term.screen)
		_ = in.Close() // keys typed after the form ended fail instead of waiting for a reader
		term.done <- err
	}()
	return term
}

// waitFor waits until the screen shows text, drawn since keys were last typed: text the screen
// showed before them, such as a field's title it showed until they moved the focus away, is only
// there again once they have taken effect and the form drew it anew. Bubble Tea redraws only the
// lines that change, so the text waited for must be on a line the keys change.
func (term *formTerminal) waitFor(t *testing.T, text string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(term.screen.since(term.typed), text); {
		if time.Now().After(deadline) {
			t.Fatalf("the screen never showed %q:\n%s", text, term.screen.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// typeKeys types keys; none, as a step that only waits has, leaves what waitFor looks at as it was.
func (term *formTerminal) typeKeys(t *testing.T, keys string) {
	t.Helper()
	if keys == "" {
		return
	}
	term.typed = term.screen.len()
	if _, err := term.keys.Write([]byte(keys)); err != nil {
		t.Fatalf("typing %q: %v; the screen:\n%s", keys, err, term.screen.String())
	}
}

// typeSteps types each step's keys once the screen shows its text.
func (term *formTerminal) typeSteps(t *testing.T, steps []keysOn) {
	t.Helper()
	for _, s := range steps {
		term.waitFor(t, s.on)
		term.typeKeys(t, s.keys)
	}
}

// end waits for the form to end and returns what ask returned.
func (term *formTerminal) end(t *testing.T) error {
	t.Helper()
	select {
	case err := <-term.done:
		return err
	case <-time.After(10 * time.Second):
		t.Fatalf("the form didn't end; the screen:\n%s", term.screen.String())
		return nil
	}
}
