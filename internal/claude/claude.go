// Package claude is Claude Code as orchestra's worker: hooks that have the worker record each tool
// it uses, and reading that record back. The hooks are loaded with --settings for the worker only,
// so the project's and the user's own settings are left as they are.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/text/unicode/norm"
)

// The files the hooks write, in the worktree's .orchestra/run/ or the folder ReportArgsIn names: what
// each one holds, which hooks write it, and who reads it.
const (
	// settingsName is the hooks' settings themselves, written by reportArgs and loaded with --settings.
	settingsName = "hooks.json"
	// activityName is the last report of any agent of the session, replaced by PreToolUse and
	// PermissionRequest (the hook's input: the tool and its arguments), by PostToolUse,
	// PostToolUseFailure and Stop (the event alone: a finished tool's input carries its whole output)
	// and by the main agent's StopFailure (failureRecord).
	// A permission prompt is recorded like a tool use: the worker waits on it until it is answered,
	// and the tool runs once it is allowed. Read by LastToolUse and ReadHooks.
	activityName = "activity.json"
	// turnName is the last turn's end (Stop, or StopFailure when an API error such as the usage limit
	// ended it) or prompt's start (UserPromptSubmit), replaced by those hooks only, so a tool use
	// reported after a Stop, from no turn of the worker's, can't hide that the turn ended: Claude Code
	// forks agents at the end of a turn (a prompt suggestion, say) that run the worker's PreToolUse hook
	// and are then refused the tool. Read by LastToolUse and ReadHooks.
	turnName = "turn.json"
	// editsName is the path of each file the worker is about to edit, one per line, appended by
	// PreToolUse for Edit, MultiEdit, Write and NotebookEdit: activity keeps only the last tool use,
	// and the scheduler needs every file the worker touches. Read by EditedFiles.
	editsName = "edits"
	// sessionName is the session's input (session_id, transcript_path), replaced by SessionStart as
	// the session starts or is resumed, cleared or compacted: what to resume, and where the transcript
	// is. Read by Session and TranscriptTail.
	sessionName = "session.json"
	// processName is the process ID of the Claude Code whose session the folder records, a line of
	// plain text, written by the SessionStart of a process the folder has none recorded for (after a
	// start) and removed by each start: the hooks of another process are an earlier worker's, left
	// running in the same worktree, whose events, its /clear and /resume included, are left out
	// (foreign). Read by the hooks only.
	processName = "process"
	// eventsName is a log of one JSON line each, appended by PermissionRequest (its tool), PreCompact
	// (its trigger), SubagentStart and SubagentStop (its ID and type), SessionStart (its source:
	// startup, resume, clear, compact) and SessionEnd (its reason: exit, clear, logout, …), which the
	// tool uses of subagents, replacing activity, can't hide. A line is short and appended in one
	// write, so lines of hooks that run at once (subagents side by side) don't mix. Read by HookRecord
	// and ReadHooks.
	eventsName = "events"
	// noticeName is the last notification Claude Code showed (a permission prompt, a dialog asking for
	// input, the session idle at its prompt for a while), replaced by Notification: what the session
	// waits on where no other hook says, as an interrupted turn has no Stop. Read by ReadHooks, and
	// by LastToolUse for the session idle after its last report.
	noticeName = "notice.json"
	// askedPrefix, followed by an agent's ID ("main" for the session's own), is the tool call that agent
	// asked to make (askedRecord), replaced by its PermissionRequest and removed by its tool's end
	// (PostToolUse or PostToolUseFailure of the same agent, tool and input) or its stop (SubagentStop).
	// Its other calls of the tool, run beside it, and other agents' tool uses, which replace activity,
	// leave it, so a prompt one call waits on still shows while another works; a prompt refused ends the
	// turn, which turn tells. Read by ReadHooks, which reads only when it was written.
	askedPrefix = "asked-"
)

