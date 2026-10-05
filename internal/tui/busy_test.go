package tui

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/charmbracelet/x/ansi"
	"go.uber.org/goleak"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// On a terminal a busy step is a spinner and the time taken, redrawn in place until the step ends,
// then its final line; an event printed meanwhile goes above it. No goroutine is left.
func TestBusyLineSpinsThenShowsItsFinalLine(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		p := Terminal(&b, 80)
		busy := p.Busy("writing the run report with claude… (ctrl+c skips)", "Writing the run report with Claude…",
			"(Ctrl+C skips)")
		time.Sleep(1050 * time.Millisecond)
		p.Event(dispatch.Event{Kind: dispatch.EvTriage, Ticket: "orchestra-1", Detail: "retry", Title: "A ticket",
			Time: time.Date(2026, 10, 5, 12, 0, 1, 0, time.UTC)})
		time.Sleep(1050 * time.Millisecond)
		busy.Done("Run report written")
		out := ansi.Strip(b.String())

		frames := 0
		for _, f := range busySpinner.Frames {
			if strings.Contains(out, "\r"+strings.TrimSpace(f)+" Writing the run report with Claude…") {
				frames++
			}
		}
		if frames < 2 {
			t.Errorf("drew %d of the spinner's frames, want them in turn:\n%q", frames, out)
		}
		for _, want := range []string{"Claude… 0:00 (Ctrl+C skips)\r", "Claude… 0:01 (Ctrl+C skips)\r",
			"Claude… 0:02 (Ctrl+C skips)\r",
			// The event takes the busy line's place, which is drawn again below it.
			"Claude… 0:01 (Ctrl+C skips)\r12:00:01 ◆ orchestra-1 triage: retry  A ticket\n⣽ Writing the run report"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%q", want, out)
			}
		}
		if !strings.Contains(b.String(), "\r"+ansi.EraseEntireLine) {
			t.Errorf("the busy line isn't cleared before it is redrawn:\n%q", b.String())
		}
		if !strings.HasSuffix(out, "\r◆ Run report written (2s)\n") {
			t.Errorf("the busy line doesn't end as its final line:\n%q", out)
		}
	})
}

// A step that ends within the spinner's first frame, such as triage with nothing queued, shows
// only its final line.
func TestBusyStepEndingAtOnceShowsOnlyItsFinalLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		Terminal(&b, 80).Busy("finishing triage…", "Finishing triage…", "").Done("Triage finished")
		if out := ansi.Strip(b.String()); out != "◆ Triage finished\n" {
			t.Errorf("printed %q", out)
		}
	})
}

// A busy step that fails ends with the warning in its place.
func TestBusyStepEndsWithAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		busy := Terminal(&b, 80).Busy("writing the run report…", "Writing the run report…", "")
		time.Sleep(500 * time.Millisecond)
		busy.Warn("REVIEW_FAILED: claude: timed out")
		if out := ansi.Strip(b.String()); !strings.HasSuffix(out, "\r◆ REVIEW_FAILED: claude: timed out\n") {
			t.Errorf("printed %q", out)
		}
	})
}

// Plain output says the step began, as the log does, and nothing more; a styled printer that isn't
// a Terminal prints the line once, without animation.
func TestBusyStepWithoutATerminalIsOneLine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		busy := Printer{Out: &b}.Busy("finishing triage…", "Finishing triage…", "")
		time.Sleep(2 * time.Second)
		busy.Done("Triage finished")
		if out := b.String(); strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, " finishing triage…\n") {
			t.Errorf("plain output printed %q", out)
		}

		b.Reset()
		busy = Printer{Out: &b, Styled: true, Width: 80}.Busy("", "Writing the run report…", "(Ctrl+C skips)")
		time.Sleep(2 * time.Second)
		busy.Done("Run report written")
		if out := ansi.Strip(b.String()); out != "◆ Writing the run report… (Ctrl+C skips)\n◆ Run report written (2s)\n" {
			t.Errorf("a styled printer printed %q", out)
		}
	})
}

func TestBusyTimes(t *testing.T) {
	for _, tc := range []struct {
		d            time.Duration
		clock, final string
	}{
		{0, "0:00", "0s"},
		{42*time.Second + 900*time.Millisecond, "0:42", "42s"},
		{64 * time.Second, "1:04", "1m04s"},
		{time.Hour + 2*time.Minute + 3*time.Second, "1:02:03", "1h02m03s"},
	} {
		if got := clock(tc.d); got != tc.clock {
			t.Errorf("clock(%v) = %q, want %q", tc.d, got, tc.clock)
		}
		if got := duration(tc.d); got != tc.final {
			t.Errorf("duration(%v) = %q, want %q", tc.d, got, tc.final)
		}
	}
}
