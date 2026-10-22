package dispatch

import (
	"fmt"
	"strings"
	"time"
)

// reviewLogHead and reviewLogTail cap the run's log as the reviewer is given it: at most reviewLogHead
// lines of its start, up to the end of its START block, and its last reviewLogTail lines, so a long
// overnight run's review stays well inside the model's context.
const (
	reviewLogHead = 60
	reviewLogTail = 400
)

// reviewLog is the run's log lines as the reviewer is given them: all of them when they are few, else
// its START block and its last lines, with a line between them saying how many were cut.
func reviewLog(lines []string) string {
	if len(lines) <= reviewLogHead+reviewLogTail {
		return strings.Join(lines, "\n")
	}
	head, tail := startBlockEnd(lines), len(lines)-reviewLogTail
	kept := append(lines[:head:head], fmt.Sprintf("[%d lines of the log cut here]", tail-head))
	return strings.Join(append(kept, lines[tail:]...), "\n")
}

// startBlockEnd is the index just past the START block among the log's first reviewLogHead lines:
// the lines before its START line, the line and the indented ones after it, at most reviewLogHead in
// all; 0 when they have no START line.
func startBlockEnd(lines []string) int {
	head := lines[:min(len(lines), reviewLogHead)]
	for i, line := range head {
		if !strings.HasPrefix(logText(line), "START ") {
			continue
		}
		end := i + 1
		for end < len(head) && strings.HasPrefix(logText(head[end]), "  ") {
			end++
		}
		return end
	}
	return 0
}

// logText is a log line without its time stamp.
func logText(line string) string {
	if len(line) <= len(logTime) {
		return line
	}
	return line[len(logTime)+1:]
}

// reportSpan is when the run ran, as the report's heading gives it: the start's date and time, and
// the end's time, after its date when that differs from the start's.
func reportSpan(start, end time.Time) string {
	const day, minute = "2006-01-02", "15:04"
	if end.Format(day) != start.Format(day) {
		return start.Format(day+" "+minute) + "–" + end.Format(day+" "+minute)
	}
	return start.Format(day+" "+minute) + "–" + end.Format(minute)
}

// reviewTime is how the reviewer's closing line gives the run's start and end: with their dates and
// UTC offsets, as the log stamps its lines, so a run past midnight or a clock change reads in order.
const reviewTime = "2006-01-02 15:04 -0700"