// recordFormat is the format of the records the hooks write, which each JSON one carries as "v": bump
// it whenever a record's fields or what they mean change. A worker keeps the hooks of the orchestra
// that launched it, so another orchestra (one upgraded since, or adopting the worker) may read its
// records. One with no "v" was written before records carried it, in what is format 1; one in a newer
// format than recordFormat is unreadable (readable), and the Reporter says so in the log. An older
// format, once there is one, is read by its own rules or not at all: nothing on disk is rewritten.
// edits is plain text, a path a line: a change to it takes a new file name. What the asked records hold
// only the hooks that wrote them read, the worker's own.
//
// A field or an event added to format 1 that its earlier readers can ignore keeps the format: a
// permission prompt's removal, or StopFailure's record, which those readers take for a turn that is
// over (State: idle) without the usage limit it may tell, as hooks that ran none left it before.
const recordFormat = 1

// readable reports whether a record in format v, 0 for none given, is one these readers know.
func readable(v int) bool {
	return v == 0 || v == recordFormat
}

// newerFormat returns the newest format of the records in b, one record or a log of them a line each,
// that is newer than recordFormat, and 0 when none is; it stops at what it can't read.
func newerFormat(b []byte) int {
	newest := 0
	d := json.NewDecoder(bytes.NewReader(b))
	for {
		var r struct {
			V int `json:"v"`
		}
		if d.Decode(&r) != nil {
			return newest
		}
		if r.V > recordFormat && r.V > newest {
			newest = r.V
		}
	}
}

// Reporter sets workers up to report what they are doing and reads what they reported. The zero
// Reporter logs nothing; NewReporter's says in the log, once a worker, that its records are in a newer
// format than it reads.
type Reporter struct {
	newer *newerLog
	env   map[string]string // what the worker's settings give its environment beside workerEnv (see WithEnv)
}

// WithEnv is r with the workers' settings giving their environment env too, as their share of the
// machine's cores (see dispatch.CoresVar): Claude Code runs their commands with it.
func (r Reporter) WithEnv(env map[string]string) dispatch.Reporter {
	r.env = env
	return r
}

// NewReporter returns a Reporter that logs with log.
func NewReporter(log func(text string)) Reporter {
	return Reporter{newer: &newerLog{log: log, said: map[string]bool{}}}
}

// newerLog is where a Reporter says that a worker's records are in a newer format, and the worktrees
// it said it of.
type newerLog struct {
	log  func(text string)
	mu   sync.Mutex
	said map[string]bool
}

// sayNewer logs, once for worktree, that its record name, read as b, is in a newer format than
// recordFormat, if it is: the worker was launched by a newer orchestra.
func (r Reporter) sayNewer(worktree, name string, b []byte) {
	if r.newer == nil {
		return
	}
	r.newer.mu.Lock()
	said := r.newer.said[worktree]
	r.newer.mu.Unlock()
	if said {
		return
	}
	v := newerFormat(b)
	if v == 0 {
		return
	}
	r.newer.mu.Lock()
	defer r.newer.mu.Unlock()
	if r.newer.said[worktree] {
		return
	}
	r.newer.said[worktree] = true
	r.newer.log(fmt.Sprintf("%s is in hook record format %d, newer than this orchestra reads (%d): "+
		"the worker was launched by a newer orchestra, and its records are left unread",
		filepath.Join(worktree, project.RunPath(name)), v, recordFormat))
}

// ReportArgs writes the reporting hooks to .orchestra/run/hooks.json in worktree and returns the
// arguments that load them. An earlier worker's records are removed, so nothing stale is read.
func (r Reporter) ReportArgs(worktree string) ([]string, error) {
	return reportArgs(worktree, "", r.env, allRecords...)
}

// ReportArgsIn is ReportArgs for a Claude session with no worktree of its own, such as the
// environment probe's worker (project.ProbeRun) or the feature interview (project.InterviewRun): its
// hooks write to the folder of that name in dir's .orchestra/run/, apart from a ticket's records.
func (r Reporter) ReportArgsIn(dir, folder string) ([]string, error) {
	return reportArgs(dir, folder, r.env, allRecords...)
}

// allRecords are the files the hooks write, all of them an earlier session's for a new one.
var allRecords = []string{activityName, turnName, editsName, sessionName, processName, eventsName, noticeName}

// ResumeArgs is ReportArgs for a worker resuming the session of the one before it in worktree: the
// files that one edited are kept, as the resumed worker carries on its work, and so is its session,
// which the resumed worker's SessionStart hook writes again (claude --resume keeps the session ID):
// were the start to fail, as when Herdr is down, the next run still finds the session to resume.
// Its other records are removed: the subagents its process ran ended with it, and the resumed session
// runs in a process of its own.
func (r Reporter) ResumeArgs(worktree string) ([]string, error) {
	return reportArgs(worktree, "", r.env, activityName, turnName, processName, eventsName, noticeName)
}

