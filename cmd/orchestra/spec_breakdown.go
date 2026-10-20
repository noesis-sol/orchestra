package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The headless breakdown (orchestra --spec without --refine) takes the spec as complete and final,
// with nobody there to ask: claude -p in the main checkout, with the user's own Claude Code setup
// (the project's CLAUDE.md and settings, as a feature's session has) but no MCP servers besides a
// worker's (dispatch.Config.SessionMCP), breaks it down. Its instructions (internal/project's
// spec-prompt.md) are appended to its system prompt; its first message names the committed spec. It
// reads with Read, Glob, Grep and Agent, files with bd through Bash, which runs nothing else but
// read-only git, and answers with the root epic and the assumptions it made (--json-schema), streaming
// what it does (stream-json) for the progress shown. Breaking the spec down for the first time, it
// files the root epic and the environment ticket alone, and its answer gives the rest of the tree,
// which orchestra files (spec_tree.go); an update files through bd all it changes.

// specSchema is an update's answer: the root epic's ID, the assumptions it made, and, with no root
// epic, why it filed none. A breakdown for the first time answers specTreeSchema.
const specSchema = `{"type":"object","properties":{"root":{"type":"string"},` +
	`"assumptions":{"type":"array","items":{"type":"string"}},"problem":{"type":"string"}},` +
	`"required":["root","assumptions"]}`

// specAnswer is the breakdown's answer, as specSchema or specTreeSchema has it.
type specAnswer struct {
	Root        string      `json:"root"`
	Assumptions []string    `json:"assumptions"`
	Problem     string      `json:"problem"`
	Issues      []specIssue `json:"issues"` // for orchestra to file under the root epic; none in an update
}

// specTools are the breakdown's tools: it reads, has sub-agents read, and runs bd and git.
const specTools = "Read,Glob,Grep,Agent,Bash"

// specAllowed are the breakdown's allow rules: Bash runs bd and read-only git without asking.
var specAllowed = []string{"Bash(bd *)", "Bash(git log *)", "Bash(git show *)", "Bash(git diff *)",
	"Bash(git status *)", "Bash(git ls-files *)", "Bash(git ls-tree *)", "Bash(git grep *)", "Bash(git blame *)",
	"Bash(git rev-parse *)", "Bash(git cat-file *)"}

// specDenied are the tools the breakdown never has, a sub-agent's included: it changes nothing but
// Beads.
var specDenied = []string{"Edit", "Write", "NotebookEdit"}

// specCountEvery is how often plain output says how many epics and tickets the breakdown has filed.
const specCountEvery = time.Minute

// headlessBreakdown breaks a spec down in claude -p, as a specBreakdown.
type headlessBreakdown struct {
	repo   string        // the main checkout, where claude runs
	limit  time.Duration // how long the session may take
	effort string
	// servers are the MCP servers it gets, and no others: none but the project's chosen ones
	servers []mcp.Server
	chrome  []string      // --chrome or --no-chrome, as workers get Chrome (dispatch.Config.SessionChrome)
	say     tui.Printer   // where its progress shows: a terminal's busy line, or plain lines
	log     *dispatch.Log // nil in tests
	// bd, which has what a session that gave no answer filed, and files the tree its answer gives
	tracker    specBd
	countEvery time.Duration // specCountEvery; tests give their own
}

// newHeadlessBreakdown is the headless breakdown of the spec run s, its progress on s's terminal
// unless the run is plain.
func newHeadlessBreakdown(s *session) headlessBreakdown {
	c := s.cfg
	return headlessBreakdown{repo: c.Repo, limit: c.SpecTimeout, effort: c.SpecEffort, servers: c.SessionMCP(),
		chrome: c.SessionChrome(), say: s.printer(), log: s.log, tracker: beads.Tracker{Repo: c.Repo},
		countEvery: specCountEvery}
}

