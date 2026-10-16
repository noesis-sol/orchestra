package claude

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A terminal reads a Claude session's visible screen where its hooks can't tell: for the dialogs that
// run no hook, and for a permission prompt allowed, which runs none either (see Hooks.Settle).

// dialogs are what Claude Code shows of the dialogs it asks before a session starts, which run no
// hook: whether to trust the folder, and whether to use the project's MCP servers.
var dialogs = []string{"trust this folder", "mcp server found in this project", "mcp servers found in this project"}

// ShowsDialog reports whether the bottom of screen, a Claude session's visible screen, shows one of
// the dialogs it asks before a session starts: whether to trust the folder, or to use the project's
// MCP servers.
func ShowsDialog(screen string) bool {
	for _, l := range bottom(screen) {
		for _, d := range dialogs {
			if strings.Contains(l, d) {
				return true
			}
		}
	}
	return false
}

// ShowsPermission reports whether the bottom of screen, a Claude session's visible screen, shows its
// permission dialog: the question "Do you want to proceed?" (or to make an edit, create a file, …)
// or the first of its answers, "1. Yes".
func ShowsPermission(screen string) bool {
	for _, l := range bottom(screen) {
		l = strings.TrimLeft(l, " \t│|❯>") // its frame, and the cursor on the answer chosen
		if strings.HasPrefix(l, "do you want to ") || strings.HasPrefix(l, "1. yes") {
			return true
		}
	}
	return false
}

// bottom is the last 25 lines of screen, in lower case: where Claude Code shows a dialog.
func bottom(screen string) []string {
	lines := strings.Split(strings.ToLower(screen), "\n")
	return lines[max(0, len(lines)-25):]
}

// A turn that ends on the account's usage limit (the five-hour session limit, the weekly one, …), as
// Claude Code 2.1.301 ends it, seen on 2026-10-16 in a real claude whose requests went to a fake API that
// answered them with the 429 and the anthropic-ratelimit-unified-* headers of a subscription's limits:
//   - It runs StopFailure, not Stop. Its input has the common fields (session_id, transcript_path, cwd,
//     prompt_id, effort), error "rate_limit", no error_details, and last_assistant_message, what it
//     says of the limit: "You've hit your session limit · resets 7pm (Europe/Athens)". The limit is
//     the session limit, the weekly limit, the Opus, Sonnet or Fable limit, or just "limit" or "usage
//     limit"; " · progress saved" may follow. error is rate_limit for other refusals too, in other
//     words: "API Error: Server is temporarily limiting requests (not your usage limit) · …", or a
//     model under high load. Other API errors are of other kinds: server_error for a 500 or a 529, and
//     the others its hook's matcher lists (authentication_failed, billing_error, max_output_tokens, …).
//     Its code runs the hook for a subagent's turns as well; the hooks keep the main agent's only.
//   - It sends no Notification, nor in the 6 minutes after one for being idle at its prompt. Its
//     notification types quota_auto_resume_fired, _stale and _disabled tell of its automatic continue.
//   - Its screen shows the words under the prompt, "⎿  You've hit your session limit · resets 7:41pm
//     (Europe/Athens)", and then either its automatic continue, which waits for the reset and then sends
//     the prompt "Your claude.ai usage limit has reset. Continue the task …" (a line "⏺ Usage limit
//     reached · continuing automatically at 7:41pm · esc to cancel", and under the input box "⚠ Usage
//     limit reached · limit resets 7:41pm"), or, with autoContinueAtUsageLimit off in its settings, a
//     dialog that runs no hook: "What do you want to do?", "1. Stop and wait for limit to reset", "2.
//     Wait here, then continue automatically at 7:41pm", "3. Upgrade your plan". Once it is answered
//     only the words remain. A prompt sent while the limit holds ends the same way: "⏺ Usage limit
//     reached again · …" and the words again.
//
// The reset time is the hour in 12 hours, with its minutes unless on the hour, and the time zone it is
// in, the user's own unless set: "7pm (Europe/Athens)", "7:08pm (UTC)". One more than 24 hours away
// has its day before it, and its year when another year's: "Oct 19 at 5:08pm (Europe/Athens)" (another
// ICU may write "Oct 19, 5:08pm").

// limitWords are how Claude Code's words of the account's usage limit start: the limit hit, or the
// usage credits that go on past it spent. Its other words that start "You've", such as "You've
// reached your Fable limit." for a model that needs usage credits, tell no limit that resets.
var limitWords = []string{"You've hit your ", "You're out of usage credits"}

// usageLimit is the usage limit a turn that ended on an API error of the kind (StopFailure's error)
// with the message (its last_assistant_message) ended on, the turn having ended at at; false when the
// error is another, as a server that limits requests is.
func usageLimit(kind, message string, at time.Time) (dispatch.UsageLimit, bool) {
	if kind != "rate_limit" || !saysLimit(message) {
		return dispatch.UsageLimit{}, false
	}
	return dispatch.UsageLimit{At: at, Reset: resetTime(message, at), Message: message}, true
}

// saysLimit reports whether s, StopFailure's message, is Claude Code's words of the usage limit.
func saysLimit(s string) bool {
	for _, w := range limitWords {
		if strings.HasPrefix(s, w) {
			return true
		}
	}
	return false
}

// showsLimit reports whether l, a line of the screen, is Claude Code's words of the usage limit as it
// shows them, under the prompt: "  ⎿  You've hit your …". Its message quoting them, as a worker's own
// may, is not.
func showsLimit(l string) bool {
	rest, ok := strings.CutPrefix(strings.TrimLeft(l, " \u00a0"), "⎿")
	return ok && saysLimit(strings.TrimLeft(rest, " \u00a0"))
}

// resetWords match the reset time in Claude Code's words of the usage limit: its day (month, day and
// year), its hour and minutes, am or pm, and its time zone; "15:04", in 24 hours, is read as well.
var resetWords = regexp.MustCompile(`\bresets (?:([A-Z][a-z]{2}) (\d{1,2})(?:, (\d{4}))?(?:,| at) )?` +
	`(\d{1,2})(?::(\d{2}))?([ap]m)?(?: \(([^()]+)\))?`)

// resetTime reads the reset time in message, Claude Code's words of the usage limit, written at at;
// zero when it has none it can read. A time with no day is the next one at that hour after at, or
// just before it (an hour at most): Claude Code gives the day of one more than 24 hours away. A time
// zone Go doesn't know is taken for the local one.
func resetTime(message string, at time.Time) time.Time {
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

// ShowsUsageLimit reports whether screen, a Claude session's screen, shows its last turn ended on the
// account's usage limit: Claude Code's words of the limit under a prompt, with no message after them but
// its notice that it continues by itself once the limit resets (what it shows is above limitWords).
// It reads the whole screen given: the dialog Claude Code may show under the words takes 8 lines.
func ShowsUsageLimit(screen string) bool {
	lines := strings.Split(screen, "\n")
	last := -1
	for i, l := range lines {
		if showsLimit(l) {
			last = i
		}
	}
	if last < 0 {
		return false
	}
	for _, l := range lines[last+1:] {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "⏺") && !strings.Contains(l, "Usage limit reached") {
			return false // a message after the words: the session went on
		}
	}
	return true
}
