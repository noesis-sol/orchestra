package dispatch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// notifyLimit is how long a notification may take to show before its osascript is stopped.
const notifyLimit = 10 * time.Second

// Log is the run's log file, with its notifications, and its event stream (see Begin).
type Log struct {
	mu      sync.Mutex
	f       *os.File
	project string              // what notifications are titled with: the repository folder's name
	alert   func(args []string) // shows a notification, given osascript's arguments; nil when they are off
	lines   []string            // this run's lines, for the reviewer
	shown   sync.WaitGroup      // notifications still being shown
	closed  bool                // Close has been called: no more notifications start

	events     *os.File  // the event stream, from Begin to End; nil when it isn't open
	run        time.Time // the run's start, in each of its records
	eventsSaid bool      // the event stream failed, and the log has said so
}

// OpenLog opens the log file at path for appending. With notify, Notify shows macOS notifications
// titled with title.
func OpenLog(path string, notify bool, title string) (*Log, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f, project: title}
	if _, err := exec.LookPath("osascript"); notify && err == nil { // notifications need macOS
		l.alert = l.inBackground(func(args []string) {
			// Best effort: the line is in the log already, a notification that fails is only not shown.
			_, _ = command.Output(context.Background(), notifyLimit, "", "osascript", args...)
		})
	}
	return l, nil
}

// inBackground makes show run on its own goroutine, so a slow notification doesn't hold up the
// loop, and has Close wait for it. Notifications raised after Close are dropped.
func (l *Log) inBackground(show func(args []string)) func(args []string) {
	return func(args []string) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.closed {
			return
		}
		l.shown.Go(func() { show(args) })
	}
}

// Close waits for the notifications still being shown, each at most notifyLimit, so the run's
// last one isn't lost when orchestra exits, then closes the log file.
func (l *Log) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.shown.Wait()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeEvents()
	return l.f.Close()
}

// CloseNow closes the log file without waiting for the notifications still being shown, which
// show anyway: each runs in its own process group. It is for orchestra quitting at once.
func (l *Log) CloseNow() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	l.closeEvents()
	return l.f.Close()
}

// logTime is how the log dates its lines.
const logTime = "2006-01-02 15:04:05"

// Line logs text.
func (l *Log) Line(t time.Time, text string) {
	line := t.Format(logTime) + " " + text
	l.mu.Lock()
	fmt.Fprintln(l.f, line)
	l.lines = append(l.lines, line)
	l.mu.Unlock()
}

// Notify shows notice as a notification titled with the project, if they are on; "" shows none.
// The notice is a few words (see Notice): the line it is about goes to the log.
func (l *Log) Notify(notice string) {
	if l.alert != nil && notice != "" {
		l.alert(notification(l.project, notice))
	}
}

// RunLines returns the lines logged in this run.
func (l *Log) RunLines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// Raw appends tool output (git) to the log, as the bash version did.
func (l *Log) Raw(out string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if out = strings.TrimSpace(out); out != "" {
		fmt.Fprintln(l.f, out)
	}
	if err != nil {
		fmt.Fprintln(l.f, err)
	}
}

// notification is the osascript arguments showing notice, titled with the project. Both go in as
// arguments rather than into the script, so no quoting in them can break it.
func notification(project, notice string) []string {
	return []string{
		"-e", "on run argv",
		"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
		"-e", "end run",
		"--", notice, project, // -- so a notice starting with - isn't taken for an option
	}
}
