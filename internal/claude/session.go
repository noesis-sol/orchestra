package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Claude Code keeps each session's transcript as JSON lines in
// <config>/projects/<the working directory, encoded>/<session ID>.jsonl, and resumes the session
// with claude --resume <session ID> from that directory. The worker's SessionStart hook writes
// both to .orchestra/run/session.json (see hookSettings), so orchestra needn't encode the path
// itself, nor give the worker a session ID of its own: one /clear starts another, which the hook
// records too.

// sessionID is what a Claude Code session ID looks like: a UUID. It is typed on a command line
// (claude --resume <ID>), so nothing else is taken for one.
var sessionID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	tailBytes   = 512 << 10 // how much of the end of a transcript is read: its last lines are what count
	tailEntries = 40        // the transcript's last entries given as evidence
	tailChars   = 8000      // the most of them given, from the end
	textChars   = 400       // the most of one text or tool input
	errorChars  = 800       // the most of one tool error, which says what went wrong
	resultChars = 200       // the most of one tool result that isn't an error
)

// Session returns the Claude Code session the worker in worktree last started, or resumed, as its
// SessionStart hook recorded it, and false when there is none to resume: no record, an ID that isn't
// a session's, or a transcript that is gone. The record is the worker's to change, as all of
// .orchestra/run/ is, so the transcript must be where Claude Code keeps it, named after the session,
// and a plain file.
func (Reporter) Session(worktree string) (dispatch.Session, bool) {
	b, _, err := readRun(worktree, sessionName)
	if err != nil {
		return dispatch.Session{}, false
	}
	var in struct {
		ID         string `json:"session_id"`
		Transcript string `json:"transcript_path"`
	}
	if json.Unmarshal(b, &in) != nil || !sessionID.MatchString(in.ID) {
		return dispatch.Session{}, false
	}
	p := in.Transcript
	if !filepath.IsAbs(p) || filepath.Base(p) != in.ID+".jsonl" ||
		filepath.Base(filepath.Dir(filepath.Dir(p))) != "projects" {
		return dispatch.Session{}, false
	}
	if fi, err := os.Lstat(p); err != nil || !fi.Mode().IsRegular() {
		return dispatch.Session{}, false
	}
	return dispatch.Session{ID: in.ID, Transcript: p}, true
}

// TranscriptTail returns the end of the transcript of the worker's session in worktree, as text: its
// last messages, the tools it called and what they returned, errors in full; "" when it can't be
// read.
func (r Reporter) TranscriptTail(worktree string) string {
	s, ok := r.Session(worktree)
	if !ok {
		return ""
	}
	f, err := os.Open(s.Transcript)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose
	b, err := readTail(f, tailBytes)
	if err != nil {
		return ""
	}
	return transcriptTail(b)
}

// readTail reads the last n bytes of f, from the start of a line.
func readTail(f *os.File, n int64) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	from := max(fi.Size()-n, 0)
	b, err := io.ReadAll(io.NewSectionReader(f, from, fi.Size()-from))
	if err != nil {
		return nil, err
	}
	if from > 0 {
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:] // the first line is cut
		}
	}
	return b, nil
}

// entry is one line of a transcript, as far as the evidence needs it.
type entry struct {
	Type    string `json:"type"` // user, assistant, system, …
	Content string `json:"content"`
	Message struct {
		Content json.RawMessage `json:"content"` // a string, or blocks
	} `json:"message"`
}

// block is one part of a message's content.
type block struct {
	Type    string          `json:"type"` // text, thinking, tool_use, tool_result
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"` // a tool result's: a string, or text blocks
	IsError bool            `json:"is_error"`
}

// transcriptTail renders the transcript lines in b, one line of text for each message, tool call and
// tool result, and returns the last tailEntries of them, cut to tailChars from the end. A line that
// isn't JSON is skipped, as is the model's thinking.
func transcriptTail(b []byte) string {
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, tailBytes+1) // b is no longer, so no line is
	for sc.Scan() {
		var e entry
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		lines = append(lines, entryLines(e)...)
	}
	lines = lines[max(len(lines)-tailEntries, 0):]
	out := strings.Join(lines, "\n")
	if len(out) > tailChars {
		out = out[len(out)-tailChars:]
		if i := strings.IndexByte(out, '\n'); i >= 0 {
			out = out[i+1:]
		}
	}
	return out
}

// entryLines renders one transcript entry.
func entryLines(e entry) []string {
	switch e.Type {
	case "user", "assistant":
	case "system":
		if t := oneLine(e.Content, errorChars); t != "" {
			return []string{"system: " + t}
		}
		return nil
	default:
		return nil // summaries, snapshots and the like say nothing of what the worker did
	}
	var text string
	if json.Unmarshal(e.Message.Content, &text) == nil {
		if t := oneLine(text, textChars); t != "" {
			return []string{e.Type + ": " + t}
		}
		return nil
	}
	var blocks []block
	if json.Unmarshal(e.Message.Content, &blocks) != nil {
		return nil
	}
	var lines []string
	for _, bl := range blocks {
		switch bl.Type {
		case "text":
			if t := oneLine(bl.Text, textChars); t != "" {
				lines = append(lines, e.Type+": "+t)
			}
		case "tool_use":
			lines = append(lines, "tool "+bl.Name+": "+toolInput(bl.Input))
		case "tool_result":
			if bl.IsError {
				lines = append(lines, "tool error: "+oneLine(resultText(bl.Content), errorChars))
			} else {
				lines = append(lines, "tool result: "+oneLine(resultText(bl.Content), resultChars))
			}
		}
	}
	return lines
}

// toolInput is what a tool was called with: a command, a file, a pattern, or else its input as JSON.
func toolInput(raw json.RawMessage) string {
	var in map[string]any
	if json.Unmarshal(raw, &in) != nil {
		return oneLine(string(raw), textChars)
	}
	for _, key := range []string{"command", "file_path", "notebook_path", "pattern", "url", "query", "prompt"} {
		if s, ok := in[key].(string); ok && s != "" {
			return oneLine(s, textChars)
		}
	}
	return oneLine(string(raw), textChars)
}

// resultText is a tool result's text: its content as a string, or its text blocks joined.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, bl := range blocks {
		if bl.Type == "text" {
			parts = append(parts, bl.Text)
		}
	}
	return strings.Join(parts, " ")
}

// oneLine is s on one line, its runs of white space each one space, cut to at most n bytes with an
// ellipsis, never inside a character.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return fmt.Sprintf("%s… (%d bytes)", s[:cut], len(s))
}
