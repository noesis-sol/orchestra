package claude

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata" // the zone the fixtures' words name, on a machine with no time zone database

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// What Claude Code 2.1.301 gave its StopFailure hook as a turn ended on an API error, captured on 2026-10-16
// from a real claude -p whose requests went to a fake API (see claudescreen's comment above limitWords): 429s with the
// anthropic-ratelimit-unified-* headers of a subscription's session, weekly and Opus limits and of its limit
// with extra usage refused, a 429 without them, a 529 and a 500. Only the paths are shortened.
const (
	failureSessionLimit    = `{"session_id":"c7ef3be7-5eae-48aa-8234-e122429e424c","transcript_path":"/Users/dev/.claude/projects/-Users-dev-work/c7ef3be7-5eae-48aa-8234-e122429e424c.jsonl","cwd":"/Users/dev/work","prompt_id":"e73545a7-ccbe-432a-9d99-009e5c61e056","effort":{"level":"medium"},"hook_event_name":"StopFailure","error":"rate_limit","last_assistant_message":"You've hit your session limit · resets 7pm (Europe/Athens)"}`
	failureWeeklyLimit     = `{"session_id":"1df94958-56bc-48ab-b63b-88ce7963bac2","transcript_path":"/Users/dev/.claude/projects/-Users-dev-work/1df94958-56bc-48ab-b63b-88ce7963bac2.jsonl","cwd":"/Users/dev/work","prompt_id":"2554ed67-c87c-4ca0-a7b4-6e837f9dcf8b","effort":{"level":"medium"},"hook_event_name":"StopFailure","error":"rate_limit","last_assistant_message":"You've hit your weekly limit · resets Oct 19 at 5:08pm (Europe/Athens)"}`
	failureOpusLimit       = `{"session_id":"5df08e39-5a9d-42aa-b235-885823b373a3","transcript_path":"/Users/dev/.claude/projects/-Users-dev-work/5df08e39-5a9d-42aa-b235-885823b373a3.jsonl","cwd":"/Users/dev/work","prompt_id":"67a9a3f2-b05b-490f-8a59-4d71d81a521a","effort":{"level":"medium"},"hook_event_name":"StopFailure","error":"rate_limit","last_assistant_message":"You've hit your Opus limit · resets 7:08pm (Europe/Athens)"}`
	failureLimit           = `{"session_id":"f22dc7c0-ac77-4802-93dc-4cc940b5569f","transcript_path":"/Users/dev/.claude/projects/-Users-dev-work/f22dc7c0-ac77-4802-93dc-4cc940b5569f.jsonl","cwd":"/Users/dev/work","prompt_id":"5a4b7467-b092-403c-ba02-3bbb06cceff5","effort":{"level":"medium"},"hook_event_name":"StopFailure","error":"rate_limit","last_assistant_message":"You've hit your limit · resets 8:38pm (Europe/Athens)"}`
	failureRequestsLimited = `{"session_id":"6fdeafb2-12fb-4480-8409-e837632f7372","transcript_path":"/Users/dev/.claude/projects/-Users-dev-work/6fdeafb2-12fb-4480-8409-e837632f7372.jsonl","cwd":"/Users/dev/work","prompt_id":"b76ebb46-3d0f-46b3-a133-582b86fbc27c","effort":{"level":"medium"},"hook_event_name":"StopFailure","error":"rate_limit","last_assistant_message":"API Error: Server is temporarily limiting requests (not your usage limit) · Number of request tokens has exceeded your per-minute rate limit"}`
	failureOverloaded      = `{"session_id":"9d800916-2769-4cd5-90ef-6e3b12bbb65e","transcript_path":"/Users/dev/.claude/projects/-Users-dev-work/9d800916-2769-4cd5-90ef-6e3b12bbb65e.jsonl","cwd":"/Users/dev/work","prompt_id":"c7a11487-750e-45ab-b472-8b9c7d3f2404","effort":{"level":"medium"},"hook_event_name":"StopFailure","error":"server_error","last_assistant_message":"API Error: 529 Overloaded. This is a server-side issue, usually temporary — try again in a moment. If it persists, check your inference gateway (127.0.0.1:18766)."}`
	failureServerError     = `{"session_id":"d6f8b2f7-ddf6-427f-808b-47b5a781924c","transcript_path":"/Users/dev/.claude/projects/-Users-dev-work/d6f8b2f7-ddf6-427f-808b-47b5a781924c.jsonl","cwd":"/Users/dev/work","prompt_id":"2e379a51-68ab-4198-a600-244a8bfc1755","effort":{"level":"medium"},"hook_event_name":"StopFailure","error":"server_error","last_assistant_message":"API Error: 500 Internal server error. This is a server-side issue, usually temporary — try again in a moment. If it persists, check your inference gateway (127.0.0.1:18766)."}`
)

