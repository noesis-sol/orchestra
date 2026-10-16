package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The worker's hooks are orchestra's own binary, the one that launched the worker, run as 'orchestra hook
// <event> <folder>' (hookSettings): it reads the hook's input and writes the records of the folder
// (activityName and the other names say what each holds) from hookInput, the type their readers read.
// Like hashicorp/go-plugin's plugins, it serves as the hook only with hookCookie set to 1, which the
// hook's command sets: run by hand, 'orchestra hook' says what it is and does nothing.

// hookCookie names the variable the hook's command sets to 1 for 'orchestra hook' to run as the hook.
const hookCookie = "ORCHESTRA_HOOK"

// hookCommand is the subcommand that runs a hook: hidden, as only Claude Code runs it.
const hookCommand = "hook"

// init serves the hook, before main and the packages that import this one start, when the binary is
// run as one: orchestra itself, or a test binary, which the hooks its tests install run (os.Executable).
func init() {
	if len(os.Args) > 1 && os.Args[1] == hookCommand && os.Getenv(hookCookie) == "1" {
		os.Exit(Hook(os.Args[2:], os.Getenv, os.Stdin, os.Stderr))
	}
}

// Hook is 'orchestra hook <event> <folder>', args being what follows hook: the worker's hook for the
// event, which writes its records to folder. It keeps the rules hookSettings lists: it exits 0
// whatever happens, reads all its input and never makes the folder. Without hookCookie set to 1 in
// getenv, it reads nothing and says on stderr what it is.
func Hook(args []string, getenv func(string) string, stdin io.Reader, stderr io.Writer) int {
	if getenv(hookCookie) != "1" {
		fmt.Fprintf(stderr, "orchestra %s is the hook of a Claude Code worker, which orchestra launches with it; "+
			"it does nothing run by hand (without %s=1)\n", hookCommand, hookCookie)
		return 0
	}
	in, err := io.ReadAll(stdin) // all of it: Claude Code would be left writing to a closed pipe
	if err != nil || len(args) != 2 {
		return 0
	}
	root, err := os.OpenRoot(args[1])
	if err != nil {
		return 0 // the folder is gone, as the worker removed it: never made again
	}
	defer func() { _ = root.Close() }() // what is written is closed: nothing to lose
	writeRecords(root, args[0], in)
	return 0
}

// hookInput is a hook's input, as the hooks and the records' readers take it: the fields of Claude
// Code's hook input orchestra reads, and V, the record's format (recordFormat). A note in the events log
// is one of these with the event and its noted fields (noteOf) only; the replaced records are the input
// itself with V added (withFormat), or one of these with the event alone.
type hookInput struct {
	V            int      `json:"v,omitzero"`
	Event        string   `json:"hook_event_name,omitzero"`
	Tool         string   `json:"tool_name,omitzero"`
	Session      string   `json:"session_id,omitzero"`
	Transcript   string   `json:"transcript_path,omitzero"`
	Agent        string   `json:"agent_id,omitzero"`
	AgentType    string   `json:"agent_type,omitzero"`
	Trigger      string   `json:"trigger,omitzero"`
	Source       string   `json:"source,omitzero"`
	Reason       string   `json:"reason,omitzero"`
	Notification string   `json:"notification_type,omitzero"`
	Input        toolArgs `json:"tool_input,omitzero"`
	// A permission prompt's note only: the removal in its command that Claude Code's safety check
	// holds for a person (dispatch.GuardedRemoval), read from the whole command, of which the note
	// keeps the start. Readers that don't know it read the prompt as an ordinary one.
	Removal string `json:"removal,omitzero"`
}

// toolArgs is the tool's arguments in a hook's input, those orchestra reads.
type toolArgs struct {
	Command      string `json:"command,omitzero"`
	FilePath     string `json:"file_path,omitzero"`
	NotebookPath string `json:"notebook_path,omitzero"`
}

// editTools are the tools whose file the hook adds to editsName before they run.
var editTools = []string{"Edit", "MultiEdit", "Write", "NotebookEdit"}

// writeRecords writes the records of the event's hook, whose input is in, to root, an os.Root at their
// folder; a record it can't write is left as it was.
func writeRecords(root *os.Root, event string, in []byte) {
	var h hookInput
	if json.Unmarshal(in, &h) != nil {
		h = hookInput{} // the input is unreadable: only the records that need none of it are written
	}
	if foreign(root, event, h) {
		return
	}
	input := withFormat(in)
	switch event {
	case "PreToolUse":
		replace(root, activityName, input)
		if path := h.Input.FilePath; slices.Contains(editTools, h.Tool) {
			if path == "" {
				path = h.Input.NotebookPath
			}
			if path != "" && !strings.Contains(path, "\n") { // a path a line
				appendTo(root, editsName, []byte(path+"\n"))
			}
		}
	case "PostToolUse", "PostToolUseFailure":
		// Claude Code runs PostToolUse after a tool that succeeded only; PostToolUseFailure is the other
		// case. The event alone: a finished tool's input carries its whole output.
		replace(root, activityName, eventRecord("PostToolUse"))
		name := askedName(h.Agent)
		if b, err := root.ReadFile(name); err == nil && bytes.Equal(b, askedRecord(h.Tool, in)) {
			_ = root.Remove(name) // one gone already is no prompt asked
		}
	case "PermissionRequest":
		replace(root, activityName, input)
		note(root, event, h)
		replace(root, askedName(h.Agent), askedRecord(h.Tool, in))
	case "SubagentStop":
		note(root, event, h)
		_ = root.Remove(askedName(h.Agent)) // none there is none asked
	case "PreCompact", "SubagentStart", "SessionEnd":
		note(root, event, h)
	case "Stop":
		replace(root, activityName, eventRecord(event))
		replace(root, turnName, eventRecord(event))
	case "UserPromptSubmit":
		replace(root, turnName, eventRecord(event))
	case "SessionStart":
		replace(root, sessionName, input)
		note(root, event, h)
	case "Notification":
		replace(root, noticeName, input)
	}
}