// BreakDown runs the session on the spec r names, files the tree its answer gives (fileAnswerTree), and
// returns the root epic its answer names and the assumptions it made. A session that fails, runs out
// of time or is stopped gives no answer: the root epic is then the one it filed, as bd has it, if
// any. An answer orchestra can't file is an error, with the root epic it names.
func (h headlessBreakdown) BreakDown(ctx context.Context, r breakdownRequest) (breakdownResult, error) {
	if why := organ.Unavailable(ctx, "claude"); why != "" {
		return breakdownResult{}, errors.New(why)
	}
	prompt, err := project.WriteSpecPrompt(h.repo, r.Update != nil)
	if err != nil {
		return breakdownResult{}, fmt.Errorf("cannot write the breakdown's instructions: %w", err)
	}
	servers, err := sessionMCPArgs(h.repo, h.servers, h.chrome)
	if err != nil {
		return breakdownResult{}, fmt.Errorf("the breakdown %w", err)
	}
	effort := cmp.Or(h.effort, project.DefaultSpecEffort)
	schema := specTreeSchema
	if r.Update != nil {
		schema = specSchema
	}
	args := specArgs(prompt, servers, effort, claudeHelp(ctx), schema)
	h.logLine(fmt.Sprintf("SPEC: breaking %s down with claude (effort %s, time limit %s)", r.Path, effort,
		command.ShortDuration(h.limit)))
	progress := startSpecProgress(h.say, h.log, h.countEvery)
	stream := &specStream{pending: map[string][]filedIssue{}, filed: func(issues []filedIssue, ids []string) {
		progress.filed(issues, ids)
		if r.Filed != nil && ids != nil {
			r.Filed(ids...)
		}
	}}
	began := time.Now()
	err = command.Stream(ctx, h.limit, h.repo, nil, specMessage(r, h.limit), stream, "claude", args...)
	stream.end()
	h.logSession(stream, time.Since(began))
	answer, err := stream.answer(ctx, err)
	if err != nil {
		progress.end(ctx.Err() != nil)
		return breakdownResult{Root: filedRoot(ctx, h.tracker, r.Spec)}, err
	}
	res := breakdownResult{Root: answer.Root, Assumptions: answer.Assumptions, Problem: answer.Problem}
	if r.Update == nil && answer.Root != "" {
		if res.Warnings, err = h.fileAnswer(ctx, r.Spec, answer, stream.filed); err != nil {
			progress.end(ctx.Err() != nil)
			return res, err
		}
	}
	progress.done()
	return res, nil
}

// fileAnswer files the tree the answer a gives for spec (fileAnswerTree), telling filed of each issue
// filed, and logs what it filed. It returns the warnings about the root epic.
func (h headlessBreakdown) fileAnswer(ctx context.Context, spec project.Spec, a specAnswer,
	filed func(issues []filedIssue, ids []string)) ([]string, error) {
	if len(a.Issues) > 0 {
		h.logLine(fmt.Sprintf("SPEC: filing the %s of the breakdown's answer under %s", plural(len(a.Issues), "issue"),
			a.Root))
	}
	began := time.Now()
	done, err := fileAnswerTree(ctx, h.tracker, spec, a, func(f filedIssue, id string) {
		filed([]filedIssue{f}, []string{id})
	})
	if err != nil {
		return nil, err
	}
	if done.issues > 0 {
		h.logLine(fmt.Sprintf("SPEC: filed %s and %s from the breakdown's answer (%s)", plural(done.issues, "issue"),
			plural(done.links, "later blocks link"), command.ShortDuration(time.Since(began).Round(time.Second))))
	}
	return done.warnings, nil
}

// specArgs is the session's command line, past claude, for the instructions in prompt: its tools, its
// MCP servers and Chrome (servers, from sessionMCPArgs), the tools' rules, nothing that would ask for
// a permission (help, claude's --help, says whether it has --permission-prompts), its answer's schema
// and the stream of what it does. Its first message comes on stdin: an argument after the rules' list
// would be read as one of them. dontAsk denies whatever the rules don't allow, whatever mode the
// user's settings start Claude Code in; --permission-prompts none denies what would still prompt.
func specArgs(prompt string, servers []string, effort, help, schema string) []string {
	args := slices.Concat([]string{"-p", "--append-system-prompt-file", prompt, "--tools", specTools}, servers,
		[]string{"--allowedTools"})
	args = append(args, specAllowed...)
	args = append(args, "--disallowedTools")
	args = append(args, specDenied...)
	args = append(args, "--permission-mode", "dontAsk")
	if organ.ListsFlag(help, "--permission-prompts") {
		args = append(args, "--permission-prompts", "none")
	}
	return append(args, "--output-format", "stream-json", "--verbose", "--json-schema", schema,
		"--effort", effort)
}

// claudeHelp is claude's --help, or "" when it can't be read.
func claudeHelp(ctx context.Context) string {
	help, err := command.Output(ctx, command.ReadLimit, os.TempDir(), "claude", "--help")
	if err != nil {
		return ""
	}
	return help
}

