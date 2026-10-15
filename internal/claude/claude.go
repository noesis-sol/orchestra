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
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// PermissionRequest (the hook's input: the tool and its arguments) and by PostToolUse,
	// PostToolUseFailure and Stop (the event alone: a finished tool's input carries its whole output).
	// A permission prompt is recorded like a tool use: the worker waits on it until it is answered,
	// and the tool runs once it is allowed. Read by LastToolUse and ReadHooks.
	activityName = "activity.json"
	// turnName is the last turn's end (Stop) or prompt's start (UserPromptSubmit), replaced by those
	// hooks only, so a tool use reported after a Stop, from no turn of the worker's, can't hide that
	// the turn ended: Claude Code forks agents at the end of a turn (a prompt suggestion, say) that run
	// the worker's PreToolUse hook and are then refused the tool. Read by LastToolUse and ReadHooks.
	turnName = "turn.json"
	// editsName is the path of each file the worker is about to edit, one per line, appended by
	// PreToolUse for Edit, MultiEdit, Write and NotebookEdit: activity keeps only the last tool use,
	// and the scheduler needs every file the worker touches. Read by EditedFiles.
	editsName = "edits"
	// sessionName is the session's input (session_id, transcript_path), replaced by SessionStart as
	// the session starts or is resumed, cleared or compacted: what to resume, and where the transcript
	// is. Read by Session and TranscriptTail.
	sessionName = "session.json"
	// eventsName is a log of one JSON line each, appended by PermissionRequest (its tool), PreCompact
	// (its trigger), SubagentStart and SubagentStop (its ID and type), SessionStart (its source:
	// startup, resume, clear, compact) and SessionEnd (its reason: exit, clear, logout, …), which the
	// tool uses of subagents, replacing activity, can't hide. A line is short and appended in one
	// write, so lines of hooks that run at once (subagents side by side) don't mix; a value with a
	// quote or a backslash is noted empty. Read by HookRecord and ReadHooks.
	eventsName = "events"
	// noticeName is the last notification Claude Code showed (a permission prompt, a dialog asking for
	// input, the session idle at its prompt for a while), replaced by Notification: what the session
	// waits on where no other hook says, as an interrupted turn has no Stop. Read by ReadHooks.
	noticeName = "notice.json"
	// askedPrefix, followed by an agent's ID ("main" for the session's own), is the tool that agent
	// asked to use, replaced by its PermissionRequest and removed by its tool's end (PostToolUse or
	// PostToolUseFailure of the same agent and tool) or its stop (SubagentStop). Other agents' tool
	// uses, which replace activity, leave it, so a prompt one subagent waits on still shows while
	// another works; a prompt refused ends the turn, which turn tells. Read by ReadHooks.
	askedPrefix = "asked-"
)

// recordFormat is the format of the records the hooks write, which each JSON one carries as "v": bump
// it whenever a record's fields or what they mean change. A worker keeps the hooks of the orchestra
// that launched it, so another orchestra (one upgraded since, or adopting the worker) may read its
// records. One with no "v" was written before records carried it, in what is format 1; one in a newer
// format than recordFormat is unreadable (readable), and the Reporter says so in the log. An older
// format, once there is one, is read by its own rules or not at all: nothing on disk is rewritten.
// edits and the asked records are plain text, a path a line and a tool name: a change to them takes a
// new file name.
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
type Reporter struct{ newer *newerLog }

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
func (Reporter) ReportArgs(worktree string) ([]string, error) {
	return reportArgs(worktree, "", allRecords...)
}

// ReportArgsIn is ReportArgs for a Claude session with no worktree of its own, such as the
// environment probe's worker (project.ProbeRun) or the feature interview (project.InterviewRun): its
// hooks write to the folder of that name in dir's .orchestra/run/, apart from a ticket's records.
func (Reporter) ReportArgsIn(dir, folder string) ([]string, error) {
	return reportArgs(dir, folder, allRecords...)
}

// allRecords are the files the hooks write, all of them an earlier session's for a new one.
var allRecords = []string{activityName, turnName, editsName, sessionName, eventsName, noticeName}

// ResumeArgs is ReportArgs for a worker resuming the session of the one before it in worktree: the
// files that one edited are kept, as the resumed worker carries on its work, and so is its session,
// which the resumed worker's SessionStart hook writes again (claude --resume keeps the session ID):
// were the start to fail, as when Herdr is down, the next run still finds the session to resume.
// Its other records are removed: the subagents its process ran ended with it.
func (Reporter) ResumeArgs(worktree string) ([]string, error) {
	return reportArgs(worktree, "", activityName, turnName, eventsName, noticeName)
}

