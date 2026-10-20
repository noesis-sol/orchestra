package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// Init's last stage asks for the ticket prefix, starting from the current one, and asks to confirm
// a change that renames the tickets.

const (
	onPrefix = "┃ Ticket prefix"
	ctrlU    = "\x15" // clears the input before the cursor
)

// existingPrefix is the choice for a repository whose Beads has 12 tickets prefixed orchestra.
func existingPrefix() project.Choice {
	return project.Choice{Prefix: "orchestra", PrefixWas: "orchestra", PrefixTickets: 12,
		PrefixExample: "orchestra-74b2.31"}
}

func TestEnterKeepsTheTicketPrefix(t *testing.T) {
	for name, c := range map[string]project.Choice{
		"new Beads": {Prefix: "orchestra", PrefixWas: "orchestra", PrefixNew: true},
		"existing":  existingPrefix(),
	} {
		term := askOn(t, func(in io.Reader, out io.Writer) error {
			return AskInit(t.Context(), in, out, ttyTerm, &c, Ask{Questions: project.Questions{Prefix: true}})
		})
		term.waitFor(t, onPrefix)
		term.waitFor(t, "orchestra")
		term.typeKeys(t, "\r")
		if err := term.end(t); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.Prefix != "orchestra" || c.RenamePrefix {
			t.Errorf("%s: prefix %q, rename %v; want orchestra kept", name, c.Prefix, c.RenamePrefix)
		}
		if screen := term.screen.String(); strings.Contains(screen, "Rename") {
			t.Errorf("%s: asked to rename a prefix kept:\n%s", name, screen)
		}
	}
}

func TestChangingTheTicketPrefixAsksToRenameTheTickets(t *testing.T) {
	for _, answer := range []string{"y", "n"} {
		c := existingPrefix()
		term := askOn(t, func(in io.Reader, out io.Writer) error {
			return AskInit(t.Context(), in, out, ttyTerm, &c, Ask{Questions: project.Questions{Prefix: true}})
		})
		term.typeSteps(t, []keysOn{
			{onPrefix, ctrlU + "orc-"},
			{"orc-", "\r"},
			{"┃ Rename 12 tickets, orchestra-74b2.31 → orc-74b2.31?", answer},
		})
		if err := term.end(t); err != nil {
			t.Fatal(err)
		}
		if c.Prefix != "orc" || c.RenamePrefix != (answer == "y") {
			t.Errorf("%s: prefix %q, rename %v", answer, c.Prefix, c.RenamePrefix)
		}
	}
}

func TestTheFormRefusesAnInvalidTicketPrefix(t *testing.T) {
	c := project.Choice{Prefix: "orchestra", PrefixWas: "orchestra", PrefixNew: true}
	term := askOn(t, func(in io.Reader, out io.Writer) error {
		return AskInit(t.Context(), in, out, ttyTerm, &c, Ask{Questions: project.Questions{Prefix: true}})
	})
	term.typeSteps(t, []keysOn{
		{onPrefix, ctrlU + "Orc"},
		{"Orc", "\r"},
		{"the prefix must start with a lowercase letter (got 'Orc')", ctrlU + "orc_x"},
		{"orc_x", "\r"},
		{"must hold only lowercase letters, digits and hyphens (got 'orc_x')", ctrlU + "orc2"},
		{"orc2", "\r"},
	})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if c.Prefix != "orc2" {
		t.Errorf("prefix %q, want orc2", c.Prefix)
	}
}

func TestTheFormKeepsTheTicketPrefixWhileTheTicketsCantBeRenamed(t *testing.T) {
	c := existingPrefix()
	c.PrefixRefusal = "the last run left workers on orchestra-a1 (in .orchestra/run/state.json)"
	term := askOn(t, func(in io.Reader, out io.Writer) error {
		return AskInit(t.Context(), in, out, ttyTerm, &c, Ask{Questions: project.Questions{Prefix: true}})
	})
	term.typeSteps(t, []keysOn{
		{"It stays orchestra for now", ctrlU + "orc"},
		{"orc", "\r"},
		{"the tickets can't be renamed now; keep orchestra", ctrlU},
		{"", "\r"}, // cleared: kept
	})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if c.Prefix != "orchestra" {
		t.Errorf("prefix %q, want orchestra kept", c.Prefix)
	}
}

func TestAccessibleFormAsksForTheTicketPrefix(t *testing.T) {
	for _, tc := range []struct {
		answers []string
		prefix  string
		rename  bool
	}{
		{[]string{""}, "orchestra", false},
		{[]string{"Bad", "orc", "y"}, "orc", true},
		{[]string{"orc", ""}, "orc", false},
	} {
		c := existingPrefix()
		var out strings.Builder
		if err := AskInit(t.Context(), typed(tc.answers...), &out, "dumb", &c, Ask{Questions: project.Questions{Prefix: true}}); err != nil {
			t.Fatalf("%v: %v\n%s", tc.answers, err, out.String())
		}
		if c.Prefix != tc.prefix || c.RenamePrefix != tc.rename {
			t.Errorf("%v: prefix %q, rename %v\n%s", tc.answers, c.Prefix, c.RenamePrefix, out.String())
		}
		if tc.rename && !strings.Contains(out.String(), "Rename 12 tickets, orchestra-74b2.31 → orc-74b2.31? [y/N]") {
			t.Errorf("%v: the confirmation isn't asked:\n%s", tc.answers, out.String())
		}
	}
}