// specMessage is the session's first message: the spec it breaks down, and how long it has; in update
// mode, also the root epic whose tree it updates, the version of the spec that tree was filed for, and
// where to read the change and the tree.
func specMessage(r breakdownRequest, limit time.Duration) string {
	s, u := r.Spec, r.Update
	var b strings.Builder
	if u == nil {
		fmt.Fprintf(&b, "Break down the spec at %s into Beads tickets.\n\n", s.Path)
	} else {
		fmt.Fprintf(&b, "The spec at %s has changed since its Beads tickets were filed under %s: update them for "+
			"the change.\n\n", s.Path, u.Root)
	}
	fmt.Fprintf(&b, "- path: %s\n- sha256: %s\n- commit: %s\n", s.Path, s.SHA256, s.Commit)
	if u != nil {
		fmt.Fprintf(&b, "- root epic: %s\n- filed for: sha256 %s, commit %s\n- the change and the tree: %s\n", u.Root,
			u.Was.SHA256, cmp.Or(u.Was.Commit, "not recorded"), u.File)
	}
	fmt.Fprintf(&b, "- time limit: %s, after which orchestra stops this session\n", command.ShortDuration(limit))
	return b.String()
}

// specLister is bd as a breakdown reads what it filed: the issues carrying the spec's metadata.
type specLister interface {
	WithMetadata(ctx context.Context, key, value string) ([]dispatch.Ticket, error)
}

// specBd is bd as the headless breakdown has it: it reads what it filed, and files its answer's tree.
type specBd interface {
	specLister
	treeFiler
}

// filedRoot is the root epic of the spec that bd has, as rootAmong finds it, or "": after a session
// that gave no answer. bd is read after a stop or the time limit all the same, with its own limit.
func filedRoot(ctx context.Context, bd specLister, s project.Spec) string {
	issues, err := bd.WithMetadata(context.WithoutCancel(ctx), dispatch.SpecKey, s.Path)
	if err != nil {
		return ""
	}
	return rootAmong(issues, s)
}

// rootAmong is the spec's root epic among issues, those carrying the spec's metadata: the one epic
// among them whose parent isn't among them (see topEpics), or of several such the one whose SHA-256
// is the spec's; "" when that names none, or more than one.
func rootAmong(issues []dispatch.Ticket, s project.Spec) string {
	tops := topEpics(issues)
	var same []string
	for _, t := range tops {
		if t.MetadataText(project.SpecSHA256Key) == s.SHA256 {
			same = append(same, t.ID)
		}
	}
	switch {
	case len(tops) == 1:
		return tops[0].ID
	case len(same) == 1:
		return same[0]
	}
	return ""
}

// logSession logs what the session cost, from its result, or that it gave none.
func (h headlessBreakdown) logSession(s *specStream, took time.Duration) {
	if s.result == nil {
		h.logLine(fmt.Sprintf("SPEC: claude session %s gave no result (%s)", s.session,
			command.ShortDuration(took.Round(time.Second))))
		return
	}
	r := *s.result
	h.logLine("SPEC: breakdown " + organ.Spend{CostUSD: r.CostUSD, Turns: r.Turns, Session: r.SessionID,
		Subtype: r.Subtype, Took: took}.Cost())
}

// logLine writes text to the run's log, if there is one.
func (h headlessBreakdown) logLine(text string) {
	if h.log != nil {
		h.log.Line(time.Now(), text)
	}
}

// specStream reads the session's stream-json output, a JSON message a line, as an io.Writer: what
// each bd create it ran filed, and the result at the end.
type specStream struct {
	partial []byte                  // the start of a line not yet ended
	pending map[string][]filedIssue // the bd creates of each Bash call not answered yet, by its tool_use ID
	filed   func(issues []filedIssue, ids []string)
	session string        // the session's ID, from its first message that gives one
	result  *organ.Result // its result message; nil until it comes
}

// Write takes the next of the output, a line at a time.
func (s *specStream) Write(b []byte) (int, error) {
	s.partial = append(s.partial, b...)
	for {
		i := bytes.IndexByte(s.partial, '\n')
		if i < 0 {
			return len(b), nil
		}
		s.line(s.partial[:i])
		s.partial = s.partial[i+1:]
	}
}

// end takes a last line the output didn't end.
func (s *specStream) end() {
	if len(bytes.TrimSpace(s.partial)) > 0 {
		s.line(s.partial)
	}
	s.partial = nil
}