// reportArgs is ReportArgsIn, removing the earlier session's records named stale; folder "" is
// dir's .orchestra/run/ itself. A file or a symlink in place of the folder, wherever it points, is
// replaced by it, as WriteRun replaces a symlink in place of a file: the records are written through
// no link.
func reportArgs(dir, folder string, stale ...string) ([]string, error) {
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
	at := func(name string) string { return filepath.Join(dir, rel(name)) }
	settings := hookSettings(records{activity: at(activityName), turn: at(turnName), edits: at(editsName),
		session: at(sessionName), events: at(eventsName), notice: at(noticeName), asked: at(askedPrefix)})
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := project.WriteRun(root, dir, rel(settingsName), b, 0o644); err != nil {
		return nil, err
	}
	return []string{"--settings", at(settingsName)}, nil
}

// records are the paths of the files the hooks write; asked is the start of the paths of the
// permission prompts asked, one per agent.
type records struct{ activity, turn, edits, session, events, notice, asked string }

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

// hookSettings is the settings that have the worker's hooks write the records r (activityName and
// the other names say what each holds), each JSON one in recordFormat, and that carry workerEnv.
// Every hook keeps to these rules:
//   - A record replaced is written to a temporary file and moved over it, so a reader never sees
//     half of one.
//   - It exits 0, ignoring its errors: a reporting hook must never change what the worker does, and
//     Claude Code takes a hook's exit status 2 as "block" (the tool call is refused, or the turn may
//     not end). dash, /bin/sh on Debian and Ubuntu, exits 2 when a redirection fails, as every write
//     here does once the worker has removed .orchestra/run/.
//   - It reads all its input: a hook that stops early would leave Claude Code writing to a closed
//     pipe.
//   - It doesn't make the folder again: a git stash pop of the worker's own git stash --all would
//     then fail on the file.
func hookSettings(r records) map[string]any {
	activity, turn, edits, session, events := r.activity, r.turn, r.edits, r.session, r.events
	writeTo := func(path, from string) string {
		return "f=" + command.ShellQuote(path) + `; ` + from + ` > "$f.$$" && mv -f "$f.$$" "$f"`
	}
	write := func(from string) string { return writeTo(activity, from) }
	// v starts each JSON record with its format; input is the hook's input, a JSON object, so
	// started: the object's opening brace is replaced by the record's start.
	v := `{"v":` + strconv.Itoa(recordFormat)
	input := `sed '1s/^[[:space:]]*{/` + v + `,/'`
	event := func(name string) string {
		return write(`printf '` + v + `,"hook_event_name":"` + name + `"}'`)
	}
	turnAt := func(name string) string {
		return writeTo(turn, `printf '`+v+`,"hook_event_name":"`+name+`"}'`)
	}
	hook := func(line string) []any {
		line = "{ " + line + "; cat >/dev/null; } 2>/dev/null; exit 0"
		return []any{map[string]any{"type": "command", "command": line}}
	}
	// The path is the first file_path (notebook_path for NotebookEdit) in the tool's input; a
	// quote inside the input's strings is escaped, so one in a Write's content can't match.
	// note appends the event to events, with the named string fields of its input: names, IDs and
	// words, each found as the first such key, which a quote escaped inside a string can't fake.
	field := func(f string) string {
		return f + `=$(printf '%s' "$in" | grep -oE '"` + f + `" *: *"[^"\\]*"' | head -n 1 | ` +
			`sed -E 's/^[^:]*: *"//; s/"$//')`
	}
	note := func(name string, fields ...string) string {
		line, format, args := `in=$(cat)`, v+`,"hook_event_name":"`+name+`"`, ""
		for _, f := range fields {
			line += `; ` + field(f)
			format += `,"` + f + `":"%s"`
			args += ` "$` + f + `"`
		}
		return line + `; printf '` + format + `}\n'` + args + ` >> ` + command.ShellQuote(events)
	}
	// askedBy sets f to the asking agent's record, its ID kept to the characters of a file's name, and
	// then runs then.
	askedBy := func(then string) string {
		return `in=$(cat); ` + field("agent_id") + `; ` + field("tool_name") +
			`; a=$(printf '%s' "$agent_id" | tr -cd 'A-Za-z0-9_-'); f=` + command.ShellQuote(r.asked) +
			`"${a:-main}"; ` + then
	}
	asked := askedBy(`printf '%s' "$tool_name" > "$f.$$" && mv -f "$f.$$" "$f"`)
	answered := askedBy(`[ "$(cat "$f")" = "$tool_name" ] && rm -f "$f"`)
	edited := `grep -oE '"(file_path|notebook_path)" *: *"[^"]*"' | head -n 1 | sed -E 's/^[^:]*: *"//; s/"$//' >> ` +
		command.ShellQuote(edits)
	return map[string]any{"hooks": map[string]any{
		"PreToolUse": []any{
			map[string]any{"matcher": "*", "hooks": hook(write(input))},
			map[string]any{"matcher": "Edit|MultiEdit|Write|NotebookEdit", "hooks": hook(edited)},
		},
		"PostToolUse": []any{
			map[string]any{"matcher": "*", "hooks": hook(event("PostToolUse"))},
			map[string]any{"matcher": "*", "hooks": hook(answered)},
		},
		// Claude Code runs PostToolUse after a tool that succeeded only; this is the other case.
		"PostToolUseFailure": []any{
			map[string]any{"matcher": "*", "hooks": hook(event("PostToolUse"))},
			map[string]any{"matcher": "*", "hooks": hook(answered)},
		},
		"PermissionRequest": []any{
			map[string]any{"matcher": "*", "hooks": hook(write(input))},
			map[string]any{"matcher": "*", "hooks": hook(note("PermissionRequest", "tool_name"))},
			map[string]any{"matcher": "*", "hooks": hook(asked)},
		},
		"PreCompact":    []any{map[string]any{"hooks": hook(note("PreCompact", "trigger"))}},
		"SubagentStart": []any{map[string]any{"hooks": hook(note("SubagentStart", "agent_id", "agent_type"))}},
		"SubagentStop": []any{
			map[string]any{"hooks": hook(note("SubagentStop", "agent_id", "agent_type"))},
			map[string]any{"hooks": hook(askedBy(`rm -f "$f"`))},
		},
		"Stop": []any{
			map[string]any{"hooks": hook(event("Stop"))},
			map[string]any{"hooks": hook(turnAt("Stop"))},
		},
		"UserPromptSubmit": []any{map[string]any{"hooks": hook(turnAt("UserPromptSubmit"))}},
		"SessionStart": []any{
			map[string]any{"hooks": hook(writeTo(session, input))},
			map[string]any{"hooks": hook(note("SessionStart", "source"))},
		},
		"SessionEnd":   []any{map[string]any{"hooks": hook(note("SessionEnd", "reason"))}},
		"Notification": []any{map[string]any{"hooks": hook(writeTo(r.notice, input))}},
	}, "env": workerEnv}
}

