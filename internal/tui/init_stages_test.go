package tui

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The init form asks in stages, one at a time, each under its header: Workers, then Checks.

const (
	workersHeader = "Step 1 of 2 · Workers"
	checksHeader  = "Step 2 of 2 · Checks"
	onUnion       = "┃ Merge CHANGELOG.md by union"
	onCheck       = "┃ Check command"
	onTimeout     = "┃ Check time limit"
	shiftTab      = "\x1b[Z"
)

// reset forgets what the screen showed, so that a wait sees only what is drawn from then on.
func (s *syncBuffer) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

// askStages runs the init form for the union and the check on a fake terminal, from c.
func askStages(t *testing.T, c *project.Choice) *formTerminal {
	t.Helper()
	return askOn(t, func(in io.Reader, out io.Writer) error {
		return AskInit(in, out, c, Ask{Union: true, Check: true, Timeout: true})
	})
}

func TestInitFormAsksWorkersThenChecksUnderTheirHeaders(t *testing.T) {
	c := project.Choice{Union: true}
	term := askStages(t, &c)
	term.waitFor(t, workersHeader)
	term.waitFor(t, onUnion)
	if screen := term.screen.String(); strings.Contains(screen, "Check command") {
		t.Errorf("stage 1 shows stage 2's question:\n%s", screen)
	}
	term.typeKeys(t, "\r")
	term.waitFor(t, checksHeader)
	term.typeSteps(t, []keysOn{{onCheck, "make check\r"}, {onTimeout, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if !c.Union || c.Check != "make check" || c.CheckTimeout != project.DefaultCheckTimeoutText {
		t.Errorf("union %v, check %q, time limit %q", c.Union, c.Check, c.CheckTimeout)
	}
}

func TestShiftTabBackToWorkersKeepsTheAnswers(t *testing.T) {
	c := project.Choice{Union: true}
	term := askStages(t, &c)
	term.typeSteps(t, []keysOn{{onUnion, "n"}, {onCheck, "just verify"}})
	term.waitFor(t, "just verify")
	term.screen.reset()
	term.typeKeys(t, shiftTab)
	// Back on stage 1, the union still declined; on to stage 2, the check still typed.
	term.waitFor(t, workersHeader)
	term.typeSteps(t, []keysOn{{onUnion, "\r"}, {checksHeader, ""}, {"just verify", ""}})
	term.typeSteps(t, []keysOn{{onCheck, "\r"}, {onTimeout, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if c.Union || c.Check != "just verify" {
		t.Errorf("union %v, check %q; want false, %q", c.Union, c.Check, "just verify")
	}
}

func TestEscOrCtrlCInChecksCancelsTheForm(t *testing.T) {
	for _, cancel := range []string{"\x1b", "\x03"} {
		c := project.Choice{Union: true}
		term := askStages(t, &c)
		term.typeSteps(t, []keysOn{{onUnion, "\r"}, {onCheck, "just"}, {"just", cancel}})
		if err := term.end(t); !errors.Is(err, huh.ErrUserAborted) {
			t.Errorf("%q in stage 2: AskInit = %v, want %v", cancel, err, huh.ErrUserAborted)
		}
	}
}

func TestAStageTheFlagsAnsweredIsSkipped(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ask          Ask
		on, header   string
		keys         string
		notOnTheForm string
	}{
		{"only checks asked", Ask{Check: true}, onCheck, "Checks", "make check\r", "Merge CHANGELOG.md"},
		{"only workers asked", Ask{Union: true}, onUnion, "Workers", "\r", "Check command"},
	} {
		c := project.Choice{}
		term := askOn(t, func(in io.Reader, out io.Writer) error { return AskInit(in, out, &c, tc.ask) })
		term.typeSteps(t, []keysOn{{tc.on, tc.keys}})
		if err := term.end(t); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		screen := term.screen.String()
		if !strings.Contains(screen, tc.header) || strings.Contains(screen, "Step ") ||
			strings.Contains(screen, tc.notOnTheForm) {
			t.Errorf("%s: want the header %q alone:\n%s", tc.name, tc.header, screen)
		}
	}
}

func TestAccessibleInitFormPrintsEachStagesHeader(t *testing.T) {
	t.Setenv("TERM", "dumb")
	c := project.Choice{Concurrent: 1}
	var out strings.Builder
	// Tickets at the same time, union, check command.
	if err := AskInit(typed("2", "y", "make check"), &out, &c, Ask{Concurrent: true, Union: true, Check: true}); err != nil {
		t.Fatal(err)
	}
	screen := ansi.Strip(out.String())
	workers, checks := strings.Index(screen, workersHeader), strings.Index(screen, checksHeader)
	if workers < 0 || checks < workers || strings.Index(screen, "Merge CHANGELOG.md") > checks ||
		strings.Index(screen, "Check command") < checks {
		t.Errorf("want each stage's questions under its header:\n%s", screen)
	}
	if c.Concurrent != 2 || !c.Union || c.Check != "make check" {
		t.Errorf("concurrent %d, union %v, check %q", c.Concurrent, c.Union, c.Check)
	}
}