// streamContent is an item of a message's content: a tool's use, or its result.
type streamContent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"` // a string, or a list of text blocks
	IsError   bool            `json:"is_error"`
}

// line reads one message of the stream; anything else is passed over.
func (s *specStream) line(b []byte) {
	var m struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &m) != nil {
		return
	}
	if s.session == "" {
		s.session = m.SessionID
	}
	if m.Type == "result" {
		var r organ.Result
		if json.Unmarshal(b, &r) == nil {
			s.result = &r
		}
		return
	}
	var content []streamContent
	if json.Unmarshal(m.Message.Content, &content) != nil { // a prompt's text, say
		return
	}
	for _, c := range content {
		switch {
		case m.Type == "assistant" && c.Type == "tool_use" && c.Name == "Bash":
			var in struct {
				Command string `json:"command"`
			}
			if json.Unmarshal(c.Input, &in) == nil {
				if creates := bdCreates(in.Command); len(creates) > 0 {
					s.pending[c.ID] = creates
				}
			}
		case m.Type == "user" && c.Type == "tool_result":
			creates, ok := s.pending[c.ToolUseID]
			delete(s.pending, c.ToolUseID)
			if ok && !c.IsError && s.filed != nil {
				s.filed(creates, createdIDs(resultText(c.Content), len(creates)))
			}
		}
	}
}

// resultText is a tool result's text: the string, or its text blocks joined.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		texts = append(texts, b.Text)
	}
	return strings.Join(texts, "\n")
}

// createdIDs are the IDs n bd creates printed with --silent, one a line, in their order; nil when the
// output isn't that.
func createdIDs(out string, n int) []string {
	ids := strings.Fields(out)
	if len(ids) != n || len(strings.Split(strings.TrimSpace(out), "\n")) != n || slices.ContainsFunc(ids, notAnID) {
		return nil
	}
	return ids
}

// notAnID tells whether a word printed by bd is anything but an issue's ID: letters, digits, dots,
// hyphens and underscores.
func notAnID(w string) bool {
	return strings.ContainsFunc(w, func(r rune) bool {
		return r != '.' && r != '-' && r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	})
}

// answer is the session's answer, once its output has been read and it has ended with err: its
// structured output, with the root epic and the assumptions trimmed. The error says why there is no
// answer: ctx ended, claude ran out of time, failed, or reported an error, or its answer can't be read.
func (s *specStream) answer(ctx context.Context, err error) (specAnswer, error) {
	e, isCmd := errors.AsType[*command.Error](err)
	if isCmd {
		e.Args = nil // the instructions' path, the rules and the schema would bury why it failed
	}
	switch {
	case ctx.Err() != nil:
		return specAnswer{}, ctx.Err()
	case isCmd && e.Stopped:
		return specAnswer{}, fmt.Errorf("claude %w", e.Err) // "timed out after 1h"
	case s.result != nil && failed(*s.result):
		r := *s.result
		detail := strings.Join(r.Errors, "; ")
		if detail == "" {
			detail = r.Result
		}
		return specAnswer{}, &organ.CallError{Bin: "claude", Subtype: r.Subtype, StopReason: r.StopReason,
			Detail: strings.TrimSpace(detail)}
	case err != nil:
		return specAnswer{}, err
	case s.result == nil:
		return specAnswer{}, errors.New("claude ended without an answer")
	}
	raw := s.result.Structured
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(s.result.Result) // where an older claude puts it
	}
	var a specAnswer
	if err := json.Unmarshal(raw, &a); err != nil {
		return specAnswer{}, fmt.Errorf("claude's answer can't be read: %w", err)
	}
	a.Root, a.Problem = strings.TrimSpace(a.Root), strings.TrimSpace(a.Problem)
	var assumed []string
	for _, x := range a.Assumptions {
		if x = strings.TrimSpace(x); x != "" {
			assumed = append(assumed, x)
		}
	}
	a.Assumptions = assumed
	return a, nil
}

// failed tells whether a result reports an error, or a refusal, which claude may report as a success.
func failed(r organ.Result) bool {
	return r.IsError || strings.HasPrefix(string(r.Subtype), "error") || r.StopReason == organ.StopRefusal
}

// filedIssue is an issue a bd create command files, as its command line gives it.
type filedIssue struct {
	Epic  bool
	Title string
}