// workerEnv is the environment the worker's settings give it. Fork mode, on by default in an
// interactive session, runs every subagent in the background, so a worker told to wait for one
// (the verifier) can't: its turn ends while the subagent works. With fork mode off, Claude runs a
// subagent in the foreground when it needs the answer before going on.
var workerEnv = map[string]any{"CLAUDE_CODE_FORK_SUBAGENT": "0"}

// LastToolUse returns what the worker in worktree reported last, and when, and false when it has
// reported nothing readable; with it, when its last turn ended, if no prompt has started another.
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
		if t, ok := parseToolUse(b); ok && t.Event == "Stop" {
			u.Stopped = fi.ModTime()
		} else if !ok {
			r.sayNewer(worktree, turnName, b)
		}
	}
	return u, true
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
		var e struct {
			V       int    `json:"v"`
			Event   string `json:"hook_event_name"`
			Tool    string `json:"tool_name"`
			Trigger string `json:"trigger"`
			Agent   string `json:"agent_id"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || !readable(e.V) {
			continue
		}
		stoppedLast = e.Event == "SubagentStop" && running[e.Agent]
		switch e.Event {
		case dispatch.EventPermission:
			if e.Tool == "" {
				e.Tool = "an unnamed tool"
			}
			r.Permissions = append(r.Permissions, e.Tool)
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
	var in struct {
		V          int    `json:"v"`
		Event      string `json:"hook_event_name"`
		Tool       string `json:"tool_name"`
		Session    string `json:"session_id"`
		Transcript string `json:"transcript_path"`
		Agent      string `json:"agent_id"`
		AgentType  string `json:"agent_type"`
		Input      struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(b, &in) != nil || in.Event == "" || !readable(in.V) {
		return dispatch.ToolUse{}, false
	}
	return dispatch.ToolUse{Event: in.Event, Tool: in.Tool, Command: in.Input.Command, Session: in.Session,
		Transcript: in.Transcript, Agent: in.Agent, AgentType: in.AgentType}, true
}
