package dispatch

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// stamped is text as the log writes it, stamped at minute m of the run.
func stamped(m int, text string) string {
	return time.Date(2026, 10, 22, 22, 0, 0, 0, time.UTC).Add(time.Duration(m)*time.Minute).Format(logTime) + " " + text
}

// A short log goes to the reviewer whole.
func TestReviewLogKeepsAShortLogWhole(t *testing.T) {
	t.Parallel()
	lines := []string{stamped(0, "START orchestra"), stamped(1, "  settings: x"), stamped(2, "MERGED a-1")}
	if got, want := reviewLog(lines), strings.Join(lines, "\n"); got != want {
		t.Errorf("reviewLog = %q, want %q", got, want)
	}
}

// A long log goes to the reviewer as its START block and its last lines, saying how many were cut.
func TestReviewLogCapsALongLog(t *testing.T) {
	t.Parallel()
	lines := []string{stamped(0, "carried over: a-0"), stamped(0, "START orchestra v1"),
		stamped(0, "  settings: one"), stamped(0, "  templates: two")}
	for i := range 2000 {
		lines = append(lines, stamped(i, fmt.Sprintf("MERGED a-%d", i)))
	}
	got := strings.Split(reviewLog(lines), "\n")
	if len(got) != 4+1+reviewLogTail {
		t.Fatalf("reviewLog kept %d lines, want %d", len(got), 4+1+reviewLogTail)
	}
	if want := strings.Join(lines[:4], "\n"); strings.Join(got[:4], "\n") != want {
		t.Errorf("reviewLog starts %q, want the START block %q", got[:4], want)
	}
	if want := fmt.Sprintf("[%d lines of the log cut here]", 2000-reviewLogTail); got[4] != want {
		t.Errorf("reviewLog's cut line = %q, want %q", got[4], want)
	}
	if want := strings.Join(lines[len(lines)-reviewLogTail:], "\n"); strings.Join(got[5:], "\n") != want {
		t.Errorf("reviewLog doesn't end with the log's last %d lines", reviewLogTail)
	}
}

// A long log whose START block runs past reviewLogHead lines keeps only that many of it, and one with
// no START line keeps only its last lines.
func TestReviewLogCapsTheStartBlock(t *testing.T) {
	t.Parallel()
	long := []string{stamped(0, "START orchestra")}
	for range 2 * reviewLogHead {
		long = append(long, stamped(0, "  settings: note"))
	}
	none := []string{}
	for range 2 * reviewLogTail {
		long = append(long, stamped(1, "MERGED a-1"))
		none = append(none, stamped(1, "MERGED a-1"))
	}
	for _, tc := range []struct {
		name  string
		lines []string
		head  int
	}{{"long START block", long, reviewLogHead}, {"no START line", none, 0}} {
		got := strings.Split(reviewLog(tc.lines), "\n")
		if len(got) != tc.head+1+reviewLogTail {
			t.Errorf("%s: reviewLog kept %d lines, want %d", tc.name, len(got), tc.head+1+reviewLogTail)
			continue
		}
		if want := fmt.Sprintf("[%d lines of the log cut here]", len(tc.lines)-tc.head-reviewLogTail); got[tc.head] != want {
			t.Errorf("%s: reviewLog's cut line = %q, want %q", tc.name, got[tc.head], want)
		}
	}
}

// The report's span gives the end's date when the run ends on another day than it started.
func TestReportSpanDatesAnEndPastMidnight(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 22, 22, 5, 0, 0, time.UTC)
	for _, tc := range []struct {
		end  time.Time
		want string
	}{
		{start.Add(time.Hour), "2026-10-22 22:05–23:05"},
		{start.Add(3 * time.Hour), "2026-10-22 22:05–2026-10-23 01:05"},
	} {
		if got := reportSpan(start, tc.end); got != tc.want {
			t.Errorf("reportSpan(%v, %v) = %q, want %q", start, tc.end, got, tc.want)
		}
	}
}

// The log stamps its lines with their UTC offset, so the hour a clock change repeats reads in order.
func TestLogTimeGivesTheOffset(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 25, 2, 30, 0, 0, time.FixedZone("CET", 3600))
	if got, want := at.Format(logTime), "2026-10-25 02:30:00 +0100"; got != want {
		t.Errorf("a log stamp = %q, want %q", got, want)
	}
}
