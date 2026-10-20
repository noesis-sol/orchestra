package claude

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The end of a turn that ended normally at the account's usage limit, after Claude Code's grace note, and
// what came after it, as a worker's transcript has them from Claude Code 2.1.302 on 2026-10-17, its lines cut
// to the fields read and its messages shortened.
const (
	graceNote = `{"type":"user","message":{"role":"user","content":"[Usage limit reached; a short grace allowance ` +
		`remains, then this turn is cut off without warning. Judge what's left: if it's genuinely only a few steps, ` +
		`finish it properly, then stop and say so without extra polishing. …]"},"isMeta":true,` +
		`"timestamp":"2026-10-17T01:24:45.832Z"}`
	graceWrapUp = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Your usage ` +
		`limit was reached partway through the ticket, so I stopped, saved the work on the branch and deferred ` +
		`the ticket."}]},"timestamp":"2026-10-17T01:25:07.301Z"}`
	graceStopSummary = `{"type":"system","subtype":"stop_hook_summary","level":"suggestion",` +
		`"timestamp":"2026-10-17T01:25:07.374Z"}`
	graceArmed = `{"type":"system","subtype":"informational","content":"Usage limit reached · continuing ` +
		`automatically at 5:30am · esc to cancel","isMeta":false,"level":"notice","timestamp":"2026-10-17T01:25:07.385Z"}`
	graceDuration = `{"type":"system","subtype":"turn_duration","isMeta":false,"timestamp":"2026-10-17T01:25:07.392Z"}`
	graceEnqueued = `{"type":"queue-operation","operation":"enqueue","content":"Your claude.ai usage limit has reset. ` +
		`Continue the task you were working on when the limit was reached; do not repeat work that is already ` +
		`complete.","timestamp":"2026-10-17T02:31:37.627Z"}`
	graceDequeued = `{"type":"queue-operation","operation":"dequeue","timestamp":"2026-10-17T02:31:37.663Z"}`
	graceFired    = `{"type":"system","subtype":"informational","content":"Usage limit reset · continuing ` +
		`automatically","isMeta":false,"level":"notice","timestamp":"2026-10-17T02:31:37.628Z"}`
	graceContinue = `{"type":"user","message":{"role":"user","content":"Your claude.ai usage limit has reset. ` +
		`Continue the task you were working on when the limit was reached; do not repeat work that is already ` +
		`complete."},"isMeta":true,"timestamp":"2026-10-17T02:31:37.686Z"}`
)

// informational is a notice of Claude Code's automatic continue in a transcript, saying text.
func informational(text string) string {
	return `{"type":"system","subtype":"informational","content":"` + text + `","level":"notice"}`
}

// graceArmedWords is what the notice that the automatic continue is armed says, after the grace note.
const graceArmedWords = "Usage limit reached · continuing automatically at 5:30am · esc to cancel"

