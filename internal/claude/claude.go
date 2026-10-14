// Package claude is Claude Code as orchestra's worker: hooks that have the worker record each tool
// it uses, and reading that record back. The hooks are loaded with --settings for the worker only,
// so the project's and the user's own settings are left as they are.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/text/unicode/norm"
)

const (
	settingsName = "hooks.json"    // in the worktree's .orchestra/run/
	activityName = "activity.json" // written by the hooks, read by orchestra
	turnName     = "turn.json"     // the last turn's end (Stop) or start (UserPromptSubmit), written by the hooks
	editsName    = "edits"         // the paths the worker edits, one per line, appended by the hooks
	sessionName  = "session.json"  // the worker's Claude Code session, written by its SessionStart hook
	eventsName   = "events"        // permission prompts, compactions, subagents and sessions, one per line, appended
	noticeName   = "notice.json"   // the last notification (a dialog shown, waiting on input), written by the hooks
	askedPrefix  = "asked-"        // + an agent's ID ("main" for the session's own): the tool it asked to use
)

// Reporter sets workers up to report what they are doing and reads what they reported.
type Reporter struct{}

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

// hookSettings records the hook's input before each tool use (the tool and its arguments), and
// only the event after a tool use (failed or not) and at the end of a turn: a finished tool's input
// carries its whole output. Each write goes through a temporary file, so a reader never sees half
// of one. Before each edit, the file's path is also appended to edits: the record of the last tool
// use is replaced by the next one, and the scheduler needs every file the worker touches. As the
// session starts, or is resumed, cleared or compacted, its input (session_id, transcript_path) is
// written to session, which says what to resume and where the transcript is.
//
// The end of each turn (Stop) and the start of each prompt's (UserPromptSubmit) also replace turn,
// which no tool use does: a record of a tool use that comes after a Stop, from no turn of the
// worker's, can't hide that the turn ended. Claude Code forks agents at the end of a turn (a
// prompt suggestion, say) that run the worker's PreToolUse hook and are then refused the tool.
//
// A permission prompt's input (the tool and its arguments) is recorded like a tool use's: the
// worker waits on it until it is answered, and the tool runs once it is allowed. It is also noted in
// events, with each compaction (its trigger), each subagent's start and stop (its ID and type) and
// each session's start (its source: startup, resume, clear, compact) and end (its reason: exit,
// clear, logout, …), one JSON line each: those don't replace the record of the last tool use, which
// a subagent's own tool uses keep replacing. A line is short and appended in one write, so lines of hooks that run
// at once (subagents side by side) don't mix. A value with a quote or a backslash is noted empty.
//
// Each agent's permission prompt also writes the tool it asks to use to a record of that agent's
// own (asked, then its ID, "main" for the session's): another agent's tool uses, which replace
// activity, leave it, so a prompt one subagent waits on still shows while another works. The tool's
// end (PostToolUse or PostToolUseFailure of the same agent and tool) removes it, as does the
// subagent's stop; a prompt refused, which ends the turn, is told by the turn's records.
//
// A reporting hook must never change what the worker does, and Claude Code takes a hook's exit
// status 2 as "block": the tool call is refused, or the turn may not end. dash, /bin/sh on Debian
// and Ubuntu, exits 2 when a redirection fails, as every write here does once the worker has
// removed .orchestra/run/. So each hook ignores its errors, reads all its input (a hook that
// stops early would leave Claude Code writing to a closed pipe) and exits 0. It doesn't make the
// folder again: a git stash pop of the worker's own git stash --all would then fail on the file.
// Each notification Claude Code shows (a permission prompt, a dialog asking for input, the session
// idle at its prompt for a while) replaces notice, which says what the session waits on where no
// other hook does: an interrupted turn has no Stop.
//
// The settings also carry workerEnv.
func hookSettings(r records) map[string]any {
	activity, turn, edits, session, events := r.activity, r.turn, r.edits, r.session, r.events
	writeTo := func(path, from string) string {
		return "f=" + command.ShellQuote(path) + `; ` + from + ` > "$f.$$" && mv -f "$f.$$" "$f"`
	}
	write := func(from string) string { return writeTo(activity, from) }
	event := func(name string) string {
		return write(`printf '{"hook_event_name":"` + name + `"}'`)
	}
	turnAt := func(name string) string {
		return writeTo(turn, `printf '{"hook_event_name":"`+name+`"}'`)
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
		line, format, args := `in=$(cat)`, `{"hook_event_name":"`+name+`"`, ""
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
			map[string]any{"matcher": "*", "hooks": hook(write("cat"))},
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
			map[string]any{"matcher": "*", "hooks": hook(write("cat"))},
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
			map[string]any{"hooks": hook(writeTo(session, "cat"))},
			map[string]any{"hooks": hook(note("SessionStart", "source"))},
		},
		"SessionEnd":   []any{map[string]any{"hooks": hook(note("SessionEnd", "reason"))}},
		"Notification": []any{map[string]any{"hooks": hook(writeTo(r.notice, "cat"))}},
	}, "env": workerEnv}
}

// workerEnv is the environment the worker's settings give it. Fork mode, on by default in an
// interactive session, runs every subagent in the background, so a worker told to wait for one
// (the verifier) can't: its turn ends while the subagent works. With fork mode off, Claude runs a
// subagent in the foreground when it needs the answer before going on.
var workerEnv = map[string]any{"CLAUDE_CODE_FORK_SUBAGENT": "0"}

// LastToolUse returns what the worker in worktree reported last, and when, and false when it has
// reported nothing readable; with it, when its last turn ended, if no prompt has started another.
func (Reporter) LastToolUse(worktree string) (dispatch.ToolUse, bool) {
	b, fi, err := readRun(worktree, activityName)
	if err != nil {
		return dispatch.ToolUse{}, false
	}
	u, ok := parseToolUse(b)
	if !ok {
		return u, false
	}
	u.At = fi.ModTime() // each report replaces the file, so it was written then
	if b, fi, err := readRun(worktree, turnName); err == nil {
		if t, ok := parseToolUse(b); ok && t.Event == "Stop" {
			u.Stopped = fi.ModTime()
		}
	}
	return u, true
}

// HookRecord returns what the hooks of the worker in worktree noted besides its tool uses: the
// permission prompts it waited on, its compactions and the subagents it is running.
func (Reporter) HookRecord(worktree string) dispatch.HookRecord {
	b, _, err := readRun(worktree, eventsName)
	if err != nil {
		return dispatch.HookRecord{}
	}
	return parseEvents(b)
}

// parseEvents reads the hooks' notes, one JSON line each, skipping a line it can't read. A subagent
// is running from its start to its stop, matched by its ID.
func parseEvents(b []byte) dispatch.HookRecord {
	var r dispatch.HookRecord
	running := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		var e struct {
			Event   string `json:"hook_event_name"`
			Tool    string `json:"tool_name"`
			Trigger string `json:"trigger"`
			Agent   string `json:"agent_id"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
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
// the tool and its command, and the session and the subagent (if any) using it.
func parseToolUse(b []byte) (dispatch.ToolUse, bool) {
	var in struct {
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
	if json.Unmarshal(b, &in) != nil || in.Event == "" {
		return dispatch.ToolUse{}, false
	}
	return dispatch.ToolUse{Event: in.Event, Tool: in.Tool, Command: in.Input.Command, Session: in.Session,
		Transcript: in.Transcript, Agent: in.Agent, AgentType: in.AgentType}, true
}
