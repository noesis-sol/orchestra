package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// streamRecords reads the event stream in the checkout repo, failing the test unless each line is
// one JSON object.
func streamRecords(t *testing.T, repo string) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for line := range strings.Lines(read(t, filepath.Join(repo, project.RunPath(dispatch.EventsName)))) {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil || r == nil || !strings.HasSuffix(line, "\n") {
			t.Fatalf("not a whole JSON object on its line (%v): %q", err, line)
		}
		recs = append(recs, r)
	}
	return recs
}

// kindsOf lists the records' kinds, in order.
func kindsOf(recs []map[string]any) string {
	var kinds []string
	for _, r := range recs {
		kind, _ := r["kind"].(string)
		kinds = append(kinds, kind)
	}
	return strings.Join(kinds, " ")
}

// Quitting at once writes what the log says to the event stream too, and the end record with exit
// code 130, before the stream closes: no deferred code runs after it. A record the loop sends after
// that is dropped.
func TestQuittingAtOnceRecordsHowTheRunEnded(t *testing.T) {
	loop := &windingLoop{running: []dispatch.Status{{Ticket: "a-1", Tab: "w1:2"}},
		finishing: []dispatch.Finishing{{Ticket: "a-1", What: "merge"}}}
	r := newQuitRig(t, loop)
	repo := t.TempDir()
	r.log.Begin(repo, dispatch.RunStart{Started: time.Now(), Concurrency: 1})
	r.stopLoop(os.Interrupt)
	r.stops.deliver(os.Interrupt)
	r.stops.deliver(os.Interrupt)
	r.quitOnce(t, "INTERRUPTED: quit at once with Ctrl+C")
	r.log.Record(dispatch.Event{Kind: dispatch.EvInfo, Time: time.Now(), Text: "  a-1's merge is done"})

	recs := streamRecords(t, repo)
	if got := kindsOf(recs); got != "start warn stop end" {
		t.Fatalf("kinds: %s\n%v", got, recs)
	}
	for i, want := range []string{"a-1's merge is under way; press Ctrl+C again to abandon it",
		"INTERRUPTED: quit at once with Ctrl+C, leaving a-1 (tab w1:2) running"} {
		if text, _ := recs[i+1]["text"].(string); !strings.Contains(text, want) || !strings.Contains(r.logged(t), text) {
			t.Errorf("%s record's text %q: want %q, as logged:\n%s", recs[i+1]["kind"], text, want, r.logged(t))
		}
	}
	if code := recs[3]["code"]; code != float64(dispatch.ExitInterrupted) {
		t.Errorf("end record's code: %v", code)
	}
}

// exitCode is the code main exits with, which the end record gives.
func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{nil, 0},
		{exitStatus(dispatch.ExitStuck), dispatch.ExitStuck},
		{os.ErrClosed, 1},
	} {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("exitCode(%v) = %d, want %d", tc.err, got, tc.want)
		}
	}
}
