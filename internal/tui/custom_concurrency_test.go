package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/noesis-sol/orchestra/internal/project"
)

func TestCustomConcurrencyAcceptsOneToMax(t *testing.T) {
	for v, want := range map[string]int{"1": 1, "5": 5, " 7 ": 7, "16": project.MaxConcurrency} {
		if n, err := parseConcurrency(v); err != nil || n != want {
			t.Errorf("parseConcurrency(%q) = %d, %v; want %d", v, n, err, want)
		}
	}
	for _, v := range []string{"", "0", "-1", "17", "2.5", "six"} {
		_, err := parseConcurrency(v)
		if err == nil || !strings.Contains(err.Error(), "from 1 to 16") {
			t.Errorf("parseConcurrency(%q): want an error naming the range, got %v", v, err)
		}
	}
}

func TestConcurrencyStartsOnCustomAboveFour(t *testing.T) {
	for current, want := range map[int]struct {
		option int
		custom string
	}{
		1:                      {1, ""},
		4:                      {4, ""},
		5:                      {customConcurrency, "5"},
		6:                      {customConcurrency, "6"},
		project.MaxConcurrency: {customConcurrency, "16"},
	} {
		if option, custom := concurrencyStart(current); option != want.option || custom != want.custom {
			t.Errorf("concurrencyStart(%d) = %d, %q; want %d, %q", current, option, custom, want.option, want.custom)
		}
	}
}

func TestHiddenFormFieldIsSkippedAndDrawsNothing(t *testing.T) {
	hidden := true
	input := huh.NewInput().Title("Number").Validate(func(string) error { return io.EOF })
	f := &formField{Field: input, gap: "\n\n", hidden: func() bool { return hidden }}
	input.Focus()
	input.Blur() // validates, so the input has an error
	if f.View() != "" || !f.Skip() || f.Error() != nil {
		t.Errorf("hidden: view %q, skip %v, error %v", f.View(), f.Skip(), f.Error())
	}
	hidden = false
	if f.View() != "\n\n"+input.View() || f.Skip() || f.Error() == nil {
		t.Errorf("shown: view %q, skip %v, error %v", f.View(), f.Skip(), f.Error())
	}
}

// askConcurrency runs the init form asking only for tickets at the same time, from current, with
// the keys typed.
func askConcurrency(t *testing.T, current int, keys string) int {
	t.Helper()
	c := project.Choice{Concurrent: current}
	done := make(chan error, 1)
	go func() {
		done <- AskInit(strings.NewReader(keys), io.Discard, &c, false, false, true, false, false, false)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("AskInit from %d with %q: %v", current, keys, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("AskInit from %d with %q didn't finish", current, keys)
	}
	return c.Concurrent
}

func TestInitFormKeepsOrTypesACustomConcurrency(t *testing.T) {
	const down, enter, backspace = "\x1b[B", "\r", "\x7f"
	for _, tc := range []struct {
		name    string
		current int
		keys    string
		want    int
	}{
		{"a saved 6, confirmed", 6, enter + enter, 6},
		{"a saved 3, confirmed", 3, enter, 3},
		{"a saved 6, changed to 2", 6, strings.Repeat("\x1b[A", 3) + enter, 2},
		{"custom typed from a saved 2", 2, strings.Repeat(down, 3) + enter + "20" + enter + backspace + backspace + "9" + enter, 9},
	} {
		if got := askConcurrency(t, tc.current, tc.keys); got != tc.want {
			t.Errorf("%s: concurrent = %d, want %d", tc.name, got, tc.want)
		}
	}
}