// reportArgs is ReportArgsIn, its settings giving the worker env too, removing the earlier session's
// records named stale; folder "" is
// dir's .orchestra/run/ itself. A file or a symlink in place of the folder, wherever it points, is
// replaced by it, as WriteRun replaces a symlink in place of a file: the records are written through
// no link.
func reportArgs(dir, folder string, env map[string]string, stale ...string) ([]string, error) {
	root, err := project.OpenRun(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := func(name string) string { return project.RunPath(filepath.Join(folder, name)) }
	if folder != "" {
		if fi, err := root.Lstat(rel("")); err == nil && !fi.IsDir() {
			if err := root.Remove(rel("")); err != nil { // a link itself, not what it points to
				return nil, project.RunError(dir, rel(""), err)
			}
		}
		if err := root.MkdirAll(rel(""), 0o755); err != nil {
			return nil, project.RunError(dir, rel(""), err)
		}
	}
	asked, err := askedRecords(root, rel(""))
	if err != nil {
		return nil, project.RunError(dir, rel(""), err)
	}
	for _, name := range append(stale, asked...) { // the agents that asked ended with the earlier session
		if err := project.RemoveRun(root, dir, rel(name)); err != nil {
			return nil, err
		}
	}
	bin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("finding orchestra's binary for the worker's hooks: %w", err)
	}
	at := func(name string) string { return filepath.Join(dir, rel(name)) }
	b, err := json.MarshalIndent(hookSettings(bin, at(""), env), "", "  ")
	if err != nil {
		return nil, err
	}
	if err := project.WriteRun(root, dir, rel(settingsName), b, 0o644); err != nil {
		return nil, err
	}
	return []string{"--settings", at(settingsName)}, nil
}

// askedRecords returns the names of the permission prompts' records (askedPrefix) in folder, relative
// to root, as the hooks left them; none when the folder isn't there.
func askedRecords(root *os.Root, folder string) ([]string, error) {
	f, err := root.Open(folder)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(names, func(n string) bool { return !strings.HasPrefix(n, askedPrefix) }), nil
}

// hookSettings is the settings that have the worker's hooks write their records to the folder records
// (activityName and the other names say what each holds), each JSON one in recordFormat, and that
// carry workerEnv and env. Each event's hook is bin, orchestra's binary that launches the worker, run as 'bin
// hook <event> <records> <process>' (Hook), process being the shell's $PPID, the Claude Code that runs
// the hook, and keeps to these rules:
//   - A record replaced is written to a temporary file and moved over it, so a reader never sees
//     half of one.
//   - It exits 0, ignoring its errors: a reporting hook must never change what the worker does, and
//     Claude Code takes a hook's exit status 2 as "block" (the tool call is refused, or the turn may
//     not end). The command around bin exits 0 whatever bin does, as when it is gone.
//   - It reads all its input: a hook that stops early would leave Claude Code writing to a closed
//     pipe. The command reads what bin left, as when it is gone.
//   - It doesn't make the folder again: a git stash pop of the worker's own git stash --all would
//     then fail on the file.
//   - It records nothing of a process or a session other than the ones the folder's records name
//     (foreign), as an earlier worker's left running in the same worktree.
func hookSettings(bin, records string, env map[string]string) map[string]any {
	hook := func(event string) []any {
		line := "{ " + hookCookie + "=1 " + command.ShellQuote(bin) + " " + hookCommand + " " + event + " " +
			command.ShellQuote(records) + ` "$PPID"; cat >/dev/null; } 2>/dev/null; exit 0`
		return []any{map[string]any{"type": "command", "command": line}}
	}
	tool := func(event string) []any { return []any{map[string]any{"matcher": "*", "hooks": hook(event)}} }
	other := func(event string) []any { return []any{map[string]any{"hooks": hook(event)}} }
	return map[string]any{"hooks": map[string]any{
		"PreToolUse":         tool("PreToolUse"),
		"PostToolUse":        tool("PostToolUse"),
		"PostToolUseFailure": tool("PostToolUseFailure"),
		"PermissionRequest":  tool("PermissionRequest"),
		"PreCompact":         other("PreCompact"),
		"SubagentStart":      other("SubagentStart"),
		"SubagentStop":       other("SubagentStop"),
		"Stop":               other("Stop"),
		"StopFailure":        other("StopFailure"),
		"UserPromptSubmit":   other("UserPromptSubmit"),
		"SessionStart":       other("SessionStart"),
		"SessionEnd":         other("SessionEnd"),
		"Notification":       other("Notification"),
	}, "env": settingsEnv(env)}
}

