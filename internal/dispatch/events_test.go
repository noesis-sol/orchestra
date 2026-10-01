package dispatch

import (
	"strings"
	"testing"
)

// The osascript command takes the text and project as arguments, so quotes in them can't break
// the script, and a leading - isn't taken for an option.
func TestNotificationPassesTextAsArguments(t *testing.T) {
	cmd := notification(`my "repo"\`, `-x "closed" \ it`)
	n := len(cmd.Args)
	if n < 3 || cmd.Args[n-3] != "--" || cmd.Args[n-2] != `-x "closed" \ it` || cmd.Args[n-1] != `Orchestra: my "repo"\` {
		t.Errorf("args: %q", cmd.Args)
	}
	if script := strings.Join(cmd.Args[:n-3], " "); strings.Contains(script, "repo") || strings.Contains(script, "closed") {
		t.Errorf("text in the script: %q", script)
	}
}

// Notifications follow the kind of event, not words in it: a title or triage summary saying
// closed, FAILED or deferred shows nothing.
func TestRunNotifiesByEventNotText(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "closed FAILED deferred", 1)
	h.worker("A", finishes("a.txt"))
	o, code := h.run()
	if code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	o.emit(Event{Kind: EvTriage, Ticket: "A", Text: "  triage A: flaky (high confidence) - deferred after FAILED checks, closed sockets"})
	closed := h.sink.of(EvClosed)
	if len(closed) != 1 {
		t.Fatalf("closed: %q", closed)
	}
	want := []string{strings.TrimPrefix(closed[0], "A "), o.Final()}
	if got := h.alerts.list(); !equal(got, want) {
		t.Errorf("notified:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
