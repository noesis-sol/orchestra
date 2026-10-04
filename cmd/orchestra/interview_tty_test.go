//go:build darwin || linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// interviewChildren is what bd lists under the epic f-1 an interview files: two tickets, the
// second after the first.
const interviewChildren = `[` +
	`{"id":"f-1.1","title":"Add the JSON encoder","status":"open","issue_type":"feature","priority":1,` +
	`"metadata":{"files":["enc.go"]},"dependencies":[{"issue_id":"f-1.1","depends_on_id":"f-1","type":"parent-child"}]},` +
	`{"id":"f-1.2","title":"Add the --json flag","status":"open","issue_type":"task","priority":2,` +
	`"metadata":{"files":["main.go","README.md"]},` +
	`"dependencies":[{"issue_id":"f-1.2","depends_on_id":"f-1.1","type":"blocks"}]}]`

// interviewTools puts on PATH a claude that plays the interview and a bd that knows the epic f-1
// and its children, and a herdr that fails; no Herdr pane is known (HERDR_PANE_ID), so the session
// is on the terminal. claude records its arguments (claude-args, one per
// line in brackets), its folder, process group and instructions (claude-prompt), and whether it
// had the terminal; sends orchestra SIGINT, as Ctrl+C at the terminal does; and, given an epic,
// names it in .orchestra/run/feature.json as the instructions say. bd records its calls in
// bd-calls, each argument followed by |. It returns the folder they keep their files in.
func interviewTools(t *testing.T, epic string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("children", interviewChildren)
	name := ""
	if epic != "" {
		name = `printf '{"epic":"%s"}\n' '` + epic + `' > .orchestra/run/feature.json`
	}
	// The pause after SIGINT lets it reach orchestra while the session is still on.
	faketool.Write(t, dir, "claude", `#!/bin/sh
d='`+dir+`'
printf '[%s]\n' "$@" > "$d/claude-args"
pwd -P > "$d/claude-cwd"
ps -o pgid= -p $$ | tr -d ' ' > "$d/claude-pgid"
[ -t 0 ] && [ -t 1 ] && echo yes > "$d/claude-tty"
cp "$2" "$d/claude-prompt"
kill -INT $PPID
sleep 1
`+name+`
echo 'fake claude: bye'
`)
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
for a in "$@"; do printf '%s|' "$a"; done >> "$d/bd-calls"; echo >> "$d/bd-calls"
case "$1" in
show)
	case "$2" in
	f-1) echo '[{"id":"f-1","title":"JSON output","description":"Machine-readable output.","status":"open","issue_type":"epic","priority":1}]' ;;
	f-1.*) echo "[{\"id\":\"$2\",\"status\":\"open\",\"issue_type\":\"task\"}]" ;;
	*) echo "Error: no issue found matching $2" >&2; exit 1 ;;
	esac ;;