// settingsEnv is workerEnv with env beside it.
func settingsEnv(env map[string]string) map[string]any {
	all := maps.Clone(workerEnv)
	for k, v := range env {
		all[k] = v
	}
	return all
}

// workerEnv is the environment the worker's settings give it. Fork mode, on by default in an
// interactive session, runs every subagent in the background, so a worker told to wait for one
// (the verifier) can't: its turn ends while the subagent works. With fork mode off, Claude runs a
// subagent in the foreground when it needs the answer before going on.
var workerEnv = map[string]any{"CLAUDE_CODE_FORK_SUBAGENT": "0"}

// LastToolUse returns what the worker in worktree reported last, and when, and false when it has
// reported nothing readable; with it, when its last turn ended, if no prompt has started another, and
// when Claude Code noted it idle at its prompt since (as after a permission prompt refused), and the
// API error its last turn ended on, if it did: the usage limit, or another, which ends the turn as a
// Stop does. A turn that ended with a Stop at the usage limit, as its transcript tells (see
// limitNotices), ended on the limit too; one whose end the transcript has yet to log, which tells
// whether it did, is ending (see stopTold).
func (r Reporter) LastToolUse(worktree string) (dispatch.ToolUse, bool) {
	b, fi, err := readRun(worktree, activityName)
	if err != nil {
		return dispatch.ToolUse{}, false
	}
	u, ok := parseToolUse(b)
	if !ok {
		r.sayNewer(worktree, activityName, b)
		return u, false
	}
	u.At = fi.ModTime() // each report replaces the file, so it was written then
	if b, fi, err := readRun(worktree, turnName); err == nil {
		var t hookInput
		switch {
		case json.Unmarshal(b, &t) != nil || !readable(t.V):
			r.sayNewer(worktree, turnName, b)
		case t.Event == "Stop":
			if u.Limit, u.Ending = r.stopTold(worktree, fi.ModTime()); !u.Limit.Reached() {
				u.Stopped = fi.ModTime()
			}
		case t.Event == "StopFailure":
			var limited bool
			if u.Limit, limited = usageLimit(t.Error, t.Message, fi.ModTime()); !limited {
				u.Stopped = fi.ModTime() // another API error ends the turn as a Stop does
			}
			u.Failure = t.Error
		}
	}
	if b, fi, err := readRun(worktree, noticeName); err == nil && fi.ModTime().After(u.At) {
		var in hookInput
		if json.Unmarshal(b, &in) == nil && readable(in.V) && in.Notification == idlePrompt {
			u.Idle = fi.ModTime()
		}
	}
	return u, true
}

// stopTold is what the session's transcript tells now of the turn of the worker in worktree, which ended
// at at with a Stop (see the function stopTold): the usage limit it waits on, and whether the turn still counts
// as going on; nothing when the worker has no session.
func (r Reporter) stopTold(worktree string, at time.Time) (dispatch.UsageLimit, bool) {
	s, ok := r.Session(worktree)
	if !ok {
		return dispatch.UsageLimit{}, false
	}
	return stopTold(s.Transcript, at, time.Now())
}

// HookRecord returns what the hooks of the worker in worktree noted besides its tool uses: the
// permission prompts it waited on, its compactions and the subagents it is running. The time of the
// last note is the events file's last write: its lines carry none.
func (r Reporter) HookRecord(worktree string) dispatch.HookRecord {
	b, fi, err := readRun(worktree, eventsName)
	if err != nil {
		return dispatch.HookRecord{}
	}
	r.sayNewer(worktree, eventsName, b)
	return parseEvents(b, fi.ModTime())
}