// athens is clock, a date and time, in the time zone the fixtures were captured in, which their words name.
func athens(t *testing.T, clock string) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Athens")
	if err != nil {
		t.Fatal(err)
	}
	at, err := time.ParseInLocation(time.DateTime, clock, loc)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

// stepFailure is Claude Code's StopFailure, input being its hook's.
func stepFailure(input string) hookStep { return stepAt("StopFailure", input) }

// playHooksAt is playHooks, its records then moved in time for the turn's record to be dated at.
func playHooksAt(t *testing.T, wt string, at time.Time, steps ...hookStep) {
	t.Helper()
	playHooks(t, wt, steps)
	records := Records(wt, "")
	turn, err := os.Stat(filepath.Join(records, turnName))
	if err != nil {
		t.Fatal(err)
	}
	by := at.Sub(turn.ModTime())
	entries, err := os.ReadDir(records)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		p := filepath.Join(records, e.Name())
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, fi.ModTime().Add(by), fi.ModTime().Add(by)); err != nil {
			t.Fatal(err)
		}
	}
}

// A turn that ends on the account's usage limit, as Claude Code tells its StopFailure hook, leaves the
// worker limited, with the time the limit resets read from Claude Code's words, both in the state its
// hooks give a terminal and in its last report the loop reads; until a prompt starts another turn,
// however long it waits at its prompt.
func TestStopFailureOnTheUsageLimitLimitsTheWorker(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, input, captured, reset, message string
	}{
		{"session limit", failureSessionLimit, "2026-10-16 16:37:34", "2026-10-16 19:00:00",
			"You've hit your session limit · resets 7pm (Europe/Athens)"},
		{"weekly limit", failureWeeklyLimit, "2026-10-16 16:38:29", "2026-10-19 17:08:00",
			"You've hit your weekly limit · resets Oct 19 at 5:08pm (Europe/Athens)"},
		{"Opus limit", failureOpusLimit, "2026-10-16 16:38:32", "2026-10-16 19:08:00",
			"You've hit your Opus limit · resets 7:08pm (Europe/Athens)"},
		{"limit with extra usage refused", failureLimit, "2026-10-16 16:38:36", "2026-10-16 20:38:00",
			"You've hit your limit · resets 8:38pm (Europe/Athens)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			at, reset := athens(t, c.captured), athens(t, c.reset)
			playHooksAt(t, wt, at, stepSessionStart("startup"), stepPrompt, stepUseBash, stepUsedIt, stepFailure(c.input))
			want := agentio.UsageLimit{At: at, Reset: reset, Message: c.message}
			records := Records(wt, "")
			h := ReadHooks(records, "")
			if h.State != agentio.StateLimited || !sameLimit(h.Limit, want) {
				t.Errorf("hooks: %s %+v, want limited %+v", h.State, h.Limit, want)
			}
			// A terminal that reads the screen where the hooks can't tell has nothing to read here.
			if st := h.Settle(func() (string, error) { t.Error("the screen was read"); return "", nil }); st != h.State {
				t.Errorf("settled: %s", st)
			}
			u, ok := Reporter{}.LastToolUse(wt)
			if !ok || u.Event != "StopFailure" || !sameLimit(u.Limit, want) || !u.Stopped.IsZero() {
				t.Errorf("last report: %+v %v, want the StopFailure with %+v", u, ok, want)
			}

			// Waiting at its prompt, which Claude Code may note, leaves it limited.
			settings := readSettings(t, filepath.Join(records, settingsName))
			runHooks(t, settings, "Notification", stepNotification(idlePrompt).input)
			if h := ReadHooks(records, ""); h.State != agentio.StateLimited || !sameLimit(h.Limit, want) {
				t.Errorf("idle at its prompt since: %s %+v, want limited", h.State, h.Limit)
			}
			// A prompt, Claude Code's own once the limit resets or the loop's, starts another turn.
			runHooks(t, settings, "UserPromptSubmit", stepPrompt.input)
			if h := ReadHooks(records, ""); h.State == agentio.StateLimited || h.Limit.Reached() {
				t.Errorf("prompted since: %s %+v, want no limit", h.State, h.Limit)
			}
			if u, _ := (Reporter{}).LastToolUse(wt); u.Limit.Reached() {
				t.Errorf("last report once prompted: %+v, want no limit", u.Limit)
			}
		})
	}
}

