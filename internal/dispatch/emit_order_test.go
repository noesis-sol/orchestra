package dispatch

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// Events emitted from goroutines at once, as a merge's closed line and the next dispatch are, reach
// the log, the event stream and the sink in one order.
func TestEventsEmittedAtOnceKeepOneOrder(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	l, _ := openLog(t)
	l.Begin(repo, RunStart{Started: time.Now(), Concurrency: 4})
	sink := &recordSink{}
	o := &Loop{log: l, tallies: newTallies(sink)}

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 50 {
				o.emit(Event{Kind: EvInfo, Text: fmt.Sprintf("goroutine %d, event %d", g, i)})
			}
		})
	}
	wg.Wait()

	lines := l.RunLines()
	for i, line := range lines {
		lines[i] = line[len(logTime)+1:]
	}
	var texts []string
	for _, r := range records(t, repo)[1:] { // after the start record
		texts = append(texts, r["text"].(string))
	}
	var sunk []string
	for _, ev := range sink.events {
		sunk = append(sunk, ev.Text)
	}
	if len(lines) != 400 {
		t.Fatalf("%d lines logged, want 400", len(lines))
	}
	if !equal(texts, lines) {
		t.Errorf("records' texts:\n%s\nlog:\n%s", strings.Join(texts, "\n"), strings.Join(lines, "\n"))
	}
	if !equal(sunk, lines) {
		t.Errorf("sink's events:\n%s\nlog:\n%s", strings.Join(sunk, "\n"), strings.Join(lines, "\n"))
	}
}
