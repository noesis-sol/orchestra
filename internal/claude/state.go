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
// its hooks' records instead: activity, turn, notice, asked and events (activityName and the other
// record names say what each holds). Each record is replaced as it comes, so the ones written later,
// by their files' times, tell the latest. Whether the session's process is still there is the
// terminal's to say.

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

// UI is Claude Code as a hook-driven terminal knows its sessions (hookterm.AgentUI): by its hooks'
// records, read with its screen.
type UI struct{}

// Records is RecordsOf.
func (UI) Records(args []string) (string, bool) { return RecordsOf(args) }

// Read is ReadHooks: the session's state as its hooks tell it, and Settle.
func (UI) Read(records, dir string) (dispatch.AgentState, func(screen func() (string, error)) dispatch.AgentState) {
	h := ReadHooks(records, dir)
	return h.State, h.Settle
}

// State is what the Claude session whose hooks write to the folder records (see Records) is doing,
// as they tell it:
//
//   - gone once a SessionEnd came after the last SessionStart, unless it ended to clear the session,
//     which starts another at once;
//   - blocked while a permission prompt one of its agents asked is not answered: that agent's tool
//     not done since, nor a prompt, nor the notification that it waits at its prompt (as after a
//     refusal, which ends the turn), whatever its other agents report meanwhile; or after a
//     notification of a dialog waiting on an answer (one written in the same tick of the file's clock as
//     the prompt or the tool use that asks came after them);
//   - working while a subagent runs (dispatch.HookRecord.SubagentsRunning: one started and not
//     stopped, until no hook has reported anything for dispatch.SubagentQuiet), after a prompt
//     (UserPromptSubmit) until the turn's Stop, or after a tool use with no turn recorded;
//   - limited after a turn that ended on the account's usage limit (its StopFailure, see usageLimit,
//     or its Stop where the session's transcript tells Claude Code waits to continue by itself once the
//     limit resets, see limitNotices), until a prompt starts another, whatever the notifications say
//     meanwhile and however long ago the limit reset: the loop decides when to try again;
//   - idle after the turn's Stop, or a StopFailure for another API error, even where a tool use
//     reported later replaced it, as an agent Claude Code forks at the end of a turn reports one it
//     is refused (see turnEnded in dispatch) — but a tool that finished after it means the turn goes
//     on; after the notification that it waits at its prompt, which an interrupted turn ends with,
//     Stop or not; and once a session started and no prompt since;
//   - unknown before any hook ran, as while claude starts or shows a dialog that runs no hook (trust
//     this folder): the terminal's screen may say more.
func State(records string) dispatch.AgentState {
	return ReadHooks(records, "").State
}

// Hooks is what a Claude session's hooks tell of it (see State), and what a terminal reads its screen
// for where they can't tell (see Settle).
type Hooks struct {
	// State is what the session is doing, as its hooks tell it (see State).
	State dispatch.AgentState
	// Asked is whether it is blocked on a permission prompt the records tell asked and not answered.
	// Claude Code runs no hook as a permission is allowed (PreToolUse runs before it asks, PostToolUse
	// once the tool is done), so one allowed reads asked until its tool is done, however long that
	// takes: the terminal's screen says whether its dialog still shows (ShowsPermission).
	Asked bool
	// Limit is the usage limit the session's last turn ended on, when State is limited; zero otherwise.
	Limit dispatch.UsageLimit
	// Turned is whether the records tell a turn of the session: a prompt or a turn's end. Claude Code
	// asks its dialogs that run no hook (trust this folder, the project's MCP servers) before a
	// session's first turn, so a terminal looks for them on its screen only until then: a session's
	// output may well show their words. A new session's records have no turn (ReportArgs).
	Turned bool
}

// ReadHooks reads what the hooks writing to the folder records (see Records) tell of their session;
// records "" is the folder of a session launched without hooks in dir, a ticket's worktree, which has
// them in its .orchestra/run/. Unknown when they can't be read.
func ReadHooks(records, dir string) Hooks {
	if records == "" {
		records = Records(dir, "")
	}
	root, err := os.OpenRoot(records)
	if err != nil {
		return Hooks{State: dispatch.StateUnknown}
	}
	defer func() { _ = root.Close() }()    // read-only: nothing to lose
	b, fi, err := readIn(root, eventsName) // none yet: no session started
	var noted time.Time
	if err == nil {
		noted = fi.ModTime()
	}
	activity, turn := readRecord(root, activityName), readRecord(root, turnName)
	if turn.event == "Stop" {
		turn.limit = recordedLimit(root, turn.at) // a turn that ended normally at the limit
	}
	st, asked := state(activity, turn, readRecord(root, noticeName), readAsked(root),
		readSessions(b, noted, activity.at, time.Now()))
	h := Hooks{State: st, Asked: asked, Turned: !turn.at.IsZero()}
	if st == dispatch.StateLimited {
		h.Limit = turn.limit
	}
	return h
}