// bdCreates are the issues the bd create commands in a shell command line file, in their order, with
// the text of its here-documents left out: they give a description, not commands.
func bdCreates(line string) []filedIssue {
	var issues []filedIssue
	for _, words := range shellCommands(withoutHeredocs(line)) {
		for len(words) > 0 && isAssignment(words[0]) {
			words = words[1:]
		}
		if len(words) < 2 || words[0] != "bd" {
			continue
		}
		args := words[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "-") { // bd's own flags
			args = args[1:]
		}
		if len(args) == 0 || args[0] != "create" && args[0] != "new" {
			continue
		}
		issues = append(issues, createdBy(args[1:]))
	}
	return issues
}

// createdBy is the issue bd create files with args: its type and title.
func createdBy(args []string) filedIssue {
	var f filedIssue
	for i := 0; i < len(args); i++ {
		a := args[i]
		value := func() string {
			if _, v, ok := strings.Cut(a, "="); ok {
				return v
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch {
		case a == "--type" || a == "-t" || strings.HasPrefix(a, "--type=") || strings.HasPrefix(a, "-t="):
			f.Epic = value() == "epic"
		case a == "--title" || strings.HasPrefix(a, "--title="):
			f.Title = value()
		case i == 0 && !strings.HasPrefix(a, "-"): // bd create "<title>" …
			f.Title = a
		}
	}
	return f
}

// isAssignment tells whether a command's word sets a variable for it (BD_ACTOR=x bd create …).
func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (i == 0 || r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// withoutHeredocs is a shell command line without the bodies of its here-documents: the lines after
// one that opens one (<<EOF, <<'EOF', <<-"EOF"), up to and with the line that closes it.
func withoutHeredocs(line string) string {
	var kept []string
	var closing []string // the delimiters of the here-documents open, in order
	for l := range strings.Lines(line) {
		text := strings.TrimRight(l, "\n")
		if len(closing) > 0 {
			if strings.TrimLeft(text, "\t") == closing[0] {
				closing = closing[1:]
			}
			continue
		}
		kept = append(kept, text)
		closing = heredocDelimiters(text)
	}
	return strings.Join(kept, "\n")
}

// heredocDelimiters are the delimiters of the here-documents a line opens, in order.
func heredocDelimiters(line string) []string {
	var delims []string
	for rest := line; ; {
		i := strings.Index(rest, "<<")
		if i < 0 {
			return delims
		}
		rest = strings.TrimPrefix(rest[i+2:], "-")
		if strings.HasPrefix(rest, "<") { // <<<, a here-string
			rest = rest[1:]
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		quote := ""
		if rest != "" && (rest[0] == '\'' || rest[0] == '"') {
			quote, rest = rest[:1], rest[1:]
		}
		end := strings.IndexFunc(rest, func(r rune) bool {
			return !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
		})
		if end < 0 {
			end = len(rest)
		}
		if end > 0 {
			delims = append(delims, rest[:end])
		}
		rest = strings.TrimPrefix(rest[end:], quote)
	}
}

// shellCommands splits a shell command line into its simple commands' words, roughly as a shell
// does: quotes are taken off, a command substitution stays whole in its word, and newlines, ;, &, |,
// ( and ) end a command.
func shellCommands(line string) [][]string {
	var commands [][]string
	var words []string
	var word strings.Builder
	inWord := false
	endWord := func() {
		if inWord {
			words = append(words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endCommand := func() {
		endWord()
		if len(words) > 0 {
			commands = append(commands, words)
			words = nil
		}
	}
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == ' ' || c == '\t':
			endWord()
		case strings.IndexByte("\n;&|()", c) >= 0:
			endCommand()
		case c == '#' && !inWord:
			for i < len(line) && line[i] != '\n' {
				i++
			}
			endCommand()
		case c == '\\' && i+1 < len(line):
			i++
			if line[i] != '\n' {
				word.WriteByte(line[i])
				inWord = true
			}
		case c == '\'':
			end := strings.IndexByte(line[i+1:], '\'')
			if end < 0 {
				end = len(line) - i - 1
			}
			word.WriteString(line[i+1 : i+1+end])
			i += end + 1
			inWord = true
		case c == '"':
			i = doubleQuoted(line, i+1, &word)
			inWord = true
		case c == '$' && i+1 < len(line) && line[i+1] == '(':
			end := substitutionEnd(line, i+2)
			word.WriteString(line[i:end])
			i = end - 1
			inWord = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	endCommand()
	return commands
}

// doubleQuoted writes the text of the double-quoted string that starts at line[i] to word, a command
// substitution in it as it is, and returns the index of its closing quote.
func doubleQuoted(line string, i int, word *strings.Builder) int {
	for ; i < len(line); i++ {
		switch c := line[i]; {
		case c == '"':
			return i
		case c == '\\' && i+1 < len(line) && strings.IndexByte("\"\\$`\n", line[i+1]) >= 0:
			i++
			if line[i] != '\n' {
				word.WriteByte(line[i])
			}
		case c == '$' && i+1 < len(line) && line[i+1] == '(':
			end := substitutionEnd(line, i+2)
			word.WriteString(line[i:end])
			i = end - 1
		default:
			word.WriteByte(c)
		}
	}
	return i
}

// substitutionEnd is the index just past the ) that closes the command substitution whose text starts
// at line[i], or len(line): parentheses inside it nest, and those in its quotes don't count.
func substitutionEnd(line string, i int) int {
	depth := 1
	for ; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '\'':
			if end := strings.IndexByte(line[i+1:], '\''); end >= 0 {
				i += end + 1
			}
		case '"':
			var skip strings.Builder
			i = doubleQuoted(line, i+1, &skip)
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return len(line)
}

// specProgress shows how a breakdown gets on, from what its session files: on a terminal, a busy
// line counting the epics and tickets filed; in plain output, a line for each epic filed and the
// counts every so often. The log gets a line for each epic.
type specProgress struct {
	say  tui.Printer
	log  *dispatch.Log
	busy *tui.Busy

	mu             sync.Mutex
	epics, tickets int

	stop    chan struct{} // closed once the breakdown is over
	counted chan struct{} // closed once the plain counts' goroutine has returned; nil when none runs
}

// startSpecProgress starts showing a breakdown's progress, the counts in plain output every every.
func startSpecProgress(say tui.Printer, log *dispatch.Log, every time.Duration) *specProgress {
	p := &specProgress{say: say, log: log, stop: make(chan struct{})}
	p.busy = say.Busy("breaking the spec down with claude… (ctrl+c stops)", "Breaking the spec down with Claude…",
		"(Ctrl+C stops)")
	if !say.Styled {
		p.counted = make(chan struct{})
		go p.count(every)
	}
	return p
}

// filed counts issues, filed by one Bash call, as their IDs (nil when unknown) say.
func (p *specProgress) filed(issues []filedIssue, ids []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, f := range issues {
		if !f.Epic {
			p.tickets++
			continue
		}
		p.epics++
		line := "SPEC: filed epic"
		if i < len(ids) {
			line += " " + ids[i]
		}
		if title := planLine(f.Title); title != "" {
			line += ": " + title
		}
		if !p.say.Styled {
			p.say.Say(line, "")
		}
		if p.log != nil {
			p.log.Line(time.Now(), line)
		}
	}
	p.busy.Detail(plural(p.epics, "epic") + " · " + plural(p.tickets, "ticket"))
}

// count says, every every, how many epics and tickets have been filed, until the breakdown is over.
func (p *specProgress) count(every time.Duration) {
	defer close(p.counted)
	tick := time.NewTicker(every)
	defer tick.Stop()
	began := time.Now()
	for {
		select {
		case <-p.stop:
			return
		case <-tick.C:
		}
		p.mu.Lock() // a plain printer has no lock of its own: filed prints under this one too
		p.say.Say(fmt.Sprintf("SPEC: breaking the spec down: %s and %s filed so far (%s)", plural(p.epics, "epic"),
			plural(p.tickets, "ticket"), command.ShortDuration(time.Since(began).Round(time.Minute))), "")
		p.mu.Unlock()
	}
}

// halt stops the plain counts, once the breakdown is over.
func (p *specProgress) halt() {
	close(p.stop)
	if p.counted != nil {
		<-p.counted
	}
}

// done ends the progress of a breakdown that answered: on a terminal, its line says what was filed.
func (p *specProgress) done() {
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.busy.Done(fmt.Sprintf("Spec broken down: %s, %s", plural(p.epics, "epic"), plural(p.tickets, "ticket")))
}

// end ends the progress of a breakdown that gave no answer, stopped by ctx or not.
func (p *specProgress) end(stopped bool) {
	p.halt()
	p.mu.Lock()
	defer p.mu.Unlock()
	if stopped {
		p.busy.Done("Spec breakdown stopped")
		return
	}
	p.busy.Warn(fmt.Sprintf("SPEC: not broken down, with %s and %s filed", plural(p.epics, "epic"),
		plural(p.tickets, "ticket")))
}
