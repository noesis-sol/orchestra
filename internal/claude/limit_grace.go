package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A turn can end normally at the account's usage limit, after Claude Code's grace note (see the comment
// above limitWords): its Stop hook says no more than any turn's end, and the transcript tells the limit,
// in the notices of its automatic continue. A session whose turn ended with a Stop is limited while the
// transcript's last notice of the limit says it waits to continue by itself, with no message or prompt
// after it.

// limitTailBytes is how much of the end of a transcript is read for the notices of the usage limit, which
// come right after the turn's last message.
const limitTailBytes = 64 << 10

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
}

// recordedLimit is the usage limit the session that the record in root (an os.Root at the hooks'
// folder) names (sessionName) last waits on, by its transcript, its turn having ended at at with a Stop;
// zero when it waits on none, or the transcript can't be read.
func recordedLimit(root *os.Root, at time.Time) dispatch.UsageLimit {
	b, _, err := readIn(root, sessionName)
	var in hookInput
	if err != nil || json.Unmarshal(b, &in) != nil || !readable(in.V) {
		return dispatch.UsageLimit{}
	}
	s, ok := sessionOf(in)
	if !ok {
		return dispatch.UsageLimit{}
	}
	return transcriptLimit(s.Transcript, at)
}

// transcriptLimit is the usage limit the session whose transcript is at path waits on, as its last
// notices tell (see limitNotices), its turn having ended at at; zero when it waits on none, or the
// transcript can't be read.
func transcriptLimit(path string, at time.Time) dispatch.UsageLimit {
	f, err := os.Open(path)
	if err != nil {
		return dispatch.UsageLimit{}
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose
	b, err := readTail(f, limitTailBytes)
	if err != nil {
		return dispatch.UsageLimit{}
	}
	return limitNotices(b, at)
}

// limitNotices is the usage limit the transcript lines in b leave the session waiting on, its turn having
// ended at at: the last notice that the limit is reached and Claude Code continues by itself once it
// resets ("Usage limit reached · continuing automatically at 5:30am · esc to cancel"), when neither a
// notice that it continued, nor a prompt queued, nor a message came after it. A notice that it was
// cancelled, or that it waits for Enter, leaves the session waiting all the same. The limit resets at the
// time the notice gives (dispatch.LimitReset); zero when none is waited on.
func limitNotices(b []byte, at time.Time) dispatch.UsageLimit {
	var limit dispatch.UsageLimit
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, lineBytes+1) // readTail keeps no longer line
	for sc.Scan() {
		var e limitEntry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "user", "assistant":
			limit = dispatch.UsageLimit{}
		case "queue-operation":
			if e.Operation == "enqueue" {
				limit = dispatch.UsageLimit{}
			}
		case "system":
			var text string
			if e.Subtype != "informational" || json.Unmarshal(e.Content, &text) != nil {
				continue
			}
			switch {
			case strings.HasPrefix(text, limitReached):
				limit = dispatch.UsageLimit{At: at, Reset: dispatch.LimitReset(text, at), Message: text}
			case strings.HasPrefix(text, limitContinued), strings.HasPrefix(text, limitAvailable):
				limit = dispatch.UsageLimit{}
			}
		}
	}
	return limit
}