// sameLimit reports whether a and b are the same limit, their times the same instants.
func sameLimit(a, b agentio.UsageLimit) bool {
	return a.At.Equal(b.At) && a.Reset.Equal(b.Reset) && a.Message == b.Message
}

// A turn that ends on another API error, a rate limit that isn't the account's usage limit among
// them, is over, with no usage limit; a subagent's StopFailure ends no turn of the worker's.
func TestOtherStopFailuresOnlyEndTheTurn(t *testing.T) {
	t.Parallel()
	for name, input := range map[string]string{
		"requests limited": failureRequestsLimited, "overloaded": failureOverloaded, "server error": failureServerError,
		"usage limit's words with another error": strings.Replace(failureSessionLimit, `"rate_limit"`, `"billing_error"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			if got := playHooks(t, wt, []hookStep{stepSessionStart("startup"), stepPrompt, stepFailure(input)}); got !=
				agentio.StateIdle {
				t.Errorf("state: %s, want idle", got)
			}
			if h := ReadHooks(Records(wt, ""), ""); h.Limit.Reached() {
				t.Errorf("hooks' limit: %+v", h.Limit)
			}
			if u, ok := (Reporter{}).LastToolUse(wt); !ok || u.Event != "StopFailure" || u.Limit.Reached() {
				t.Errorf("last report: %+v %v, want the StopFailure with no limit", u, ok)
			}
		})
	}

	t.Run("a subagent's", func(t *testing.T) {
		t.Parallel()
		wt := t.TempDir()
		sub := strings.Replace(failureSessionLimit, `{`, `{"agent_id":"a1","agent_type":"verifier",`, 1)
		if got := playHooks(t, wt, []hookStep{stepSessionStart("startup"), stepPrompt, stepSubStart, stepUseBash,
			stepFailure(sub)}); got != agentio.StateWorking {
			t.Errorf("state: %s, want working", got)
		}
		if u, _ := (Reporter{}).LastToolUse(wt); u.Event != "PreToolUse" || u.Limit.Reached() {
			t.Errorf("last report: %+v, want the tool use before it", u)
		}
	})
}

// Claude Code runs the StopFailure hook as one of the others.
func TestStopFailureHookIsInstalled(t *testing.T) {
	t.Parallel()
	hooks := reportingSettings(t, t.TempDir(), false)["hooks"].(map[string]any)
	if _, ok := hooks["StopFailure"]; !ok {
		t.Error("no StopFailure hook")
	}
}

// The StopFailure record is in the current format, and read as format 1 with no format given; in a
// newer format it is unreadable, as the log says once, and tells no limit.
func TestUsageLimitRecordFormats(t *testing.T) {
	wt := t.TempDir()
	at := athens(t, "2026-10-16 16:37:34")
	playHooksAt(t, wt, at, stepSessionStart("startup"), stepNotification("auth_success"), stepPrompt,
		stepFailure(failureSessionLimit))
	records := Records(wt, "")
	for _, name := range []string{activityName, turnName} {
		if got := recordFormats(t, records)[name]; len(got) != 1 || got[0] != recordFormat {
			t.Errorf("%s in formats %v", name, got)
		}
	}
	var (
		mu    sync.Mutex
		lines []string
	)
	r := NewReporter(func(text string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, text)
	})
	reset := athens(t, "2026-10-16 19:00:00")
	limited := func(t *testing.T) {
		t.Helper()
		if h := ReadHooks(records, ""); h.State != agentio.StateLimited || !h.Limit.Reset.Equal(reset) {
			t.Errorf("hooks: %s %+v", h.State, h.Limit)
		}
		if u, ok := r.LastToolUse(wt); !ok || !u.Limit.Reset.Equal(reset) {
			t.Errorf("last report: %+v %v", u, ok)
		}
	}
	t.Run("current format", limited)
	rewriteRecords(t, records, `{"v":1,`, `{`)
	t.Run("no format", limited)
	if len(lines) > 0 {
		t.Errorf("logged: %q", lines)
	}

	rewriteRecords(t, records, `{`, `{"v":2,`)
	for range 3 {
		if h := ReadHooks(records, ""); h.State == agentio.StateLimited || h.Limit.Reached() {
			t.Errorf("hooks read from a newer format: %s %+v", h.State, h.Limit)
		}
		if u, ok := r.LastToolUse(wt); ok || u.Limit.Reached() {
			t.Errorf("last report read from a newer format: %+v %v", u, ok)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "format 2") {
		t.Errorf("logged: %q", lines)
	}
}

// Only Claude Code's words of the account's usage limit, with a rate_limit error, tell one.
func TestUsageLimitIsClaudeCodesWordsForIt(t *testing.T) {
	t.Parallel()
	at := athens(t, "2026-10-16 16:37:34")
	for _, c := range []struct {
		kind, message string
		want          bool
	}{
		{"rate_limit", "You've hit your session limit · resets 7pm (Europe/Athens)", true},
		{"rate_limit", "You've hit your weekly limit · resets Oct 19 at 5:08pm (Europe/Athens) · progress saved", true},
		{"rate_limit", "You're out of usage credits · resets 7pm (Europe/Athens)", true},
		{"rate_limit", "You've hit your monthly spend limit · raise it at https://claude.ai/settings/usage", true},
		{"rate_limit", "API Error: Server is temporarily limiting requests (not your usage limit) · Rate limited", false},
		{"rate_limit", "Opus is experiencing high load. Switch to Sonnet.", false},
		{"rate_limit", "You've reached your Fable limit. Switch to another model to continue.", false},
		{"server_error", "You've hit your session limit · resets 7pm (Europe/Athens)", false},
		{"", "", false},
	} {
		l, ok := usageLimit(c.kind, c.message, at)
		if ok != c.want || l.Reached() != c.want || ok && (!l.At.Equal(at) || l.Message != c.message) {
			t.Errorf("usageLimit(%q, %q) = %+v %v, want %v", c.kind, c.message, l, ok, c.want)
		}
	}
}

// The reset time is read from Claude Code's words in the time zone they name, the day it gives or the
// next at that hour after the words were written, or just before: a limit that has just reset.
func TestResetTimeFromClaudeCodesWords(t *testing.T) {
	t.Parallel()
	at := athens(t, "2026-10-16 16:37:34")
	utc := func(clock string) time.Time {
		u, err := time.Parse(time.DateTime, clock)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	local := func(clock string) time.Time {
		l, err := time.ParseInLocation(time.DateTime, clock, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	for _, c := range []struct {
		message string
		at      time.Time
		want    time.Time
	}{
		{"You've hit your session limit · resets 7pm (Europe/Athens)", at, athens(t, "2026-10-16 19:00:00")},
		{"You've hit your session limit · resets 7:41pm (Europe/Athens) · progress saved", at,
			athens(t, "2026-10-16 19:41:00")},
		{"resets 4pm (Europe/Athens)", at, athens(t, "2026-10-16 16:00:00")}, // reset a moment ago
		{"resets 3:30pm (Europe/Athens)", at, athens(t, "2026-10-17 15:30:00")},
		{"resets 12am (Europe/Athens)", at, athens(t, "2026-10-17 00:00:00")},
		{"resets 12:15pm (UTC)", at, utc("2026-10-17 12:15:00")},
		{"resets 2:15pm (UTC)", at, utc("2026-10-16 14:15:00")},
		{"resets 17:08 (Europe/Athens)", at, athens(t, "2026-10-16 17:08:00")},
		{"You've hit your weekly limit · resets Oct 19 at 5:08pm (Europe/Athens)", at, athens(t, "2026-10-19 17:08:00")},
		{"resets Oct 19, 5:08pm (Europe/Athens)", at, athens(t, "2026-10-19 17:08:00")},
		{"resets Jan 2, 2027 at 3pm (Europe/Athens)", at, athens(t, "2027-01-02 15:00:00")},
		{"resets Jan 2, 2027, 3pm (Europe/Athens)", at, athens(t, "2027-01-02 15:00:00")},
		{"resets 7pm", local("2026-10-16 12:00:00"), local("2026-10-16 19:00:00")},
		{"resets 7pm (Mars/Olympus_Mons)", local("2026-10-16 12:00:00"), local("2026-10-16 19:00:00")},
		{"resets 7 (Europe/Athens)", at, time.Time{}},
		{"resets 13pm (Europe/Athens)", at, time.Time{}},
		{"resets 25:00 (Europe/Athens)", at, time.Time{}},
		{"resets Oct 32 at 5pm (Europe/Athens)", at, time.Time{}},
		{"resets soon", at, time.Time{}},
		{"You've hit your monthly spend limit · raise it at https://claude.ai/settings/usage", at, time.Time{}},
	} {
		if got := agentio.LimitReset(c.message, c.at); !got.Equal(c.want) {
			t.Errorf("LimitReset(%q) = %v, want %v", c.message, got, c.want)
		}
	}
}