// The transcript's last notice of the usage limit tells a session waiting on it, when nothing came after it
// but other notices of the automatic continue that leave it waiting; the limit resets at the time the notice
// gives, in the user's time zone. A message, a prompt queued, or the notice that it continued, ends the wait.
func TestTranscriptNoticesTellTheUsageLimit(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 16, 4, 25, 7, 0, time.Local)
	armed := dispatch.UsageLimit{At: at, Reset: time.Date(2026, 10, 16, 5, 30, 0, 0, time.Local),
		Message: graceArmedWords}
	ended := []string{graceNote, graceWrapUp, graceStopSummary, graceArmed, graceDuration}
	then := func(lines ...string) []string { return append(append([]string{}, ended...), lines...) }
	for _, c := range []struct {
		name  string
		lines []string
		want  dispatch.UsageLimit
	}{
		{"ended after the grace note", ended, armed},
		{"Escape cancelled the automatic continue", then(informational(
			"Automatic continue cancelled · /rate-limit-options to re-arm")), armed},
		{"waiting for Enter once it reset", then(informational("Usage limit has reset · press enter to continue")),
			armed},
		{"turned off", then(informational("Automatic continue was turned off · this task will not resume on its own")),
			armed},
		{"refused again", then(graceContinue, informational(
			"Usage limit reached again · continuing automatically at Oct 19 at 5:08pm · esc to cancel")),
			dispatch.UsageLimit{At: at, Reset: time.Date(2026, 10, 19, 17, 8, 0, 0, time.Local), Message: "Usage limit " +
				"reached again · continuing automatically at Oct 19 at 5:08pm · esc to cancel"}},
		{"continuing shortly", then(graceContinue, informational(
			"Usage limit reached again · continuing automatically shortly · esc to cancel")),
			dispatch.UsageLimit{At: at, Message: "Usage limit reached again · continuing automatically shortly · " +
				"esc to cancel"}},
		{"its continue queued", then(graceEnqueued, graceDequeued), dispatch.UsageLimit{}},
		{"continued by itself", then(graceEnqueued, graceDequeued, graceFired, graceContinue), dispatch.UsageLimit{}},
		{"continued at once", then(informational("Usage limit available again · continuing now")),
			dispatch.UsageLimit{}},
		{"told to carry on", then(`{"type":"user","message":{"role":"user","content":"Orchestra: carry on."}}`),
			dispatch.UsageLimit{}},
		{"a turn's end with no limit", []string{graceWrapUp, graceStopSummary, graceDuration}, dispatch.UsageLimit{}},
		{"lines that aren't JSON", then("{", "not JSON"), armed},
		{"no transcript", nil, dispatch.UsageLimit{}},
	} {
		got := limitNotices([]byte(strings.Join(c.lines, "\n")), at)
		if !sameLimit(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

// A turn that ends with a Stop, its transcript then saying that Claude Code continues by itself once the
// limit resets, leaves the worker limited, in the state its hooks give a terminal and in its last report the
// loop reads, until a prompt starts another turn; one whose transcript says nothing of the limit is idle.
func TestStopAfterTheGraceNoteLimitsTheWorker(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 16, 4, 25, 7, 0, time.Local)
	want := dispatch.UsageLimit{At: at, Reset: time.Date(2026, 10, 16, 5, 30, 0, 0, time.Local),
		Message: graceArmedWords}
	wt := t.TempDir()
	p := transcriptAt(t, t.TempDir(), testID, graceNote, graceWrapUp, graceStopSummary, graceArmed, graceDuration)
	playHooksAt(t, wt, at, stepAt("SessionStart", sessionInput(testID, p)), stepPrompt, stepUseBash, stepUsedIt,
		stepStop)
	records := Records(wt, "")
	if h := ReadHooks(records, ""); h.State != dispatch.StateLimited || !sameLimit(h.Limit, want) {
		t.Errorf("hooks: %s %+v, want limited %+v", h.State, h.Limit, want)
	}
	u, ok := Reporter{}.LastToolUse(wt)
	if !ok || u.Event != "Stop" || !sameLimit(u.Limit, want) || !u.Stopped.IsZero() {
		t.Errorf("last report: %+v %v, want the Stop with %+v", u, ok, want)
	}

	// Waiting at its prompt, which Claude Code may note, leaves it limited.
	settings := readSettings(t, filepath.Join(records, settingsName))
	runHooks(t, settings, "Notification", stepNotification(idlePrompt).input)
	if h := ReadHooks(records, ""); h.State != dispatch.StateLimited {
		t.Errorf("idle at its prompt since: %s, want limited", h.State)
	}
	// Claude Code's own continue starts another turn.
	transcriptAt(t, filepath.Dir(filepath.Dir(filepath.Dir(p))), testID, graceNote, graceWrapUp, graceStopSummary,
		graceArmed, graceDuration, graceEnqueued, graceDequeued, graceFired, graceContinue)
	runHooks(t, settings, "UserPromptSubmit", stepPrompt.input)
	if h := ReadHooks(records, ""); h.State != dispatch.StateWorking || h.Limit.Reached() {
		t.Errorf("continued: %s %+v, want working", h.State, h.Limit)
	}
	if u, _ := (Reporter{}).LastToolUse(wt); u.Limit.Reached() {
		t.Errorf("last report once continued: %+v, want no limit", u.Limit)
	}
	// Its turn's end, the limit far behind it, is only that.
	runHooks(t, settings, "Stop", stepStop.input)
	if h := ReadHooks(records, ""); h.State != dispatch.StateIdle {
		t.Errorf("its next turn ended: %s, want idle", h.State)
	}
	if u, _ := (Reporter{}).LastToolUse(wt); u.Limit.Reached() || u.Stopped.IsZero() {
		t.Errorf("last report at its next turn's end: %+v, want a Stop", u)
	}
}
