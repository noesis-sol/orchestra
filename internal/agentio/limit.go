package agentio

import (
	"regexp"
	"strconv"
	"time"
)

// resetWords match the reset time in Claude Code's words of the usage limit ("resets 7pm (Europe/Athens)")
// or of its automatic continue ("continuing automatically at 5:30am"): its day (month, day and year), its
// hour and minutes, am or pm, and its time zone; "15:04", in 24 hours, is read as well.
var resetWords = regexp.MustCompile(`\b(?:resets|continuing automatically at) ` +
	`(?:([A-Z][a-z]{2}) (\d{1,2})(?:, (\d{4}))?(?:,| at) )?(\d{1,2})(?::(\d{2}))?([ap]m)?(?: \(([^()]+)\))?`)

// LimitReset reads the reset time in message, Claude Code's words of the usage limit or of its automatic
// continue, written at at; zero when it has none it can read. A time with no day is the next one at that
// hour after at, or just before it (an hour at most): Claude Code gives the day of one more than 24 hours
// away. A time with no time zone, or one Go doesn't know, is in the local one, the user's.
func LimitReset(message string, at time.Time) time.Time {
	m := resetWords.FindStringSubmatch(message)
	if m == nil || m[5] == "" && m[6] == "" {
		return time.Time{} // "resets 7", no more: not a time
	}
	hour, _ := strconv.Atoi(m[4])
	minute, _ := strconv.Atoi(m[5])
	switch {
	case m[6] != "" && (hour < 1 || hour > 12), m[6] == "" && hour > 23, minute > 59:
		return time.Time{}
	case m[6] == "am" && hour == 12:
		hour = 0
	case m[6] == "pm" && hour != 12:
		hour += 12
	}
	loc := time.Local
	if m[7] != "" {
		if l, err := time.LoadLocation(m[7]); err == nil {
			loc = l
		}
	}
	now := at.In(loc)
	if m[1] == "" {
		t := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
		if t.Before(now.Add(-time.Hour)) {
			t = time.Date(now.Year(), now.Month(), now.Day()+1, hour, minute, 0, 0, loc)
		}
		return t
	}
	month, err := time.Parse("Jan", m[1])
	day, _ := strconv.Atoi(m[2])
	if err != nil || day < 1 || day > 31 {
		return time.Time{}
	}
	year := now.Year()
	if m[3] != "" {
		year, _ = strconv.Atoi(m[3])
	}
	return time.Date(year, month.Month(), day, hour, minute, 0, 0, loc)
}
