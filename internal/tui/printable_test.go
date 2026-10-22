package tui

import (
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// receivedTypes are the types the dashboard and the printer take in, each with what makes it
// printable as it is received.
var receivedTypes = map[string]struct {
	zero      any
	printable func(any) any
}{
	"Event":  {dispatch.Event{}, func(v any) any { return printableEvent(v.(dispatch.Event)) }},
	"Status": {dispatch.Status{}, func(v any) any { return printableStatus(v.(dispatch.Status)) }},
	"NothingToRun": {dispatch.NothingToRun{}, func(v any) any {
		return printableNothing(v.(dispatch.NothingToRun))
	}},
	"InterviewProgress": {InterviewProgress{}, func(v any) any { return v.(InterviewProgress).printable() }},
	"Scouting":          {organ.Scouting{}, func(v any) any { return printableScouting(v.(organ.Scouting)) }},
}

// keptAsGiven are the string fields, as <type>.<field>, left as they are received: the terminal's
// name for a worker's tab, handed back to it and never shown, and the commands the runners run.
var keptAsGiven = map[string]bool{"Status.Tab": true, "Suite.Command": true, "Suite.Targeted": true}

// hostileText is text with escape sequences and other control characters in it.
const hostileText = "Fix \x1b]52;c;cm0gLXJmIH4K\x07\x1b[2Jit\b"

// visitStrings calls f with every exported string in v, a map's keys included, and the field it is in
// as <type>.<field> ("" for a map's key or a slice's element of a type of its own). With fill, it
// first gives each empty slice an element, each nil map a key and each nil pointer a value, so that
// every string field is reached.
func visitStrings(v reflect.Value, field string, fill bool, f func(field string, s reflect.Value)) {
	switch v.Kind() {
	case reflect.String:
		f(field, v)
	case reflect.Struct:
		for i := range v.NumField() {
			if sf := v.Type().Field(i); sf.IsExported() {
				visitStrings(v.Field(i), v.Type().Name()+"."+sf.Name, fill, f)
			}
		}
	case reflect.Slice:
		if fill && v.Len() == 0 {
			v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		}
		for i := range v.Len() {
			visitStrings(v.Index(i), field, fill, f)
		}
	case reflect.Pointer:
		if fill && v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		if !v.IsNil() {
			visitStrings(v.Elem(), field, fill, f)
		}
	case reflect.Map:
		if fill && v.Len() == 0 {
			key, elem := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
			visitStrings(key, field, fill, f)
			visitStrings(elem, field, fill, f)
			v.Set(reflect.MakeMap(v.Type()))
			v.SetMapIndex(key, elem)
			return
		}
		for it := v.MapRange(); it.Next(); {
			key, elem := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
			key.Set(it.Key())
			elem.Set(it.Value())
			visitStrings(key, field, false, f)
			visitStrings(elem, field, false, f)
		}
	default: // numbers, booleans, an error, a time: nothing a worker or an organ writes
	}
}

// Every string field of what the dashboard and the printer take in, however deep, is made printable
// as it is received, but those kept as given; the value received is left as it was, as the loop may
// still hold its slices and maps.
func TestReceivedTextIsMadePrintable(t *testing.T) {
	for name, typ := range receivedTypes {
		in := reflect.New(reflect.TypeOf(typ.zero)).Elem()
		filled := 0
		visitStrings(in, "", true, func(_ string, s reflect.Value) {
			s.SetString(hostileText)
			filled++
		})
		if filled < 2 {
			t.Fatalf("%s: only %d strings filled", name, filled)
		}
		out := reflect.ValueOf(typ.printable(in.Interface()))
		seen := 0
		visitStrings(out, "", false, func(field string, s reflect.Value) {
			seen++
			switch got := s.String(); {
			case keptAsGiven[field] && got != hostileText:
				t.Errorf("%s: %s is %q, want it kept as given", name, field, got)
			case !keptAsGiven[field] && strings.ContainsFunc(got, unicode.IsControl):
				t.Errorf("%s: %s is %q, still with control characters", name, field, got)
			case !keptAsGiven[field] && got != "Fix it":
				t.Errorf("%s: %s is %q, want %q", name, field, got, "Fix it")
			}
		})
		if seen != filled {
			t.Errorf("%s: %d strings filled, %d made printable", name, filled, seen)
		}
		visitStrings(in, "", false, func(field string, s reflect.Value) {
			if s.String() != hostileText {
				t.Errorf("%s: the received %s became %q", name, field, s.String())
			}
		})
	}
}

// An epic still open, a scope's ticket not done and a deferred ticket free to run can each have an
// ID with escape sequences, as a worker files tickets and Beads checks only an ID's prefix: the
// message of a run with nothing to run shows them without, styled and plain.
func TestNothingToRunDropsControlSequencesInTicketIDs(t *testing.T) {
	styled(t)
	for name, id := range attacks {
		for _, n := range []dispatch.NothingToRun{
			{AllDone: true, StillOpen: []string{id}},
			{Scope: id, NotDone: []dispatch.NotDone{{ID: id, Why: "blocked by " + id}}},
			{DeferredFree: []dispatch.DeferredFree{{ID: id, Blockers: []string{id}, Worktree: "/w/" + id}}},
			{DeferredFree: []dispatch.DeferredFree{{ID: id, Blockers: []string{id}, Uncommitted: "/w/" + id}}},
		} {
			var term, plain strings.Builder
			Printer{Out: &term, Styled: true, Width: 400}.Nothing(n)
			checkPrintable(t, name+" in the nothing-ready box", term.String())
			if !sgr.MatchString(term.String()) {
				t.Errorf("%s: the box lost its colours:\n%q", name, term.String())
			}
			Printer{Out: &plain}.Nothing(n)
			checkPrintable(t, name+" in the nothing-ready lines", plain.String())
		}
	}
}

// The interview's line names the epic claude filed, from the feature.json it wrote, without the
// escape sequences in its ID.
func TestInterviewLineDropsControlSequencesInTheEpic(t *testing.T) {
	styled(t)
	for name, id := range attacks {
		for _, p := range []InterviewProgress{{Epic: id}, {Epic: id, Title: "Its title", Tickets: 2}} {
			var out strings.Builder
			(&InterviewLine{Out: &out, Width: func() int { return 120 }}).Show(p)
			line := strings.ReplaceAll(out.String(), "\r", "")
			line = strings.Replace(line, "\x1b[K", "", 1) // the line's own erase
			checkPrintable(t, name+" in the interview's line", line)
		}
	}
}