// Settle is the session's state, given screen, which reads its terminal's visible screen, once its
// process is known to be there: what its hooks tell, but blocked while its screen shows a dialog that
// runs no hook, which only comes before its first turn, and working once a permission prompt they
// tell asked no longer shows, as allowed, which runs no hook: its tool runs. A screen that can't be
// read, or reads empty, says nothing. It reads the screen only where the hooks can't tell.
func (h Hooks) Settle(screen func() (string, error)) dispatch.AgentState {
	if !h.firstTurnNotDone() && !h.Asked {
		return h.State // the hooks tell
	}
	s, err := screen()
	return settle(h, s, err == nil)
}

// firstTurnNotDone reports whether the hooks tell the session is yet to take its first turn, when it
// may show a dialog that runs no hook: no hook ran yet, as while claude starts or shows one, or it is
// idle with no turn recorded.
func (h Hooks) firstTurnNotDone() bool {
	return h.State == dispatch.StateUnknown || (h.State == dispatch.StateIdle && !h.Turned)
}

// settle is Settle's decision, given the screen as read, and whether it could be read.
func settle(h Hooks, screen string, read bool) dispatch.AgentState {
	switch {
	case !read:
	case h.firstTurnNotDone():
		if ShowsDialog(screen) {
			return dispatch.StateBlocked
		}
	case h.Asked:
		if screen != "" && !ShowsPermission(screen) {
			return dispatch.StateWorking // allowed, which runs no hook: its tool runs
		}
	}
	return h.State
}

// record is one of the hooks' replaced records: the event, a notification's type, the usage limit a
// StopFailure tells (or a Stop's transcript, see ReadHooks), and when it was written; a zero at when
// there is none.
type record struct {
	event, notification string
	limit               dispatch.UsageLimit
	at                  time.Time
}

// after says whether r was written after o, or is there and o isn't.
func (r record) after(o record) bool {
	return !r.at.IsZero() && r.at.After(o.at)
}

// follows says whether r, the notice of a dialog waiting on an answer, came after o: written after it,
// or in the same tick of a coarse clock (a filesystem's 1-second times, say) where o is a record such a
// dialog can only follow, the prompt or the tool use that asks.
func (r record) follows(o record) bool {
	switch o.event {
	case "UserPromptSubmit", "PreToolUse", dispatch.EventPermission:
		return !r.at.IsZero() && !r.at.Before(o.at)
	}
	return r.after(o)
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
// (see Hooks.Asked); asked is the latest prompt an agent asked and is not done with.
func state(activity, turn, notice, asked record, s sessions) (dispatch.AgentState, bool) {
	if s.ended {
		return dispatch.StateGone, false
	}
	if notice.follows(activity) && notice.follows(turn) {
		switch notice.notification {
		case permissionPrompt:
			return dispatch.StateBlocked, true
		case elicitation:
			return dispatch.StateBlocked, false
		}
	}
	if notice.after(activity) && notice.after(turn) {
		switch notice.notification {
		case idlePrompt:
			if s.subagents {
				return dispatch.StateWorking, false
			}
			if turn.limit.Reached() {
				return dispatch.StateLimited, false // waiting at its prompt is what the limit leaves it to
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
	case "Stop", "StopFailure":
		if activity.event == "PostToolUse" && activity.after(turn) {
			return dispatch.StateWorking
		}
		if turn.limit.Reached() {
			return dispatch.StateLimited
		}
		return dispatch.StateIdle
	case "UserPromptSubmit":
		return dispatch.StateWorking
	}
	switch activity.event {
	case "PreToolUse", "PostToolUse":
		return dispatch.StateWorking
	case "Stop", "StopFailure":
		return dispatch.StateIdle
	}
	if s.started {
		return dispatch.StateIdle
	}
	return dispatch.StateUnknown
}

// readRecord reads the record name in root, an os.Root at the records' folder; a zero record when
// there is none, or it can't be read, as one in a newer format.
func readRecord(root *os.Root, name string) record {
	b, fi, err := readIn(root, name)
	if err != nil {
		return record{}
	}
	var in hookInput
	if json.Unmarshal(b, &in) != nil || in.Event == "" || !readable(in.V) {
		return record{}
	}
	r := record{event: in.Event, notification: in.Notification, at: fi.ModTime()}
	if in.Event == "StopFailure" {
		r.limit, _ = usageLimit(in.Error, in.Message, r.at)
	}
	return r
}

// readAsked returns the latest of the permission prompts' records in root, an os.Root at the records'
// folder (see askedPrefix): a permission prompt asked, dated, and a zero record when there is none.
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
// subagents running at now, skipping a line it can't read, as one in a newer format; the notes were
// last written at noted, and the last tool use reported at used. A session ended to be cleared is
// followed by another, which may not have started yet.
func readSessions(b []byte, noted, used, now time.Time) sessions {
	var s sessions
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var e hookInput
		if json.Unmarshal(sc.Bytes(), &e) != nil || !readable(e.V) {
			continue
		}
		switch e.Event {
		case "SessionStart":
			s.started, s.ended = true, false
		case "SessionEnd":
			s.ended = e.Reason != "clear"
		}
	}
	r := parseEvents(b, noted)
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
