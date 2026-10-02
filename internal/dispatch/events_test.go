package dispatch

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The osascript command takes the notice and project as arguments, so quotes in them can't break
// the script, and a leading - isn't taken for an option. The project alone is the title.
func TestNotificationPassesTextAsArguments(t *testing.T) {
	args := notification(`my "repo"\`, `-x "closed" \ it`)
	n := len(args)
	if n < 3 || args[n-3] != "--" || args[n-2] != `-x "closed" \ it` || args[n-1] != `my "repo"\` {
		t.Errorf("args: %q", args)
	}
	if script := strings.Join(args[:n-3], " "); strings.Contains(script, "repo") || strings.Contains(script, "closed") {
		t.Errorf("text in the script: %q", script)
	}
}

// Close waits for the notifications still being shown, so the run's last one isn't lost when
// orchestra exits, and drops any raised after it.
func TestCloseWaitsForNotifications(t *testing.T) {
	t.Parallel()
	l, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	release, shown := make(chan struct{}), make(chan string, 2)
	l.alert = l.inBackground(func(args []string) {
		<-release
		shown <- shownBy(args).body
	})
	l.Notify("ALL MERGED")
	closed := make(chan error)
	go func() { closed <- l.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned while a notification was still being shown")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if got := <-shown; got != "ALL MERGED" {
		t.Errorf("shown %q", got)
	}
	l.Notify("after close")
	if len(shown) != 0 {
		t.Errorf("shown after Close: %q", <-shown)
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
	want := []string{"Closed A · closed FAILED deferred", "Finished the run · 1 ticket closed"}
	if got := h.alerts.list(); !equal(got, want) {
		t.Errorf("notified:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
