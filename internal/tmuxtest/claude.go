package tmuxtest

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Claude is a fake Claude Code for a tmux pane or a cmux terminal, for faketool.Func: it reads the --settings file it is
// given and runs its hooks as Claude Code would, with the input Claude Code gives them, as it goes
// through a script of steps. Its folder, $FAKE_CLAUDE (see ClaudeEnv), holds the script, one step a
// line: script, or script.resume for a session it resumes (--resume <ID>). It logs what it did
// there (log), and keeps its sessions' transcripts there too, where Claude Code keeps them
// (projects/<folder>/<ID>.jsonl).
//
// The steps, each logged as it ends; a command runs with sh -c in the working directory, its ticket
// as $ID (the folder's name, as a ticket's worktree is named):
//
//	trust               the dialog asking whether to trust the folder, which runs no hook, until a line is typed
//	start               the session starts (SessionStart; source startup, or resume) and bracketed paste is on
//	prompt              a pasted prompt, up to the paste's end and an Enter, is submitted (UserPromptSubmit) and
//	                    kept in prompt-<n>, from 1, counting those of the sessions before; or a line typed,
//	                    as a cmux before 0.65.0 gives a prompt, the prompt being the text of the file it names
//	                    after @, if it does, as Claude Code attaches it
//	sleep <duration>    nothing for that long, as a turn that takes time
//	say <text>          the line of text is shown, as a dialog that runs no hook
//	bash <command>      the Bash tool runs the command (PreToolUse, PostToolUse)
//	write <path> <text> the Write tool writes the line of text to the file (PreToolUse, PostToolUse)
//	permission <command> the Bash tool asks for permission to run the command, and runs it once a line is
//	                    typed in answer (PreToolUse, PermissionRequest, Notification permission_prompt, then
//	                    PostToolUse once it ran: none as it is allowed)
//	stop                the turn ends (Stop)
//	keys                every line typed from here on is kept in keys, until the line "exit"
//	idle                every line typed from here on, submitted, is a turn that ends at once
//	                    (UserPromptSubmit, Stop), until the input ends
//	exit <status>       the process exits, as Claude Code crashing would: no SessionEnd
//	end                 the session ends (SessionEnd, reason exit) and the process exits 0
func Claude(args []string) int {
	c, err := newClaude(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude:", err)
		return 1
	}
	script := "script"
	if c.resumed {
		script = "script.resume"
	}
	b, err := os.ReadFile(filepath.Join(c.dir, script))
	if err != nil {
		fmt.Fprintln(os.Stderr, "fake claude:", err)
		return 1
	}
	for line := range strings.Lines(string(b)) {
		step, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
		if step == "" {
			continue
		}
		status, done, err := c.step(step, arg)
		if err != nil {
			c.logf("%s failed: %v", step, err)
			fmt.Fprintf(os.Stderr, "fake claude: %s: %v\n", step, err)
			return 1
		}
		c.logf("%s %s", step, arg)
		if done {
			return status
		}
	}
	return 0
}

// ClaudeEnv is the environment WriteFunc gives the fake Claude whose folder is dir.
func ClaudeEnv(dir string) string { return "FAKE_CLAUDE=" + dir }

// claude is one run of the fake Claude.
type claude struct {
	dir, cwd, id   string // its folder, its working directory, and that directory's name
	hooks          map[string][]hookGroup
	session        string // the session's ID
	transcript     string // its transcript
	resumed        bool
	in             *bufio.Reader // the terminal
	permissionMode string
}

// hookGroup is one entry of an event's hooks in Claude Code's settings.
type hookGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
}

func newClaude(args []string) (*claude, error) {
	c := &claude{dir: os.Getenv("FAKE_CLAUDE"), in: bufio.NewReader(os.Stdin), permissionMode: "default"}
	if c.dir == "" {
		return nil, fmt.Errorf("FAKE_CLAUDE is not set")
	}
	var err error
	if c.cwd, err = os.Getwd(); err != nil {
		return nil, err
	}
	c.id = filepath.Base(c.cwd)
	for i := 0; i < len(args); i++ {
		name, value, inline := strings.Cut(args[i], "=")
		if name != "--settings" && name != "--resume" {
			continue
		}
		if !inline && i+1 < len(args) {
			i++
			value = args[i]
		}
		if name == "--resume" {
			c.session, c.resumed = value, true
		} else if err := c.readSettings(value); err != nil {
			return nil, err
		}
	}
	if c.session == "" {
		u := make([]byte, 16)
		_, _ = rand.Read(u) // never fails
		c.session = fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
	}
	c.transcript = filepath.Join(c.dir, "projects", strings.ReplaceAll(c.cwd, string(filepath.Separator), "-"),
		c.session+".jsonl")
	return c, nil
}

