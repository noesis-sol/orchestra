package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Keys as a terminal sends them.
const (
	keyDown    = "\x1b[B"
	keyEnter   = "\r"
	keyNewLine = "\n" // Ctrl+J
	keyEsc     = "\x1b"
	keyCtrlC   = "\x03"
)

// fakeReady is the run's ready query: n tickets, or err.
type fakeReady struct {
	n   int
	err error
}

func (f fakeReady) Ready(context.Context, string) ([]dispatch.Ticket, error) {
	return make([]dispatch.Ticket, f.n), f.err
}

// screen is what the form draws, written from Bubble Tea's goroutines and read from the test's.
type screen struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *screen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// String is what was drawn, without its colours.
func (s *screen) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ansi.Strip(s.b.String())
}

// workAnswer is what the question returned, and what it drew and said.
type workAnswer struct {
	description string
	code        int
	out, err    string
}

// fakeTerminal is where the question is asked in a test: the screen it draws on, and the keys typed
// at it, as someone would, once the screen shows what they answer.
type fakeTerminal struct {
	screen screen
	keys   io.Writer
	end    func() // ends the input; nil to leave it open
}

// waitFor waits until the screen shows text.
func (term *fakeTerminal) waitFor(t *testing.T, text string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(term.screen.String(), text); {
		if time.Now().After(deadline) {
			t.Fatalf("the screen never showed %q:\n%s", text, term.screen.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// typeKeys types keys; with last, nothing more is typed after them.
func (term *fakeTerminal) typeKeys(t *testing.T, keys string, last bool) {
	t.Helper()
	if _, err := term.keys.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
	if last && term.end != nil {
		term.end()
	}
}

// askWorkOn asks the question with ready tickets on a fake terminal, and returns the terminal and
// the answer, which waits for the form to end.
func askWorkOn(ctx context.Context, t *testing.T, ready fakeReady) (*fakeTerminal, func() workAnswer) {
	t.Helper()
	in, keys := io.Pipe()
	end := func() { _ = keys.Close() } // only ends the input
	t.Cleanup(end)
	term := &fakeTerminal{keys: keys, end: end}
	var errOut strings.Builder
	done := make(chan workAnswer, 1)
	go func() {
		d, code := workQuestion{tickets: ready, in: in, out: &term.screen, err: &errOut}.ask(ctx)
		done <- workAnswer{description: d, code: code}
	}()
	return term, func() workAnswer {
		t.Helper()
		select {
		case a := <-done:
			a.out, a.err = term.screen.String(), errOut.String()
			return a
		case <-time.After(10 * time.Second):
			t.Fatalf("the question didn't end; the screen:\n%s", term.screen.String())
			return workAnswer{}
		}
	}
}

// The text field draws the bar on its left only while it has the focus, and keys typed before then
// would go to the choice.
const describing = "┃ Describe the feature"

func TestWorkQuestionRunsTheCurrentTicketsByDefault(t *testing.T) {
	term, answer := askWorkOn(context.Background(), t, fakeReady{n: 3})
	term.waitFor(t, "> Current tickets: 3 ready")
	term.typeKeys(t, keyEnter, true)
	a := answer()
	if a.description != "" || a.code != dispatch.ExitOK || a.err != "" {
		t.Errorf("got %q, exit %d, stderr %q", a.description, a.code, a.err)
	}
	for _, want := range []string{"What should this run work on?", "orchestra --tickets runs the current tickets",
		"New feature: describe it, talk it through with claude, run its tickets"} {
		if !strings.Contains(a.out, want) {
			t.Errorf("the form lacks %q:\n%s", want, a.out)
		}
	}
	if strings.Contains(a.out, "Describe the feature") {
		t.Errorf("the description was asked for:\n%s", a.out)
	}
}

func TestWorkQuestionCountsNothingWhenBdCantSay(t *testing.T) {
	term, answer := askWorkOn(context.Background(), t, fakeReady{err: errors.New("bd: database is locked")})
	term.waitFor(t, "> Current tickets")
	term.typeKeys(t, keyEnter, true)
	a := answer()
	if a.description != "" || a.code != dispatch.ExitOK {
		t.Errorf("got %q, exit %d", a.description, a.code)
	}
	if strings.Contains(a.out, "Current tickets:") {
		t.Errorf("want the option without a count:\n%s", a.out)
	}
}

func TestWorkQuestionTakesAMultiLineFeatureDescription(t *testing.T) {
	term, answer := askWorkOn(context.Background(), t, fakeReady{n: 3})
	term.waitFor(t, "> Current tickets: 3 ready")
	term.typeKeys(t, keyDown+keyEnter, false)
	term.waitFor(t, describing)
	term.typeKeys(t, "Add a --json flag"+keyNewLine+"to the list command  "+keyEnter, true)
	a := answer()
	if want := "Add a --json flag\nto the list command"; a.description != want || a.code != dispatch.ExitOK {
		t.Errorf("got %q, exit %d, want %q", a.description, a.code, want)
	}
	// Echoed once the form is gone, to run again should it not be planned.
	if want := "New feature:\n  Add a --json flag\n  to the list command\n"; !strings.HasSuffix(a.out, want) {
		t.Errorf("the request isn't echoed after the form:\n%s", a.out)
	}
}

// Enter on nothing, or on spaces, says why and keeps the field open.
func TestWorkQuestionRefusesAnEmptyDescription(t *testing.T) {
	const refused = "describe the feature first, or press Esc to cancel"
	for _, empty := range []string{"", "  " + keyNewLine} {
		term, answer := askWorkOn(context.Background(), t, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, keyDown+keyEnter, false)
		term.waitFor(t, describing)
		term.typeKeys(t, empty+keyEnter, false)
		term.waitFor(t, refused)
		term.typeKeys(t, "Add a --json flag"+keyEnter, true)
		if a := answer(); a.description != "Add a --json flag" || a.code != dispatch.ExitOK {
			t.Errorf("after %q: got %q, exit %d", empty, a.description, a.code)
		}
	}
}

// Ctrl+C or Esc, at the choice or at the description, ends the run before anything changes; so does
// a stop signal, which ends ctx.
func TestWorkQuestionCancelledExits130(t *testing.T) {
	const stopped = "orchestra: stopped before the run started; nothing was changed.\n"
	for _, cancelKey := range []string{keyEsc, keyCtrlC} {
		term, answer := askWorkOn(context.Background(), t, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, cancelKey, true)
		if a := answer(); a.description != "" || a.code != dispatch.ExitInterrupted || a.err != stopped {
			t.Errorf("%q at the choice: got %q, exit %d, stderr %q", cancelKey, a.description, a.code, a.err)
		}

		term, answer = askWorkOn(context.Background(), t, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, keyDown+keyEnter, false)
		term.waitFor(t, describing)
		term.typeKeys(t, "Add a fl", false)
		term.waitFor(t, "Add a fl")
		term.typeKeys(t, cancelKey, true)
		if a := answer(); a.description != "" || a.code != dispatch.ExitInterrupted || a.err != stopped {
			t.Errorf("%q at the description: got %q, exit %d, stderr %q", cancelKey, a.description, a.code, a.err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	term, answer := askWorkOn(ctx, t, fakeReady{n: 3})
	term.waitFor(t, "> Current tickets: 3 ready")
	cancel()
	if a := answer(); a.description != "" || a.code != dispatch.ExitInterrupted || a.err != stopped {
		t.Errorf("a stop signal: got %q, exit %d, stderr %q", a.description, a.code, a.err)
	}
}

func TestAsksWorkOnlyWhenNothingSaysWhatToRun(t *testing.T) {
	asked := options{}
	asked.Limit = 40
	for _, tc := range []struct {
		name     string
		change   func(c *options)
		terminal bool
		want     bool
	}{
		{"a terminal", func(*options) {}, true, true},
		{"no terminal", func(*options) {}, false, false},
		{"--feature", func(c *options) { c.Feature = "Add a flag" }, true, false},
		{"--ticket", func(c *options) { c.Ticket = "orchestra-1" }, true, false},
		{"--tickets", func(c *options) { c.Tickets = true }, true, false},
		{"-plain", func(c *options) { c.Plain = true }, true, false},
		{"-limit reached", func(c *options) { c.DoneSoFar = 40 }, true, false},
		{"-limit 0", func(c *options) { c.Limit = 0 }, true, false},
	} {
		c := asked
		tc.change(&c)
		if got := asksWork(c, tc.terminal); got != tc.want {
			t.Errorf("%s: asks = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTicketsFlagSkipsTheQuestion(t *testing.T) {
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want bool
	}{
		{"none", nil, nil, false},
		{"--tickets", []string{"--tickets"}, nil, true},
		{"ORCHESTRA_TICKETS=1", nil, map[string]string{"ORCHESTRA_TICKETS": "1"}, true},
		{"ORCHESTRA_TICKETS=0", nil, map[string]string{"ORCHESTRA_TICKETS": "0"}, false},
		{"--tickets=false over the variable", []string{"--tickets=false"}, map[string]string{"ORCHESTRA_TICKETS": "1"}, false},
	} {
		c, _, problems, err := readFlags(tc.args, env(tc.env), io.Discard)
		if err != nil || len(problems) > 0 || c.Tickets != tc.want {
			t.Errorf("%s: tickets = %v, want %v (%v, %v)", tc.name, c.Tickets, tc.want, err, problems)
		}
	}

	_, _, problems, err := readFlags([]string{"--feature", "Add a flag", "--tickets"}, env(nil), io.Discard)
	if err != nil || len(problems) != 1 || !strings.Contains(problems[0], "--feature can't be combined with --tickets") {
		t.Errorf("--feature with --tickets: %v, %q", err, problems)
	}
	// The variable is a default, which --feature overrides.
	c, _, problems, err := readFlags([]string{"--feature", "Add a flag"}, env(map[string]string{"ORCHESTRA_TICKETS": "1"}),
		io.Discard)
	if err != nil || len(problems) > 0 || c.Feature != "Add a flag" {
		t.Errorf("--feature with ORCHESTRA_TICKETS=1: %v, %q", err, problems)
	}
}
