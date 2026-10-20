package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// A turn can end normally at the account's usage limit, after Claude Code's grace note (see claudescreen's
// comment above limitWords): its Stop hook says no more than any turn's end, and the transcript tells the limit,
// in the notices of its automatic continue. A session whose turn ended with a Stop is limited while the
// transcript's last notice of the limit says it waits to continue by itself, with no message or prompt
// after it. Claude Code runs the turn's Stop hooks before it logs the turn's end and that notice (in a
// run of 2026-10-17 the notice came 10ms after the hooks' stop_hook_summary, which waits on the user's own
// Stop hooks too), so until the transcript logs the turn's end (see endLogged) its Stop doesn't tell
// whether the turn ended at the limit: the turn counts as going on, for turnEndWait at most.

// limitTailBytes is how much of the end of a transcript is read for the notices of the usage limit, which
// come right after the turn's last message.
const limitTailBytes = 64 << 10

// turnEndWait is how long after its Stop hook a turn whose end the transcript has yet to log counts as
// going on (see endLogged): the user's own Stop hooks, which Claude Code waits on before it logs it, may
// be slow, and a transcript that never logs it, as an older Claude Code's might not, only holds the
// turn's end back this long.
const turnEndWait = time.Minute

// summaryLag is how long after the stop_hook_summary entry, Claude Code's note that a turn's Stop hooks
// have run, the turn's end counts as logged without its turn_duration entry: the notice of the usage
// limit comes a moment after the summary (10ms in the run above), and the turn_duration after the notice;
// Claude Code doesn't log a turn_duration for every turn.
const summaryLag = time.Second

// The notices of Claude Code's automatic continue (system entries of subtype informational) that tell
// the limit reached and the continue armed, and those that tell it fired: the session goes on.
const (
	limitReached   = "Usage limit reached"
	limitContinued = "Usage limit reset"
	limitAvailable = "Usage limit available again"
)

// limitEntry is a transcript line, as far as the notices of the usage limit need it.
type limitEntry struct {
	Type      string          `json:"type"`      // user, assistant, system, queue-operation, …
	Subtype   string          `json:"subtype"`   // a system entry's: informational for the notices
	Operation string          `json:"operation"` // a queue-operation's: enqueue, dequeue, …
	Content   json.RawMessage `json:"content"`   // a system entry's text
	Timestamp string          `json:"timestamp"` // when it was logged, in RFC 3339
}

// recordedStop is what the transcript of the session that the record in root (an os.Root at the hooks'
// folder) names (sessionName) tells of its turn, which ended at at with a Stop, at now (see stopTold);
// nothing when the transcript can't be read.
func recordedStop(root *os.Root, at, now time.Time) (limit agentio.UsageLimit, ending bool) {
	b, _, err := readIn(root, sessionName)
	var in hookInput
	if err != nil || json.Unmarshal(b, &in) != nil || !readable(in.V) {
		return agentio.UsageLimit{}, false
	}
	s, ok := sessionOf(in)
	if !ok {
		return agentio.UsageLimit{}, false
	}
	return stopTold(s.Transcript, at, now)
}

// stopTold is what the transcript at path tells, at now, of the session's turn, which ended at at with a
// Stop: the usage limit it waits on, as its last notices tell (see limitNotices), and whether the turn
// still counts as going on (ending), its end not yet logged (see endLogged) less than turnEndWait after
// the Stop. Nothing when the transcript can't be read.
func stopTold(path string, at, now time.Time) (limit agentio.UsageLimit, ending bool) {
	f, err := os.Open(path)
	if err != nil {
		return agentio.UsageLimit{}, false
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose
	b, err := readTail(f, limitTailBytes)
	if err != nil {
		return agentio.UsageLimit{}, false
	}
	if limit = limitNotices(b, at); limit.Reached() {
		return limit, false
	}
	return limit, now.Sub(at) < turnEndWait && !endLogged(b, now)
}

// endLogged reports whether the transcript lines in b log, at now, the end of the turn whose last message
// they have: the turn_duration entry after it, or the stop_hook_summary written summaryLag before now (or
// at a time not given). Lines with no message have no turn to wait on.
func endLogged(b []byte, now time.Time) bool {
	logged := true
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, lineBytes+1) // readTail keeps no longer line
	for sc.Scan() {
		var e limitEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch {
		case e.Type == "assistant":
			logged = false
		case e.Type != "system":
		case e.Subtype == "turn_duration":
			logged = true
		case e.Subtype == "stop_hook_summary":
			t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			logged = logged || err != nil || now.Sub(t) >= summaryLag
		}
	}
	return logged
}

// limitNotices is the usage limit the transcript lines in b leave the session waiting on, its turn having
// ended at at: the last notice that the limit is reached and Claude Code continues by itself once it
// resets ("Usage limit reached · continuing automatically at 5:30am · esc to cancel"), when neither a
// notice that it continued, nor a prompt queued, nor a message came after it. A notice that it was
// cancelled, or that it waits for Enter, leaves the session waiting all the same. The limit resets at the
// time the notice gives (agentio.LimitReset); zero when none is waited on.
func limitNotices(b []byte, at time.Time) agentio.UsageLimit {
	var limit agentio.UsageLimit
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, lineBytes+1) // readTail keeps no longer line
	for sc.Scan() {
		var e limitEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "user", "assistant":
			limit = agentio.UsageLimit{}
		case "queue-operation":
			if e.Operation == "enqueue" {
				limit = agentio.UsageLimit{}
			}
		case "system":
			var text string
			if e.Subtype != "informational" || json.Unmarshal(e.Content, &text) != nil {
				continue
			}
			switch {
			case strings.HasPrefix(text, limitReached):
				limit = agentio.UsageLimit{At: at, Reset: agentio.LimitReset(text, at), Message: text}
			case strings.HasPrefix(text, limitContinued), strings.HasPrefix(text, limitAvailable):
				limit = agentio.UsageLimit{}
			}
		}
	}
	return limit
}