// parseEvents reads the hooks' notes, one JSON line each, the last of them noted at noted, skipping a
// line it can't read, as one in a newer format. A subagent is running from its start to its stop,
// matched by its ID; a stop with no start is of none of the worker's (Claude Code's forks at the end
// of a turn note one).
func parseEvents(b []byte, noted time.Time) dispatch.HookRecord {
	r := dispatch.HookRecord{Noted: noted}
	running := map[string]bool{}
	stoppedLast := false // the last note read is a running subagent's stop
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var e hookInput
		if json.Unmarshal(sc.Bytes(), &e) != nil || !readable(e.V) {
			continue
		}
		stoppedLast = e.Event == "SubagentStop" && running[e.Agent]
		switch e.Event {
		case dispatch.EventPermission:
			if e.Tool == "" {
				e.Tool = "an unnamed tool"
			}
			r.Permissions = append(r.Permissions, dispatch.Permission{Tool: e.Tool, Command: e.Input.Command,
				Removal: e.Removal})
		case "PreCompact":
			if e.Trigger == "" {
				e.Trigger = "unknown trigger"
			}
			r.Compactions = append(r.Compactions, e.Trigger)
		case "SubagentStart":
			running[e.Agent] = true
		case "SubagentStop":
			delete(running, e.Agent)
		}
	}
	r.Subagents = len(running)
	if stoppedLast {
		r.SubagentStopped = noted
	}
	return r
}

// EditedFiles returns the files the worker in worktree has edited, as paths in the repository, in
// the order it first edited them. Files outside the worktree and in .orchestra/run/ are left out.
func (Reporter) EditedFiles(worktree string) []string {
	b, _, err := readRun(worktree, editsName)
	if err != nil {
		return nil
	}
	return editedFiles(b, worktree)
}

// readRun reads the file name in worktree's .orchestra/run/ through an os.Root, which won't follow
// a symlink out of the worktree, and returns it with its details as it was read.
func readRun(worktree, name string) ([]byte, os.FileInfo, error) {
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }() // read-only: nothing to lose
	return readIn(root, project.RunPath(name))
}

func editedFiles(b []byte, worktree string) []string {
	roots := []string{worktree}
	if resolved, err := filepath.EvalSymlinks(worktree); err == nil && resolved != worktree {
		roots = append(roots, resolved) // the worker may name it by its real path
	}
	seen := map[string]bool{}
	var files []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p == "" {
			continue
		}
		rel := ""
		for _, root := range roots {
			path := p
			if !filepath.IsAbs(path) {
				path = filepath.Join(worktree, path)
			}
			if r, ok := relative(root, path); ok {
				rel = filepath.ToSlash(r)
				break
			}
		}
		if rel == "" || rel == "." || strings.HasPrefix(rel, project.Dir+"/"+project.RunName+"/") || seen[rel] {
			continue
		}
		seen[rel] = true
		files = append(files, rel)
	}
	return files
}

// relative returns path relative to root, when path is in it. The two are compared in one Unicode
// form, NFC: a worker may name the worktree in the other form than orchestra does, as with é written
// as e and U+0301 (NFD, as Finder gives names). The part inside root is kept in the form path gives
// it; the dispatch maps it to the file as git lists it.
func relative(root, path string) (string, bool) {
	r, err := filepath.Rel(norm.NFC.String(root), norm.NFC.String(path))
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	if r == "." {
		return r, true
	}
	// NFC leaves the separators where they are, so r is as many of path's last parts as it has.
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.Clean(path), sep)
	return strings.Join(parts[len(parts)-strings.Count(r, sep)-1:], sep), true
}

// parseToolUse reads one hook input: the event, and before a tool use (or at a permission prompt)
// the tool and its command, and the session and the subagent (if any) using it; false when it can't
// be read, as one in a newer format.
func parseToolUse(b []byte) (dispatch.ToolUse, bool) {
	var in hookInput
	if json.Unmarshal(b, &in) != nil || in.Event == "" || !readable(in.V) {
		return dispatch.ToolUse{}, false
	}
	return dispatch.ToolUse{Event: in.Event, Tool: in.Tool, Command: in.Input.Command, Failure: in.Error,
		Session: in.Session, Transcript: in.Transcript, Agent: in.Agent, AgentType: in.AgentType}, true
}
