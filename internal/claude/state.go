package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A terminal that can't read a Claude session's screen, as Herdr does, tells what it is doing from
// its hooks' records instead (see hookSettings): the last report (activity), the start or end of its
// last turn (turn), the last notification (notice), the permission prompts its agents asked and are
// not done with (asked) and the log of its sessions and subagents (events). Each record is replaced
// as it comes, so the ones written later, by their files' times, tell the latest. Whether the
// session's process is still there is the terminal's to say.

// Records is the folder the hooks loaded with ReportArgsIn(dir, folder) write their records to;
// folder "" is the one of ReportArgs(dir), dir's .orchestra/run/.
func Records(dir, folder string) string {
	return filepath.Join(dir, project.RunPath(folder))
}

// RecordsOf returns the folder the hooks loaded by args (from ReportArgs, ResumeArgs or ReportArgsIn,
// among a session's other arguments) write their records to, and false when args load none.
func RecordsOf(args []string) (string, bool) {
	for i, a := range args {
		path, ok := strings.CutPrefix(a, "--settings=")
		switch {
		case a == "--":
			return "", false // what follows is the prompt, none of claude's options
		case a == "--settings" && i+1 < len(args):
			path, ok = args[i+1], true
		}
		if ok && filepath.IsAbs(path) && filepath.Base(path) == settingsName {
			return filepath.Dir(path), true
		}
	}
	return "", false
}

// State is what the Claude session whose hooks write to the folder records (see Records) is doing,
// as they tell it:
//
//   - gone once a SessionEnd came after the last SessionStart, unless it ended to clear the session,
//     which starts another at once;
//   - blocked while a permission prompt one of its agents asked is not answered: that agent's tool
//     not done since, nor a prompt, nor the notification that it waits at its prompt (as after a
//     refusal, which ends the turn), whatever its other agents report meanwhile; or after a
//     notification of a dialog waiting on an answer;
//   - working while a subagent runs (dispatch.HookRecord.SubagentsRunning: one started and not
//     stopped, until no hook has reported anything for dispatch.SubagentQuiet), after a prompt
//     (UserPromptSubmit) until the turn's Stop, or after a tool use with no turn recorded;
//   - idle after the turn's Stop, even where a tool use reported later replaced it, as an agent Claude
//     Code forks at the end of a turn reports one it is refused (see turnEnded in dispatch) — but a
//     tool that finished after it means the turn goes on; after the notification that it waits at
//     its prompt, which an interrupted turn ends with, Stop or not; and once a session started and
//     no prompt since;
//   - unknown before any hook ran, as while claude starts or shows a dialog that runs no hook (trust
//     this folder): the terminal's screen may say more.
func State(records string) dispatch.AgentState {
	st, _ := StateAsked(records)
	return st
}

// StateAsked is State, and whether it is blocked on a permission prompt the records tell asked and
// not answered. Claude Code runs no hook as a permission is allowed (PreToolUse runs before it asks,
// PostToolUse once the tool is done), so one allowed reads asked until its tool is done, however long
// that takes: the terminal's screen says whether its dialog still shows (ShowsPermission).
func StateAsked(records string) (dispatch.AgentState, bool) {
	root, err := os.OpenRoot(records)
	if err != nil {
		return dispatch.StateUnknown, false
	}
	defer func() { _ = root.Close() }()    // read-only: nothing to lose
	b, fi, err := readIn(root, eventsName) // none yet: no session started
	var noted time.Time
	if err == nil {
		noted = fi.ModTime()
	}
	activity := readRecord(root, activityName)
	return state(activity, readRecord(root, turnName), readRecord(root, noticeName), readAsked(root),
		readSessions(b, noted, activity.at, time.Now()))
}

// TurnRecorded reports whether the records (see Records) tell a turn of the session: a prompt or a
// turn's end. Claude Code asks its dialogs that run no hook (trust this folder, the project's MCP
// servers) before a session's first turn, so a terminal looks for them on its screen only until then:
// a session's output may well show their words. A new session's records have no turn (ReportArgs).
func TurnRecorded(records string) bool {
	root, err := os.OpenRoot(records)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }() // read-only: nothing to lose
	return !readRecord(root, turnName).at.IsZero()
}

// ShowsPermission reports whether the bottom of screen, a Claude session's visible screen, shows its
// permission dialog: the question "Do you want to proceed?" (or to make an edit, create a file, …)
// or the first of its answers, "1. Yes".
func ShowsPermission(screen string) bool {
	lines := strings.Split(strings.ToLower(screen), "\n")
	for _, l := range lines[max(0, len(lines)-25):] {
		l = strings.TrimLeft(l, " \t│|❯>") // its frame, and the cursor on the answer chosen
		if strings.HasPrefix(l, "do you want to ") || strings.HasPrefix(l, "1. yes") {
			return true
		}
	}
	return false
}