// foreign reports whether the event's hook, whose input is h, is one of a session other than the one
// the folder's session record (sessionName) names: an earlier worker's, left running in its tab beside
// the worker started since in the same worktree (dispatch renames an idle earlier worker and leaves
// it), whose hooks write to the same folder. Its records would tell of the worker that came after it:
// its SessionEnd, as it is closed or exited, that one gone. A session's start is its own, which it
// records, but a compaction's, which keeps the session's ID: one under another ID is another
// session's. An input or a record that names no session is the folder's session's.
func foreign(root *os.Root, event string, h hookInput) bool {
	if h.Session == "" || event == "SessionStart" && h.Source != "compact" {
		return false
	}
	b, err := root.ReadFile(sessionName)
	var rec hookInput
	if err != nil || json.Unmarshal(b, &rec) != nil || rec.Session == "" {
		return false // no session recorded yet, as before its start: none to tell another from
	}
	return rec.Session != h.Session
}

// withFormat returns in, a hook's input, as a record: the JSON object with V first; nil when in is no
// JSON object.
func withFormat(in []byte) []byte {
	obj := bytes.TrimLeft(in, " \t\r\n")
	if !json.Valid(obj) || len(obj) == 0 || obj[0] != '{' {
		return nil
	}
	rest := obj[1:]
	b := []byte(`{"v":` + strconv.Itoa(recordFormat))
	if t := bytes.TrimLeft(rest, " \t\r\n"); t[0] != '}' {
		b = append(b, ',')
	}
	return append(b, rest...)
}

// eventRecord is the record of the event alone.
func eventRecord(event string) []byte {
	b, _ := json.Marshal(hookInput{V: recordFormat, Event: event}) // strings only: it can't fail
	return b
}

// noteOf is the note of the event in the events log: its named fields from h, the hook's input.
func noteOf(event string, h hookInput) hookInput {
	n := hookInput{V: recordFormat, Event: event}
	switch event {
	case "PermissionRequest":
		n.Tool = h.Tool
		n.Input.Command = dispatch.Clip(h.Input.Command, 80) // a note is short: the start names the program
		n.Removal = dispatch.GuardedRemoval(h.Input.Command)
	case "PreCompact":
		n.Trigger = h.Trigger
	case "SubagentStart", "SubagentStop":
		n.Agent, n.AgentType = h.Agent, h.AgentType
	case "SessionStart":
		n.Source = h.Source
	case "SessionEnd":
		n.Reason = h.Reason
	}
	return n
}

// note appends the event's note to the events log, a line in one write, so lines of hooks that run at
// once (subagents side by side) don't mix.
func note(root *os.Root, event string, h hookInput) {
	b, _ := json.Marshal(noteOf(event, h)) // strings only: it can't fail
	appendTo(root, eventsName, append(b, '\n'))
}

// askedName is the record of the permission prompt the agent asked: askedPrefix and its ID kept to the
// characters of a file's name, "main" for the session's own.
func askedName(agent string) string {
	id := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return -1
	}, agent)
	if id == "" {
		id = "main"
	}
	return askedPrefix + id
}

// askedRecord is the record of the permission prompt asked for the call of tool whose hook's input is in:
// the tool's name, then a hash of its input on a line of its own. PermissionRequest carries no tool_use_id,
// so the input tells the call apart from another of the same tool the agent runs beside it (Claude Code
// runs Read, Grep, WebFetch and read-only Bash in parallel), whose end leaves the prompt asked. The input
// is hashed as decoded and encoded again, so key order and spacing don't count; one that reaches the
// tool changed (a dialog's updatedInput) leaves the record to the turn's end or the agent's stop.
func askedRecord(tool string, in []byte) []byte {
	var h struct {
		Input any `json:"tool_input"`
	}
	_ = json.Unmarshal(in, &h)    // unreadable: no input, as for every other unreadable one
	b, _ := json.Marshal(h.Input) // what JSON decoded: it can't fail
	sum := sha256.Sum256(b)
	return []byte(tool + "\n" + hex.EncodeToString(sum[:]) + "\n")
}

// replace replaces the record name in root with b, written to a temporary file moved over it, so a
// reader never sees half of one; nil b leaves it as it was.
func replace(root *os.Root, name string, b []byte) {
	if b == nil {
		return
	}
	tmp := name + "." + strconv.Itoa(os.Getpid())
	if root.WriteFile(tmp, b, 0o644) != nil || root.Rename(tmp, name) != nil {
		_ = root.Remove(tmp) // left unwritten: a hook ignores its errors
	}
}

// appendTo appends b to the record name in root, in one write.
func appendTo(root *os.Root, name string, b []byte) {
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(b) // left unwritten: a hook ignores its errors
	_ = f.Close()
}
