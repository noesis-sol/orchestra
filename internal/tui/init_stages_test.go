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
	onChoice      = "┃ The project's checks"
	onCheck       = "┃ Check command"
	onTimeout     = "┃ check-fast time limit"
	shiftTab      = "\x1b[Z"
)

// reset forgets what the screen showed, so that a wait sees only what is drawn from then on.
func (s *syncBuffer) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

// askStages runs the init form for the union and the checks on a fake terminal, from c, with no
// scout: stage 2's choice starts on Manual.
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
	term.typeSteps(t, []keysOn{{onChoice, "\r"}, {onCheck, "make check\r"}, {onTimeout, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if !c.Union || c.FastCommand() != "make check" || !c.ReplaceFast || c.CheckFastTimeout != project.DefaultCheckTimeoutText {
		t.Errorf("union %v, check %q, time limit %q", c.Union, c.FastCommand(), c.CheckFastTimeout)
	}
}

func TestShiftTabBackToWorkersKeepsTheAnswers(t *testing.T) {
	c := project.Choice{Union: true}
	term := askStages(t, &c)
	term.typeSteps(t, []keysOn{{onUnion, "n"}, {onChoice, "\r"}, {onCheck, "just verify"}})
	term.waitFor(t, "just verify")
	term.typeKeys(t, shiftTab)
	term.waitFor(t, onChoice)
	term.screen.reset()
	term.typeKeys(t, shiftTab)
	// Back on stage 1, the union still declined; on to stage 2, Manual still chosen and the check
	// still typed.
	term.waitFor(t, workersHeader)
	term.typeSteps(t, []keysOn{{onUnion, "\r"}, {checksHeader, ""}, {onChoice, "\r"}, {"just verify", ""}})
	term.typeSteps(t, []keysOn{{onCheck, "\r"}, {onTimeout, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if c.Union || c.FastCommand() != "just verify" {
		t.Errorf("union %v, check %q; want false, %q", c.Union, c.FastCommand(), "just verify")
	}
}

func TestEscOrCtrlCInChecksCancelsTheForm(t *testing.T) {
	for _, cancel := range []string{"\x1b", "\x03"} {
		c := project.Choice{Union: true}
		term := askStages(t, &c)
		term.typeSteps(t, []keysOn{{onUnion, "\r"}, {onChoice, "\r"}, {onCheck, "just"}, {"just", cancel}})
		if err := term.end(t); !errors.Is(err, huh.ErrUserAborted) {
			t.Errorf("%q in stage 2: AskInit = %v, want %v", cancel, err, huh.ErrUserAborted)
		}
	}
}

func TestAStageTheFlagsAnsweredIsSkipped(t *testing.T) {
	for _, tc := range []struct {
		name         string
		ask          Ask
		header       string
		steps        []keysOn
		notOnTheForm string
	}{
		{"only checks asked", Ask{Check: true}, "Checks", []keysOn{{onChoice, "\r"}, {onCheck, "make check\r"}},
			"Merge CHANGELOG.md"},
		{"only workers asked", Ask{Union: true}, "Workers", []keysOn{{onUnion, "\r"}}, "Check command"},
	} {
		c := project.Choice{}
		term := askOn(t, func(in io.Reader, out io.Writer) error { return AskInit(in, out, &c, tc.ask) })
		term.typeSteps(t, tc.steps)
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
	// Tickets at the same time, union, the choice of checks (3, Manual), check command.
	if err := AskInit(typed("2", "y", "3", "make check"), &out, &c, Ask{Concurrent: true, Union: true, Check: true}); err != nil {
		t.Fatal(err)
	}
	screen := ansi.Strip(out.String())
	workers, checks := strings.Index(screen, workersHeader), strings.Index(screen, checksHeader)
	if workers < 0 || checks < workers || strings.Index(screen, "Merge CHANGELOG.md") > checks ||
		strings.Index(screen, "Check command") < checks {
		t.Errorf("want each stage's questions under its header:\n%s", screen)
	}
	if c.Concurrent != 2 || !c.Union || c.FastCommand() != "make check" {
		t.Errorf("concurrent %d, union %v, check %q", c.Concurrent, c.Union, c.FastCommand())
	}
}