// readSettings reads the hooks in the settings file at path.
func (c *claude) readSettings(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	c.hooks = s.Hooks
	return nil
}

// logf appends a line to the log.
func (c *claude) logf(format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(c.dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintf(f, "%s\n", fmt.Sprintf(format, args...)) // the log is the tests' to read: a lost line shows
}

// step takes one step; done says the process ends, with status.
func (c *claude) step(step, arg string) (status int, done bool, err error) {
	switch step {
	case "trust":
		return 0, false, c.trust()
	case "start":
		return 0, false, c.start()
	case "prompt":
		return 0, false, c.prompt()
	case "sleep":
		d, err := time.ParseDuration(arg)
		time.Sleep(d)
		return 0, false, err
	case "say":
		fmt.Println(arg)
		return 0, false, nil
	case "bash":
		return 0, false, c.bash(arg)
	case "write":
		path, text, _ := strings.Cut(arg, " ")
		if !filepath.IsAbs(path) {
			path = filepath.Join(c.cwd, path)
		}
		input := map[string]any{"file_path": path, "content": text + "\n"}
		return 0, false, c.tool("Write", input, func() error { return os.WriteFile(path, []byte(text+"\n"), 0o644) })
	case "permission":
		return 0, false, c.permission(arg)
	case "stop":
		fmt.Println("● Done.")
		c.hook("Stop", "", map[string]any{"stop_hook_active": false})
		return 0, false, nil
	case "keys":
		return 0, false, c.keys()
	case "idle":
		return 0, true, c.idle()
	case "exit":
		n, err := strconv.Atoi(arg)
		return n, true, err
	case "end":
		c.hook("SessionEnd", "", map[string]any{"reason": "exit"})
		return 0, true, nil
	}
	return 0, false, fmt.Errorf("no such step: %s", step)
}

// start starts the session: bracketed paste on, the transcript begun, SessionStart.
func (c *claude) start() error {
	fmt.Print("\x1b[?2004h")
	source := "startup"
	if c.resumed {
		source = "resume"
	}
	fmt.Printf("fake claude: session %s (%s)\n> ", c.session, source)
	if err := os.MkdirAll(filepath.Dir(c.transcript), 0o755); err != nil {
		return err
	}
	if err := c.record(map[string]any{"type": "system", "content": "session " + source}); err != nil {
		return err
	}
	c.hook("SessionStart", "", map[string]any{"source": source})
	c.logf("session %s %s", c.session, source)
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// record appends an entry to the transcript.
func (c *claude) record(entry map[string]any) error {
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(c.transcript, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// trust shows the dialog asking whether to trust the folder, as Claude Code does before a session
// starts in a folder it hasn't been told to trust, and waits for the line that answers it, which
// clears the screen.
func (c *claude) trust() error {
	fmt.Printf("Quick safety check: Is this a project you created or one you trust?\n" +
		"❯ 1. Yes, I trust this folder\n  2. No, exit\n")
	answer, err := c.in.ReadString('\n')
	if err != nil {
		return fmt.Errorf("the input ended before the trust dialog was answered: %w", err)
	}
	c.logf("trust answered %q", strings.TrimSpace(answer))
	fmt.Print("\x1b[H\x1b[2J") // the dialog is gone from the screen, as Claude Code draws its own over it
	return nil
}

// The bracketed paste's markers around a pasted text.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// attached is a file named after @ in a message, which Claude Code attaches to it.
var attached = regexp.MustCompile(`(?:^|\s)@(\S+)`)

// prompt reads a pasted prompt up to the line its paste ends on, which the Enter after it ends, and
// submits it; or a line typed, as it is, or the text of the file it attaches (@).
func (c *claude) prompt() error {
	var lines []string
	for {
		line, err := c.in.ReadString('\n')
		if err != nil {
			return fmt.Errorf("the input ended before a pasted prompt was submitted: %w", err)
		}
		line = strings.TrimSuffix(line, "\n")
		if len(lines) == 0 && !strings.Contains(line, pasteStart) {
			typed, err := c.typed(line)
			if err != nil {
				return err
			}
			lines = append(lines, typed)
			break
		}
		end := strings.Contains(line, pasteEnd)
		lines = append(lines, strings.ReplaceAll(strings.ReplaceAll(line, pasteStart, ""), pasteEnd, ""))
		if end {
			break
		}
	}
	text := strings.Join(lines, "\n")
	n := 1 // after the prompts of the sessions before
	for exists(filepath.Join(c.dir, "prompt-"+strconv.Itoa(n))) {
		n++
	}
	if err := os.WriteFile(filepath.Join(c.dir, "prompt-"+strconv.Itoa(n)), []byte(text), 0o644); err != nil {
		return err
	}
	entry := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}}
	if err := c.record(entry); err != nil {
		return err
	}
	c.hook("UserPromptSubmit", "", map[string]any{"prompt": text})
	return nil
}

// typed is the prompt in a line typed: the line, or the text of the file it attaches (@).
func (c *claude) typed(line string) (string, error) {
	m := attached.FindStringSubmatch(line)
	if m == nil {
		return line, nil
	}
	path := m[1]
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.cwd, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("the prompt's attachment: %w", err)
	}
	c.logf("attached %s", m[1])
	return string(b), nil
}

// bash runs the Bash tool on command.
func (c *claude) bash(command string) error {
	return c.tool("Bash", map[string]any{"command": command}, func() error { return c.run(command) })
}

// run runs command as the Bash tool does, showing its output.
func (c *claude) run(command string) error {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir, cmd.Env = c.cwd, append(os.Environ(), "ID="+c.id)
	out, err := cmd.CombinedOutput()
	fmt.Printf("%s", out)
	if err != nil {
		return fmt.Errorf("%s: %w: %s", command, err, out)
	}
	return nil
}

// tool uses the tool with input, running it with use between its PreToolUse and PostToolUse.
func (c *claude) tool(name string, input map[string]any, use func() error) error {
	fmt.Printf("● %s(%v)\n", name, input)
	c.hook("PreToolUse", name, map[string]any{"tool_name": name, "tool_input": input})
	err := use()
	event := "PostToolUse"
	if err != nil {
		event = "PostToolUseFailure"
	}
	c.hook(event, name, map[string]any{"tool_name": name, "tool_input": input})
	return err
}

// permission uses the Bash tool on command, asking for permission first, as Claude Code does: after
// its PreToolUse, its dialog shown (PermissionRequest, Notification permission_prompt) until a line is
// typed in answer, and then cleared from the screen as the command runs, which runs no hook until its
// PostToolUse.
func (c *claude) permission(command string) error {
	input := map[string]any{"command": command}
	return c.tool("Bash", input, func() error {
		fmt.Printf("Bash command\n  %s\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n", command)
		c.hook("PermissionRequest", "Bash", map[string]any{"tool_name": "Bash", "tool_input": input})
		c.hook("Notification", "", map[string]any{"message": "Claude needs your permission to use Bash",
			"notification_type": "permission_prompt"})
		answer, err := c.in.ReadString('\n')
		if err != nil {
			return fmt.Errorf("the input ended before the permission prompt was answered: %w", err)
		}
		c.logf("permission answered %q", strings.TrimSpace(answer))
		fmt.Printf("\x1b[2J\x1b[H● Bash(%s)\n  Running…\n", command)
		return c.run(command)
	})
}

// keys keeps every line typed, until the line exit.
func (c *claude) keys() error {
	for {
		line, err := c.in.ReadString('\n')
		if err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(c.dir, "keys"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, err = f.WriteString(line)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil || strings.TrimSpace(line) == "exit" {
			return err
		}
	}
}

// idle takes each line typed as a prompt whose turn ends at once, until the input ends.
func (c *claude) idle() error {
	for {
		line, err := c.in.ReadString('\n')
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		c.logf("typed %q", strings.TrimSpace(line))
		c.hook("UserPromptSubmit", "", map[string]any{"prompt": line})
		c.hook("Stop", "", map[string]any{"stop_hook_active": false})
	}
}

// hook runs the hooks of event that match tool ("" for an event without one) with input, as Claude
// Code runs them: each command with sh -c in the working directory, its input as JSON on stdin with
// the session's fields, for up to a minute. Their exit status is ignored: a reporting hook blocks
// nothing.
func (c *claude) hook(event, tool string, input map[string]any) {
	in := map[string]any{"session_id": c.session, "transcript_path": c.transcript, "cwd": c.cwd,
		"hook_event_name": event, "permission_mode": c.permissionMode}
	maps.Copy(in, input)
	b, err := json.Marshal(in)
	if err != nil {
		c.logf("%s hook input: %v", event, err)
		return
	}
	for _, g := range c.hooks[event] {
		if !matches(g.Matcher, tool) {
			continue
		}
		for _, h := range g.Hooks {
			if h.Type != "command" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", h.Command)
			cmd.Dir, cmd.Stdin = c.cwd, strings.NewReader(string(b))
			cmd.Env = append(os.Environ(), "CLAUDE_PROJECT_DIR="+c.cwd)
			if out, err := cmd.CombinedOutput(); err != nil {
				c.logf("%s hook failed: %v: %s", event, err, out)
			}
			cancel()
		}
	}
}

// matches reports whether a hook group's matcher matches tool, as Claude Code's do: "" and "*" match
// everything, and anything else is a regular expression for the whole tool name.
func matches(matcher, tool string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	return err == nil && re.MatchString(tool)
}