// record is one of the hooks' replaced records: the event, a notification's type, and when it was
// written; a zero at when there is none.
type record struct {
	event, notification string
	at                  time.Time
}

// after says whether r was written after o, or is there and o isn't.
func (r record) after(o record) bool {
	return !r.at.IsZero() && r.at.After(o.at)
}

// The notifications State reads: Claude Code waits at its prompt, or on an answer to a dialog.
const (
	idlePrompt       = "idle_prompt"
	permissionPrompt = "permission_prompt"
	elicitation      = "elicitation_dialog"
)

// sessions is what the events log says of the sessions and subagents: whether one started, whether
// the last one ended for good, and whether a subagent runs.
type sessions struct {
	started, ended, subagents bool
}

// state is State's reading of the records, and whether it is blocked on a permission prompt asked
// (see StateAsked); asked is the latest prompt an agent asked and is not done with.
func state(activity, turn, notice, asked record, s sessions) (dispatch.AgentState, bool) {
	if s.ended {
		return dispatch.StateGone, false
	}
	if notice.after(activity) && notice.after(turn) {
		switch notice.notification {
		case permissionPrompt:
			return dispatch.StateBlocked, true
		case elicitation:
			return dispatch.StateBlocked, false
		case idlePrompt:
			if s.subagents {
				return dispatch.StateWorking, false
			}
			return dispatch.StateIdle, false
		}
	}
	if asked.after(turn) && !(notice.notification == idlePrompt && notice.after(asked)) {
		return dispatch.StateBlocked, true
	}
	return unasked(activity, turn, s), false
}

// unasked is state's reading of records that tell no prompt or dialog waiting on an answer.
func unasked(activity, turn record, s sessions) dispatch.AgentState {
	if s.subagents {
		return dispatch.StateWorking
	}
	switch turn.event {
	case "Stop":
		if activity.event == "PostToolUse" && activity.after(turn) {
			return dispatch.StateWorking
		}
		return dispatch.StateIdle
	case "UserPromptSubmit":
		return dispatch.StateWorking
	}
	switch activity.event {
	case "PreToolUse", "PostToolUse":
		return dispatch.StateWorking
	case "Stop":
		return dispatch.StateIdle
	}
	if s.started {
		return dispatch.StateIdle
	}
	return dispatch.StateUnknown
}

// readRecord reads the record name in root, an os.Root at the records' folder; a zero record when
// there is none, or it can't be read.
func readRecord(root *os.Root, name string) record {
	b, fi, err := readIn(root, name)
	if err != nil {
		return record{}
	}
	var in struct {
		Event        string `json:"hook_event_name"`
		Notification string `json:"notification_type"`
	}
	if json.Unmarshal(b, &in) != nil || in.Event == "" {
		return record{}
	}
	return record{event: in.Event, notification: in.Notification, at: fi.ModTime()}
}

// readAsked returns the latest of the permission prompts' records in root, an os.Root at the records'
// folder (see hookSettings): a permission prompt asked, dated, and a zero record when there is none.
// One asked earlier tells no more: a turn or a notification that answers the latest came after it too.
func readAsked(root *os.Root) record {
	names, _ := askedRecords(root, ".") // none to read: none asked
	var latest record
	for _, name := range names {
		if fi, err := root.Lstat(name); err == nil && fi.Mode().IsRegular() && fi.ModTime().After(latest.at) {
			latest = record{event: dispatch.EventPermission, at: fi.ModTime()}
		}
	}
	return latest
}

// readSessions reads the hooks' notes, one JSON line each, for the sessions' starts and ends and the
// subagents running at now, skipping a line it can't read; the notes were last written at noted, and
// the last tool use reported at used. A session ended to be cleared is followed by another, which
// may not have started yet.
func readSessions(b []byte, noted, used, now time.Time) sessions {
	var s sessions
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var e struct {
			Event  string `json:"hook_event_name"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Event {
		case "SessionStart":
			s.started, s.ended = true, false
		case "SessionEnd":
			s.ended = e.Reason != "clear"
		}
	}
	r := parseEvents(b)
	r.Noted = noted
	s.subagents = r.SubagentsRunning(used, now)
	return s
}

// readIn reads the file name in root, which won't follow a symlink out of it, and returns it with its
// details as it was read.
func readIn(root *os.Root, name string) ([]byte, os.FileInfo, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	b, err := io.ReadAll(f)
	return b, fi, err
}