list) case "$*" in *"--parent f-1") cat "$d/children" ;; *) echo '[]' ;; esac ;;
ready) echo '[]' ;;
esac
`)
	faketool.Write(t, dir, "herdr", "#!/bin/sh\necho 'herdr: not in this test' >&2\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HERDR_PANE_ID", "")
	return dir
}

// describeFeature answers the question with a new feature, a list whose first line starts with -,
// as claude's options do.
func describeFeature(t *testing.T, term *fakeTerminal) {
	t.Helper()
	term.waitFor(t, "> Current tickets: none, all done")
	term.typeKeys(t, keyDown+keyEnter, false)
	term.waitFor(t, describing)
	term.typeKeys(t, "- Add a --json flag"+keyNewLine+"- to the list command"+keyEnter, false)
}

// screenOf is what orchestra has shown on the terminal, with plain line ends.
func screenOf(term *fakeTerminal) string {
	return strings.ReplaceAll(term.screen.String(), "\r\n", "\n")
}

// "New feature" hands the terminal to claude in the main checkout, in orchestra's process group,
// with the interview's instructions appended to its system prompt and the description as its first
// message; Ctrl+C meanwhile is claude's. Once it exits, orchestra shows the epic it filed and runs
// it on y, as --feature runs the epic it files: with none of its tickets ready, as bd answers here,
// the run says so, scoped to the epic.
func TestInterviewFilesTheFeatureAndRunsItsEpic(t *testing.T) {
	dir := interviewTools(t, "f-1")
	term, repo, exit := runOnTerminal(t)
	describeFeature(t, term)
	term.waitFor(t, "Start the run on f-1 (2 tickets)? [y/N]")
	term.typeKeys(t, "y\n", false)
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}

	prompt := filepath.Join(realPath(repo), project.RunPath("interview-prompt.md")) // the repository as git names it
	if args := read(t, filepath.Join(dir, "claude-args")); args != "[--append-system-prompt-file]\n["+prompt+"]\n"+
		"[--]\n[- Add a --json flag\n- to the list command]\n" {
		t.Errorf("claude ran with:\n%s", args)
	}
	if cwd, want := strings.TrimSpace(read(t, filepath.Join(dir, "claude-cwd"))), realPath(repo); cwd != want {
		t.Errorf("claude ran in %s, want the main checkout %s", cwd, want)
	}
	if pgid := strings.TrimSpace(read(t, filepath.Join(dir, "claude-pgid"))); pgid != strconv.Itoa(syscall.Getpgrp()) {
		t.Errorf("claude ran in process group %s, orchestra in %d: it can't read the terminal", pgid, syscall.Getpgrp())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-tty")); err != nil {
		t.Errorf("claude didn't have the terminal: %v", err)
	}
	instructions := read(t, filepath.Join(dir, "claude-prompt"))
	for _, want := range []string{
		// the grilling method, and its notice
		"Copyright (c) 2026 Matt Pocock", "Permission is hereby granted, free of charge",
		"**design\ntree**", "**frontier**", "❓ **Q1** - **<question title>**", "➡️ <your recommended answer>",
		"dispatch a sub-agent to explore", "Do not act on it until the user confirms",
		// what orchestra adds: context, proposal, filing and handing back
		"The user's first message is the feature's description", "CLAUDE.md or AGENTS.md", "Only read.",
		"File nothing until the user agrees", "at most 60 characters", "acceptance criteria", "task, feature, bug or chore",
		"from 0 (critical) to 4", "`solo`", "`human`", "bd list --status open",
		"bd create --type epic", "bd create --parent <epic ID>", `--metadata '{"files":["a.go","b.go"]}'`,
		"bd dep add <the ticket that waits> <the ticket it waits for>",
		`printf '{"epic":"%s"}\n' '<epic ID>' > .orchestra/run/feature.json`, "`/exit`",
		// orchestra can't end the session on its terminal: claude says how the user does
		"## On orchestra's terminal", "tell the user to type `/exit` to hand back",
	} {
		if !strings.Contains(instructions, want) {
			t.Errorf("the instructions lack %q", want)
		}
	}
	if b, err := os.ReadFile(prompt); err != nil || string(b) != instructions {
		t.Errorf("claude wasn't given the instructions orchestra wrote: %v", err)
	}

	out := screenOf(term)
	for _, want := range []string{
		"New feature:\n  - Add a --json flag\n  - to the list command\n",
		"Talking the feature through with claude", "fake claude: bye",
		"Epic: f-1 JSON output\n  Machine-readable output.\n",
		"f-1.1  feature P1  Add the JSON encoder\n", "files: enc.go\n",
		"f-1.2  task    P2  Add the --json flag\n", "files: main.go, README.md\n", "after: f-1.1\n",
		"○ Nothing under f-1 is ready to run", "f-1.1 (not started)", "f-1.2 (not started)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the terminal lacks %q:\n%s", want, out)
		}
	}
	calls := read(t, filepath.Join(dir, "bd-calls"))
	for _, want := range []string{"show|f-1|--json|\n", "list|--json|--limit|0|--parent|f-1|\n",
		"ready|--json|--limit|0|--exclude-label|human|--exclude-type|epic|--parent|f-1|\n"} {
		if !strings.Contains(calls, want) {
			t.Errorf("bd calls lack %q:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "create|") {
		t.Errorf("orchestra filed tickets itself:\n%s", calls)
	}
	events := read(t, filepath.Join(repo, project.RunPath(dispatch.EventsName)))
	if !strings.Contains(events, `"scope":"f-1"`) || !strings.Contains(events, `"feature":"- Add a --json flag\n- to the list command"`) {
		t.Errorf("the run's start doesn't name the epic and the feature:\n%s", events)
	}
}

// On n, nothing runs, and orchestra says how to run the epic later.
func TestInterviewEpicDeclinedRunsNothing(t *testing.T) {
	dir := interviewTools(t, "f-1")
	term, _, exit := runOnTerminal(t)
	describeFeature(t, term)
	term.waitFor(t, "Start the run on f-1 (2 tickets)? [y/N]")
	term.typeKeys(t, "n\n", false)
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if out := screenOf(term); !strings.Contains(out, "Start it later with: orchestra --ticket f-1\n") {
		t.Errorf("the terminal lacks how to start it later:\n%s", out)
	}
	if calls := read(t, filepath.Join(dir, "bd-calls")); strings.Contains(calls, "--exclude-type|epic|--parent|f-1|") {
		t.Errorf("the epic was run:\n%s", calls)
	}
}

// An interview that names no epic bd knows, names it in a file that can't be read, or files
// nothing, runs nothing and exits 0; a feature.json left by an earlier interview doesn't count.
func TestInterviewThatFiledNothingRunsNothing(t *testing.T) {
	for _, tc := range []struct {
		name, epic, earlier, stderr string
	}{
		{"nothing filed", "", `{"epic":"f-1"}`, ""},
		{"unknown epic", "f-9", "", "orchestra can't find f-9, the epic the interview named: bd show f-9 --json: exit status 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := interviewTools(t, tc.epic)
			term, repo, exit := runOnTerminal(t)
			if tc.earlier != "" {
				if err := os.MkdirAll(filepath.Join(repo, project.Dir, project.RunName), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, project.RunPath(project.FeatureName)),
					[]byte(tc.earlier), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			describeFeature(t, term)
			code, stderr := exit()
			if code != dispatch.ExitOK || !strings.Contains(stderr, tc.stderr) {
				t.Errorf("exit %d, stderr:\n%s", code, stderr)
			}
			if out := screenOf(term); !strings.Contains(out, "fake claude: bye\n"+noFeature+"\n") {
				t.Errorf("the terminal lacks %q:\n%s", noFeature, out)
			}
			if calls := read(t, filepath.Join(dir, "bd-calls")); strings.Contains(calls, "--parent|f-1|") {
				t.Errorf("went on to an epic:\n%s", calls)
			}
		})
	}
}

// hideClaude takes every claude off PATH but keeps git, and the fakes in dir first.
func hideClaude(t *testing.T, dir string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(git, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	kept := []string{dir}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(p, "claude")); err != nil {
			kept = append(kept, p)
		}
	}
	t.Setenv("PATH", strings.Join(kept, string(os.PathListSeparator)))
}

// Without claude there is no interview: orchestra says why and plans the feature with its organs,
// as it did before, which need claude too.
func TestInterviewWithoutClaudePlansWithTheOrgans(t *testing.T) {
	dir := featureTools(t, featureScreenOK, `{"type":"result","is_error":false,"structured_output":`+featurePlanJSON+`}`, 0)
	if err := os.Remove(filepath.Join(dir, "claude")); err != nil {
		t.Fatal(err)
	}
	hideClaude(t, dir)
	term, _, exit := runOnTerminal(t)
	describeFeature(t, term)
	code, stderr := exit()
	if code != dispatch.ExitSetup || !strings.Contains(stderr, "orchestra couldn't screen the request:") {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if out := screenOf(term); !strings.Contains(out,
		"orchestra can't talk the feature through with claude (claude not found): its organs plan it.\n"+
			"screening the request with claude…") {
		t.Errorf("the terminal lacks why there is no interview:\n%s", out)
	}
}
