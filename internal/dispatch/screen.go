package dispatch

import "strings"

// inputHolds reports whether the agent's input box still holds the prompt, unsent. The box runs
// from the last line starting with ❯ to the rule below it; a long paste shows only its last lines
// there, or a "[Pasted text …]" placeholder, so any substantial line of the prompt counts.
func inputHolds(screen, prompt string) bool {
	lines := strings.Split(screen, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "❯") {
			start = i
		}
	}
	if start < 0 {
		return false
	}
	var box []string
	for i, l := range lines[start:] {
		t := strings.TrimSpace(l)
		if i > 0 && strings.HasPrefix(t, "─") {
			break
		}
		box = append(box, t)
	}
	box[0] = strings.TrimSpace(strings.TrimPrefix(box[0], "❯"))
	text := strings.Join(box, " ")
	if strings.Contains(text, "[Pasted text") {
		return true
	}
	for _, l := range strings.Split(prompt, "\n") {
		r := []rune(strings.TrimSpace(l))
		if len(r) < 20 {
			continue // too short to tell apart from anything else on screen
		}
		if len(r) > 40 {
			r = r[:40]
		}
		if strings.Contains(text, string(r)) {
			return true
		}
	}
	return false
}

// Claude Code marks tool calls with ⏺ and its working spinner with one of these glyphs.
var activityMarks = []string{"⏺", "✻", "✶", "✳", "✢", "✽"}

// lastActivity returns the worker's most recent action or spinner line, skipping the input box
// and status bar at the bottom of its screen.
func lastActivity(screen string) string {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		for _, m := range activityMarks {
			if strings.HasPrefix(l, m) {
				return l
			}
		}
	}
	return ""
}
