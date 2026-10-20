//go:build unix

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// filingBd is a bd that files what bd create is given, its flags as --flag value or --flag=value, as
// a line of JSON in filed: the first issue without a parent as s-1, each other as its parent's ID and
// its number among the parent's children; with --json it prints {"id":…}, else the ID. bd show and
// bd list (by --parent, or by the spec's --metadata-field) read filed, bd show --json saying as bd
// does when it has no issue by the ID; bd update --set-metadata k=v adds k to the issue's metadata,
// once; bd dep cycles prints cycles, else none; bd ready finds nothing ready. Each call goes into
// bd-calls, its arguments followed by |. Titles, acceptance criteria, labels and metadata go into
// filed as they are given: tests give them without characters JSON escapes.
const filingBd = `#!/bin/sh
d=$(dirname "$0")
for a in "$@"; do printf '%s|' "$a"; done >> "$d/bd-calls"; echo >> "$d/bd-calls"
cmd=$1; shift; first=$1
parent= field= type=task title= acceptance= metadata='{}' labels= json= set=
while [ $# -gt 0 ]; do
	case "$1" in
	--parent) parent=$2; shift ;;
	--metadata-field) field=$2; shift ;;
	--type) type=$2; shift ;;
	--title) title=$2; shift ;;
	--acceptance) acceptance=$2; shift ;;
	--metadata) metadata=$2; shift ;;
	--labels) labels=$2; shift ;;
	--set-metadata) set=$2; shift ;;
	--parent=*) parent=${1#*=} ;;
	--type=*) type=${1#*=} ;;
	--title=*) title=${1#*=} ;;
	--acceptance=*) acceptance=${1#*=} ;;
	--metadata=*) metadata=${1#*=} ;;
	--labels=*) labels=${1#*=} ;;
	--json) json=1 ;;
	esac
	shift
done
listed() { printf '['; grep "$1" "$d/filed" 2>/dev/null | paste -sd, -; printf ']\n'; }
case "$cmd" in
create)
	if [ -z "$parent" ]; then id=s-1; else
		n=$(grep -c "\"parent\":\"$parent\"" "$d/filed" 2>/dev/null); id="$parent.$((n+1))"
	fi
	[ -z "$labels" ] || labels=$(printf '"%s"' "$labels" | sed 's/,/","/g')
	printf '{"id":"%s","title":"%s","status":"open","issue_type":"%s","parent":"%s","acceptance_criteria":"%s","labels":[%s],"metadata":%s}\n' \
		"$id" "$title" "$type" "$parent" "$acceptance" "$labels" "$metadata" >> "$d/filed"
	if [ -n "$json" ]; then printf '{"id":"%s"}\n' "$id"; else echo "$id"; fi ;;
show)
	l=$(grep "\"id\":\"$first\"," "$d/filed" 2>/dev/null)
	[ -n "$l" ] || {
		[ -n "$json" ] && echo '{"error":"no issues found matching the provided IDs"}'
		echo "Error: no issue found matching \"$first\"" >&2; exit 1
	}
	echo "[$l]" ;;
update)
	if [ -n "$set" ]; then
		kv="\"${set%%=*}\":\"${set#*=}\""
		sed "/\"id\":\"$first\",/{s|{}}\$|{@}}|;s|}}\$|,$kv}}|;s|{@,|{|;}" "$d/filed" > "$d/filed.new"
		mv "$d/filed.new" "$d/filed"
	fi ;;
list)
	if [ -n "$parent" ]; then listed "\"parent\":\"$parent\""
	elif [ -n "$field" ]; then listed "\"spec\":\"${field#spec=}\""
	else echo '[]'; fi ;;
dep) cat "$d/cycles" 2>/dev/null || echo '[]' ;;
ready) echo '[]' ;;
esac
`

// helpWithPrompts is the line of claude's --help that lists --permission-prompts.
const helpWithPrompts = "  --permission-prompts <target>         Who answers permission prompts with\n"

// fakeSession writes into dir a claude that answers --help with help, having first run dir's
// on-help, a shell script, if there is one, and, as a session, keeps its arguments, one a line, in
// claude-args, and its first message in claude-in, sets spec, sha and commit to what the message
// gives, then runs body, a shell script that prints the session's stream.
func fakeSession(t *testing.T, dir, help, body string) {
	t.Helper()
	faketool.Write(t, dir, "claude", `#!/bin/sh
d='`+dir+`'
if [ "$1" = --help ]; then
	if [ -f "$d/on-help" ]; then sh "$d/on-help" > /dev/null; fi
	printf '%s' '`+help+`'; exit 0
fi
printf '%s\n' "$@" > "$d/claude-args"
cat > "$d/claude-in"
spec=$(sed -n 's/^- path: //p' "$d/claude-in")
sha=$(sed -n 's/^- sha256: //p' "$d/claude-in")
commit=$(sed -n 's/^- commit: //p' "$d/claude-in")
echo '{"type":"system","subtype":"init","session_id":"sess-1","cwd":"x"}'
`+body)
}

// streamed is the stream line of one message, made from v.
func streamed(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// filing is the part of a fake session's body that runs cmd, a shell command filing issues with bd,
// as the Bash call use, streaming its use and its result, cmd's output.
func filing(t *testing.T, use, cmd string) string {
	t.Helper()
	call := streamed(t, map[string]any{"type": "assistant", "session_id": "sess-1", "message": map[string]any{
		"content": []any{map[string]any{"type": "tool_use", "id": use, "name": "Bash",
			"input": map[string]any{"command": cmd, "description": "File issues"}}}}})
	return "cat <<'EOS'\n" + call + "\nEOS\n" +
		"out=$(" + cmd + ")\n" +
		`printf '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + use +
		`","content":"%s","is_error":false}]}}\n' "$(printf '%s' "$out" | paste -sd'|' - | sed 's/|/\\n/g')"` + "\n"
}

// answering is the part of a fake session's body that ends it with the answer a, as claude's result
// message, successful.
func answering(t *testing.T, a specAnswer) string {
	t.Helper()
	return "cat <<'EOS'\n" + streamed(t, map[string]any{"type": "result", "subtype": "success", "is_error": false,
		"result": "Filed.", "structured_output": a, "session_id": "sess-1", "total_cost_usd": 1.25, "num_turns": 30}) +
		"\nEOS\n"
}

// The root epic's metadata, the others', and a ticket's files, as a fake session's shell gives them.
const (
	rootMeta = `"{\"spec\":\"$spec\",\"spec_sha256\":\"$sha\",\"spec_commit\":\"$commit\"}"`
	specMeta = `"{\"spec\":\"$spec\"}"`
)

// ticketMeta is a ticket's metadata as a fake session's shell gives it: the spec, and its files.
func ticketMeta(files ...string) string {
	return `"{\"spec\":\"$spec\",\"files\":[\"` + strings.Join(files, `\",\"`) + `\"]}"`
}

// fileTree is the part of a fake session's body that files an app's tree: the root epic s-1, the epic
// s-1.1 with the ticket s-1.1.1, and the ticket s-1.2, which waits for s-1.1.1; the root epic and the
// epic in one call, the two tickets in another.
func fileTree(t *testing.T) string {
	t.Helper()
	return filing(t, "toolu_1", "bd create --type epic --priority 1 --title 'An app' --metadata "+rootMeta+
		" --silent && bd create --parent s-1 --type epic --title 'Listing' --metadata "+specMeta+" --silent") +
		filing(t, "toolu_2", "bd create --parent s-1.1 --type task --title 'List the things' --acceptance 'Lists them' "+
			"--metadata "+ticketMeta("list.go")+" --silent && bd create --parent s-1 --type task --title 'Sort the things' "+
			"--acceptance 'Sorts them' --metadata "+ticketMeta("sort.go")+" --deps blocked-by:s-1.1.1 --silent")
}

// breakdownFixture is a repository set up for orchestra, inside a Herdr pane, without notifications,
// triage or the run report, with filingBd, a herdr that fails and fakeSession's claude on PATH, given
// help and body; a spec outside it, which it returns with the folder of the fakes.
func breakdownFixture(t *testing.T, help, body string) (repo, spec, tools string) {
	t.Helper()
	repo = specFixture(t)
	for k, v := range map[string]string{"NOTIFY": "0", "TRIAGE": "0", "REVIEW": "0", "ORCHESTRA_SPEC_TIMEOUT": "",
		"ORCHESTRA_SPEC_EFFORT": ""} {
		setenv(t, k, v)
	}
	tools = t.TempDir()
	faketool.Write(t, tools, "bd", filingBd)
	faketool.Write(t, tools, "herdr", "#!/bin/sh\necho 'herdr: not in this test' >&2\nexit 1\n")
	fakeSession(t, tools, help, body)
	onPath(t, tools)
	spec = writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n\nIt lists things, sorted.\n")
	return repo, spec, tools
}

// answerTree is the rest of an app's tree, as the headless breakdown's answer gives it under its root
// epic s-1: the epic Listing, with the tickets List the things (solo) and Name the things, which waits
// for it; and the ticket Sort the things, under the root epic, which waits for List the things too,
// given before it. filingBd files them as s-1.1, s-1.1.1, s-1.1.2 and s-1.2.
var answerTree = []specIssue{
	{Key: "listing", Type: "epic", Priority: 1, Title: "Listing", Description: "Lists things."},
	{Key: "sort", Type: "task", Priority: 2, Title: "Sort the things", Description: "Sorts.", Acceptance: "Sorts them",
		Files: []string{"sort.go"}, WaitsFor: []string{"list"}},
	{Key: "list", Parent: "listing", Type: "task", Priority: 1, Title: "List the things", Description: "Lists.",
		Acceptance: "Lists them", Labels: []string{"solo"}, Files: []string{"list.go"}},
	{Key: "names", Parent: "listing", Type: "task", Priority: 2, Title: "Name the things", Description: "Names.",
		Acceptance: "Names them", Files: []string{"names.go"}, WaitsFor: []string{" list "}},
}

// A spec run without --refine breaks the spec down in claude -p: the session files the root epic with
// bd and answers with it, the rest of the tree and its assumptions. orchestra files the rest, each
// issue under its parent with its labels, its files and the spec in its metadata and its blocks
// links, notes the assumptions on the root epic and sets its SHA-256 and commit last; the tree passes
// the checks, and the run is scoped to the root epic. Plain output says each epic as it is filed, and
// the log has them, the session's cost, what orchestra filed and the assumptions.
func TestSpecBreakdownRunsTheRootEpicItFiles(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	root := filing(t, "toolu_1", "bd create --type epic --priority 1 --title 'An app' --metadata "+specMeta+" --silent")
	repo, spec, tools := breakdownFixture(t, helpWithPrompts, root+answering(t, specAnswer{Root: "s-1",
		Assumptions: []string{" Things are sorted by name, A to Z (Sort the things). ", ""}, Issues: answerTree}))
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	const kept = ".orchestra/specs/app.md"
	for _, want := range []string{" breaking the spec down with claude… (ctrl+c stops)\n",
		" SPEC: filed epic s-1: An app\n", " SPEC: filed epic s-1.1: Listing\n",
		"broke the spec down into s-1 (2 epics, 3 tickets)\n",
		"Assumed where the spec leaves a choice open:\n  - Things are sorted by name, A to Z (Sort the things).\n",
		" ○ Nothing under s-1 is ready to run\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stderr, "warning:") {
		t.Errorf("a tree with nothing amiss has warnings:\n%s", stderr)
	}
	if in := read(t, filepath.Join(tools, "claude-in")); !strings.Contains(in, "- path: "+kept+"\n") ||
		!strings.Contains(in, "- time limit: 1h, after which orchestra stops this session\n") {
		t.Errorf("the first message:\n%s", in)
	}
	const meta = `|--metadata={"files":["%s"],"spec":".orchestra/specs/app.md"}|`
	calls := read(t, filepath.Join(tools, "bd-calls"))
	var at []int
	for _, want := range []string{
		"create|--json|--title=Listing|--description=Lists things.|--type=epic|--priority=1|--parent=s-1|" +
			`--metadata={"spec":".orchestra/specs/app.md"}|`,
		"create|--json|--title=Sort the things|--description=Sorts.|--type=task|--priority=2|--acceptance=Sorts them|" +
			"--parent=s-1" + fmt.Sprintf(meta, "sort.go"),
		"create|--json|--title=List the things|--description=Lists.|--type=task|--priority=1|--acceptance=Lists them|" +
			"--parent=s-1.1|--labels=solo" + fmt.Sprintf(meta, "list.go"),
		"create|--json|--title=Name the things|--description=Names.|--type=task|--priority=2|--acceptance=Names them|" +
			"--parent=s-1.1|--deps=blocked-by:s-1.1.1" + fmt.Sprintf(meta, "names.go"),
		"dep|add|s-1.2|s-1.1.1|",
		"update|s-1|--append-notes|Assumptions\n\n- Things are sorted by name, A to Z (Sort the things).|",
		"update|s-1|--set-metadata|spec_sha256=",
		"update|s-1|--set-metadata|spec_commit=",
		"ready|--json|--limit|0|--exclude-label|human|--exclude-type|epic|--parent|s-1|\n",
	} {
		i := strings.Index(calls, want)
		if i < 0 {
			t.Errorf("bd wasn't called with %q:\n%s", want, calls)
		}
		at = append(at, i)
	}
	if !slices.IsSorted(at) {
		t.Errorf("bd was called out of order (%v):\n%s", at, calls)
	}
	log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
	for _, want := range []string{"SPEC: breaking " + kept + " down with claude (effort high, time limit 1h)\n",
		"SPEC: filed epic s-1: An app\n", "SPEC: filed epic s-1.1: Listing\n",
		"SPEC: breakdown $1.2500 in 30 turns, ", ", session sess-1\n",
		"SPEC: filing the 4 issues of the breakdown's answer under s-1\n",
		"SPEC: filed 4 issues and 1 later blocks link from the breakdown's answer (",
		"SPEC: broken down into s-1 (2 epics, 3 tickets)\n",
		"SPEC: assumed: Things are sorted by name, A to Z (Sort the things).\n"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log lacks %q:\n%s", want, log)
		}
	}
	if recs := streamRecords(t, repo); recs[0]["scope"] != "s-1" || recs[0]["spec"] != kept {
		t.Errorf("start record: %v", recs[0])
	}
}

// A session that runs out of time, fails, or answers with no root epic ends the run with exit 2,
// before its loop: orchestra lists what was filed for the spec and says that running it again
// carries on.
func TestSpecBreakdownThatDoesNotFinishListsWhatItFiled(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools, per case, and waits out a time limit")
	}
	failure := streamed(t, map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true,
		"errors": []string{"API Error: 529 overloaded"}, "session_id": "sess-1"})
	// The session that times out has filed its tree before it starts, when orchestra asks claude for
	// its --help, so that the time limit, which counts from the session's start, can't stop it
	// halfway through the filing on a loaded machine.
	filedFirst := "[ -f \"$d/filed\" ] && exit 0\nspec=.orchestra/specs/app.md sha= commit=\n" + fileTree(t)
	for _, tc := range []struct {
		name, body string
		onHelp     string // the fake claude's on-help (see fakeSession)
		args       []string
		want       []string
	}{
		{"timed out", "exec sleep 30\n", filedFirst, []string{"--spec-timeout", "1s"}, []string{
			"orchestra couldn't break the spec down: claude timed out after 1s\n",
			"It filed 2 epics and 2 tickets for .orchestra/specs/app.md, under s-1:\n  s-1  epic  An app\n" +
				"  s-1.1  epic  Listing\n  s-1.1.1  task  List the things\n  s-1.2  task  Sort the things\n",
		}},
		{"failed", fileTree(t) + "cat <<'EOS'\n" + failure + "\nEOS\nexit 1\n", "", nil, []string{
			"orchestra couldn't break the spec down: claude failed during the call: API Error: 529 overloaded\n",
			"It filed 2 epics and 2 tickets for .orchestra/specs/app.md, under s-1:\n",
		}},
		{"no root epic", filing(t, "toolu_1", "bd create --type epic --title 'An app' --metadata "+rootMeta+" --silent") +
			answering(t, specAnswer{Problem: "The spec\ndescribes nothing to build."}), "", nil, []string{
			"orchestra: the spec's breakdown named no root epic, so there is nothing to run.\n" +
				"It says: The spec describes nothing to build.\n",
			"It filed 1 epic and 0 tickets for .orchestra/specs/app.md, under s-1:\n  s-1  epic  An app\n",
		}},
		{"nothing filed", answering(t, specAnswer{}), "", nil, []string{
			"orchestra: the spec's breakdown named no root epic, so there is nothing to run.\n" +
				"It filed nothing for .orchestra/specs/app.md.\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, spec, tools := breakdownFixture(t, "", tc.body)
			if tc.onHelp != "" {
				if err := os.WriteFile(filepath.Join(tools, "on-help"), []byte("d='"+tools+"'\n"+tc.onHelp), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, code := runSpecIn(t, repo, func(string) {}, append([]string{"--spec", spec, "--plain"},
				tc.args...)...)
			if code != 2 || strings.Contains(stdout, "START") {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			for _, want := range append(tc.want, "Run it again to carry on: orchestra --spec "+spec+"\n") {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr)
				}
			}
			if recs := streamRecords(t, repo); kindsOf(recs) != "start end" || recs[1]["code"] != 2.0 {
				t.Errorf("records: %v", recs)
			}
		})
	}
}

// specBreakdownIn is a headless breakdown in repo, with its progress in plain lines on out.
func specBreakdownIn(repo string, out *strings.Builder) headlessBreakdown {
	return headlessBreakdown{repo: repo, limit: time.Minute, effort: "max", say: tui.Printer{Out: out},
		tracker: beads.Tracker{Repo: repo}, countEvery: time.Hour}
}

// breakdownRepo is a folder with the .orchestra/run/ a breakdown writes its instructions to.
func breakdownRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, project.Dir, project.RunName), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

// testSpec is a committed spec, as a breakdown is asked about it.
var testSpec = project.Spec{Path: ".orchestra/specs/app.md", SHA256: strings.Repeat("ab", 32), Commit: "c0ffee"}

// The session is claude -p in the main checkout with the breakdown's instructions, its tools Read,
// Glob, Grep, Agent and Bash, no MCP servers (the project chose none), Bash allowed only bd and read-only git, Edit, Write and NotebookEdit
// denied, nothing that would ask allowed (--permission-prompts none only where claude lists it), the
// answer's schema (the tree's, for a spec broken down for the first time), the stream of what it does,
// and the effort; its first message names the spec.
func TestSpecBreakdownCommandLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, help string
		prompts    []string
	}{
		{"with --permission-prompts", "Usage: claude [options]\n" + helpWithPrompts, []string{"--permission-prompts", "none"}},
		{"an older claude", "Usage: claude [options]\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, tools := breakdownRepo(t), t.TempDir()
			faketool.Write(t, tools, "bd", filingBd)
			fakeSession(t, tools, tc.help, answering(t, specAnswer{Root: "s-1"}))
			onPath(t, tools)
			var out strings.Builder
			if _, err := specBreakdownIn(repo, &out).BreakDown(testCtx(context.Background(), t),
				breakdownRequest{Spec: testSpec}); err != nil {
				t.Fatal(err)
			}
			prompt := filepath.Join(repo, project.Dir, project.RunName, "spec-prompt.md")
			want := slices.Concat([]string{"-p", "--append-system-prompt-file", prompt, "--tools", "Read,Glob,Grep,Agent,Bash",
				"--strict-mcp-config", "--allowedTools", "Bash(bd *)", "Bash(git log *)", "Bash(git show *)", "Bash(git diff *)",
				"Bash(git status *)", "Bash(git ls-files *)", "Bash(git ls-tree *)", "Bash(git grep *)",
				"Bash(git blame *)", "Bash(git rev-parse *)", "Bash(git cat-file *)",
				"--disallowedTools", "Edit", "Write", "NotebookEdit", "--permission-mode", "dontAsk"}, tc.prompts,
				[]string{"--output-format", "stream-json", "--verbose", "--json-schema", specTreeSchema, "--effort", "max"})
			if got := strings.Split(strings.TrimSuffix(read(t, filepath.Join(tools, "claude-args")), "\n"), "\n"); !reflect.DeepEqual(got, want) {
				t.Errorf("claude ran with\n%q\nwant\n%q", got, want)
			}
			if b := read(t, prompt); !strings.HasPrefix(b, "<!--") || !strings.Contains(b, "# Spec breakdown") {
				t.Errorf("the instructions:\n%s", b)
			}
			wantIn := "Break down the spec at .orchestra/specs/app.md into Beads tickets.\n\n- path: .orchestra/specs/app.md\n" +
				"- sha256: " + testSpec.SHA256 + "\n- commit: c0ffee\n- time limit: 1m, after which orchestra stops this session\n"
			if in := read(t, filepath.Join(tools, "claude-in")); in != wantIn {
				t.Errorf("the first message:\n%s\nwant\n%s", in, wantIn)
			}
		})
	}
}

// The schema asks for the root epic and the assumptions, and lets the session say why it filed no
// root epic.
func TestSpecSchema(t *testing.T) {
	t.Parallel()
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal([]byte(specSchema), &s); err != nil {
		t.Fatal(err)
	}
	if s.Properties["root"].Type != "string" || s.Properties["assumptions"].Type != "array" ||
		s.Properties["problem"].Type != "string" || !reflect.DeepEqual(s.Required, []string{"root", "assumptions"}) {
		t.Errorf("schema %+v", s)
	}
}

// The session's answer is its result's structured output, which stream-json still carries at the end;
// a result that reports an error or a refusal, a session that ends without one, and one whose answer
// can't be read are errors, and so is one stopped by ctx or the time limit. A session that gave no
// answer still names the root epic it filed, as bd has it.
func TestSpecBreakdownAnswers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		want       breakdownResult
		err        string
	}{
		{"answer", answering(t, specAnswer{Root: " s-1 ", Assumptions: []string{" One. ", " "}}),
			breakdownResult{Root: "s-1", Assumptions: []string{"One."}}, ""},
		{"no root epic", answering(t, specAnswer{Problem: " Nothing to build. "}),
			breakdownResult{Problem: "Nothing to build."}, ""},
		{"answer in the result's text", "echo '" + `{"type":"result","subtype":"success","result":"{\"root\":\"s-2\",\"assumptions\":[]}"}` + "'\n",
			breakdownResult{Root: "s-2"}, ""},
		{"refusal", "echo '" + `{"type":"result","subtype":"success","stop_reason":"refusal","result":"I cannot."}` + "'\n",
			breakdownResult{}, "claude refused to answer: I cannot."},
		{"error result", "echo '" + `{"type":"result","subtype":"error_max_turns","is_error":true}` + "'\nexit 1\n",
			breakdownResult{}, "claude reached its turn limit"},
		{"no result", "echo 'not logged in' >&2\nexit 1\n", breakdownResult{}, "claude: exit status 1: not logged in"},
		{"ended without an answer", "", breakdownResult{}, "claude ended without an answer"},
		{"unreadable", "echo '" + `{"type":"result","subtype":"success","result":"Done."}` + "'\n",
			breakdownResult{}, "claude's answer can't be read: invalid character 'D' looking for beginning of value"},
		{"filed, then failed", filing(t, "toolu_1", "bd create --type epic --title 'An app' --metadata "+rootMeta+
			" --silent") + "exit 1\n", breakdownResult{Root: "s-1"}, "claude: exit status 1: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, tools := breakdownRepo(t), t.TempDir()
			faketool.Write(t, tools, "bd", filingBd)
			fakeSession(t, tools, "", tc.body)
			onPath(t, tools)
			var out strings.Builder
			got, err := specBreakdownIn(repo, &out).BreakDown(testCtx(context.Background(), t), breakdownRequest{Spec: testSpec})
			if tc.err == "" && err != nil || tc.err != "" && (err == nil || err.Error() != tc.err) {
				t.Errorf("error %v, want %q", err, tc.err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// A breakdown stopped by its context, Ctrl+C, ends with the context's error, and names the root epic
// it filed before it stopped; its busy line ends as stopped.
func TestSpecBreakdownStopped(t *testing.T) {
	t.Parallel()
	repo, tools := breakdownRepo(t), t.TempDir()
	faketool.Write(t, tools, "bd", filingBd)
	fakeSession(t, tools, "", filing(t, "toolu_1", "bd create --type epic --title 'An app' --metadata "+rootMeta+
		" --silent")+"touch \"$d/filed-root\"\nexec sleep 30\n")
	onPath(t, tools)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if _, err := os.Stat(filepath.Join(tools, "filed-root")); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	var out strings.Builder
	got, err := specBreakdownIn(repo, &out).BreakDown(testCtx(ctx, t), breakdownRequest{Spec: testSpec})
	if !errors.Is(err, context.Canceled) || got.Root != "s-1" {
		t.Errorf("got %+v, %v", got, err)
	}
}

// specStreamOf is a stream reading what a session prints, its filing counted by p.
func specStreamOf(p *specProgress) *specStream {
	return &specStream{pending: map[string][]filedIssue{}, filed: p.filed}
}

// bashUse and bashResult are the stream lines of a Bash call running cmd, and of its result.
func bashUse(id, cmd string) string {
	b, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "Filing."},
		map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}}}}})
	return string(b) + "\n"
}

func bashResult(id string, content any, isError bool) string {
	b, _ := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": content, "is_error": isError}}}})
	return string(b) + "\n"
}

// On a terminal a breakdown's busy line counts the epics and tickets each bd create the session ran
// filed, from the stream, as their results come, and ends saying what was filed and how long it took.
// A call that failed, or whose result hasn't come, counts nothing.
func TestSpecProgressOnATerminal(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		p := startSpecProgress(tui.Terminal(&b, 160), nil, specCountEvery)
		s := specStreamOf(p)
		write := func(text string) {
			// The stream comes in pieces that don't end at its lines.
			for len(text) > 0 {
				n := min(len(text), 37)
				if _, err := s.Write([]byte(text[:n])); err != nil {
					t.Fatal(err)
				}
				text = text[n:]
			}
		}
		write(`{"type":"system","subtype":"init","session_id":"sess-9"}` + "\n")
		write(bashUse("u1", "bd create --type epic --title 'An app' --silent"))
		time.Sleep(1100 * time.Millisecond)
		write(bashResult("u1", "s-1", false))
		write(bashUse("u2", "bd create --parent s-1 -t task --title 'A' --silent && bd create --parent s-1 --title 'B' --silent"))
		write(bashUse("u3", "bd create --parent s-1 --type=epic --title 'Failed' --silent"))
		write(bashResult("u3", "Error: database is locked", true))
		write(bashUse("u4", "bd list --json"))
		write(bashResult("u4", "[]", false))
		time.Sleep(1100 * time.Millisecond)
		write(bashResult("u2", []any{map[string]any{"type": "text", "text": "s-1.1\ns-1.2"}}, false))
		time.Sleep(1100 * time.Millisecond)
		p.done()
		out := ansi.Strip(b.String())
		for _, want := range []string{"Breaking the spec down with Claude… 0:00 (Ctrl+C stops)\r",
			"Breaking the spec down with Claude… 1 epic · 0 tickets · 0:01 (Ctrl+C stops)\r",
			"Breaking the spec down with Claude… 1 epic · 2 tickets · 0:02 (Ctrl+C stops)\r",
			"\r◆ Spec broken down: 1 epic, 2 tickets (3s)\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%q", want, out)
			}
		}
		if strings.Contains(out, "SPEC:") || s.session != "sess-9" {
			t.Errorf("plain lines on a terminal, or session %q:\n%q", s.session, out)
		}
	})
}

// A breakdown that ends without an answer ends its busy line with a warning saying what was filed;
// one stopped says so.
func TestSpecProgressEnds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		p := startSpecProgress(tui.Terminal(&b, 160), nil, specCountEvery)
		p.filed([]filedIssue{{Epic: true, Title: "An app"}, {Title: "A"}}, nil)
		time.Sleep(500 * time.Millisecond)
		p.end(false)
		if out := ansi.Strip(b.String()); !strings.HasSuffix(out, "\r◆ SPEC: not broken down, with 1 epic and 1 ticket filed\n") {
			t.Errorf("printed %q", out)
		}
		b.Reset()
		p = startSpecProgress(tui.Terminal(&b, 160), nil, specCountEvery)
		time.Sleep(1500 * time.Millisecond)
		p.end(true)
		if out := ansi.Strip(b.String()); !strings.HasSuffix(out, "\r◆ Spec breakdown stopped (1s)\n") {
			t.Errorf("printed %q", out)
		}
	})
}

// In plain output a breakdown says that it starts, each epic as it is filed, with its ID when the
// session's bd create printed it alone, and the counts every so often; the log gets each epic.
func TestSpecProgressInPlainOutput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var b strings.Builder
		dir := t.TempDir()
		log, err := dispatch.OpenLog(filepath.Join(dir, "orchestra.log"), false, "app")
		if err != nil {
			t.Fatal(err)
		}
		p := startSpecProgress(tui.Printer{Out: &b}, log, time.Minute)
		s := specStreamOf(p)
		for _, line := range []string{
			bashUse("u1", "bd create --type epic --title 'An app' --silent && bd create --parent s-1 --type epic --title 'Listing\nand sorting' --silent"),
			bashResult("u1", "s-1\ns-1.1\n", false),
			bashUse("u2", "BD_ACTOR=me bd create 'A ticket' --parent s-1.1 --silent"),
			bashResult("u2", "s-1.1.1", false),
		} {
			if _, err := s.Write([]byte(line)); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(61 * time.Second)
		if _, err := s.Write([]byte(bashUse("u3", "bd create --type epic --title 'Sorting' --json") +
			bashResult("u3", `{"id":"s-1.2"}`, false))); err != nil {
			t.Fatal(err)
		}
		time.Sleep(60 * time.Second)
		p.done()
		if err := log.Close(); err != nil {
			t.Fatal(err)
		}
		want := "breaking the spec down with claude… (ctrl+c stops)\n" +
			"SPEC: filed epic s-1: An app\n" +
			"SPEC: filed epic s-1.1: Listing and sorting\n" +
			"SPEC: breaking the spec down: 2 epics and 1 ticket filed so far (1m)\n" +
			"SPEC: filed epic: Sorting\n" +
			"SPEC: breaking the spec down: 3 epics and 1 ticket filed so far (2m)\n"
		if out := unstamped(b.String()); out != want {
			t.Errorf("printed\n%s\nwant\n%s", out, want)
		}
		logged := unstamped(read(t, filepath.Join(dir, "orchestra.log")))
		for _, want := range []string{"SPEC: filed epic s-1: An app\n", "SPEC: filed epic: Sorting\n"} {
			if !strings.Contains(logged, want) {
				t.Errorf("the log lacks %q:\n%s", want, logged)
			}
		}
		if strings.Contains(logged, "so far") {
			t.Errorf("the counts went into the log:\n%s", logged)
		}
	})
}

// bdCreates finds each bd create in a command line, its type and title, wherever the line joins it
// to others; text in quotes and here-documents is no command.
func TestBdCreates(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		line string
		want []filedIssue
	}{
		{"bd create --type epic --title 'An app' --silent", []filedIssue{{Epic: true, Title: "An app"}}},
		{`bd create "A ticket" -t task`, []filedIssue{{Title: "A ticket"}}},
		{`bd create --type=epic --title="An app"`, []filedIssue{{Epic: true, Title: "An app"}}},
		{"bd -q create -t epic --title x", []filedIssue{{Epic: true, Title: "x"}}},
		{"BD_ACTOR=me bd new --title y", []filedIssue{{Title: "y"}}},
		{"bd create --title a && bd dep add s-2 s-1 --type blocks; bd create --title b | cat\nbd create --title c",
			[]filedIssue{{Title: "a"}, {Title: "b"}, {Title: "c"}}},
		{"bd create --type epic --title 'Root' --description \"$(cat <<'EOF'\nbd create --title inside\nRun (it) \"quoted\"\nEOF\n)\" --silent && bd create --title after",
			[]filedIssue{{Epic: true, Title: "Root"}, {Title: "after"}}},
		{"bd create --title 'x' --body-file - <<EOF\nbd create --title body\nEOF\nbd create --title next",
			[]filedIssue{{Title: "x"}, {Title: "next"}}},
		{`bd create --title "say \"bd create\" here" --notes 'bd create'`, []filedIssue{{Title: `say "bd create" here`}}},
		{`echo "bd create --title no"; bd list # bd create --title comment`, nil},
		{"bd update s-1 --title x", nil},
		{"git log", nil},
	} {
		if got := bdCreates(tc.line); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("bdCreates(%q) = %+v, want %+v", tc.line, got, tc.want)
		}
	}
}

// specTree is a specTracker over a tree in memory: show answers from issues, as do Descendants (by
// parent) and WithMetadata (by the spec's path); Cycles answers cycles; SetMetadata keeps what it set.
type specTree struct {
	issues []dispatch.Ticket
	cycles [][]string
	set    []string
	fail   string // a method that fails
}

func (s *specTree) Show(_ context.Context, id string) (dispatch.Ticket, error) {
	for _, t := range s.issues {
		if t.ID == id && s.fail != "Show" {
			return t, nil
		}
	}
	return dispatch.Ticket{}, errors.New("no issue found matching " + id)
}

func (s *specTree) Descendants(_ context.Context, id string) ([]dispatch.Ticket, error) {
	if s.fail == "Descendants" {
		return nil, errors.New("bd list: database is locked")
	}
	var under []dispatch.Ticket
	for queue := []string{id}; len(queue) > 0; queue = queue[1:] {
		for _, t := range s.issues {
			if t.Parent == queue[0] {
				under = append(under, t)
				queue = append(queue, t.ID)
			}
		}
	}
	return under, nil
}

func (s *specTree) WithMetadata(_ context.Context, key, value string) ([]dispatch.Ticket, error) {
	var with []dispatch.Ticket
	for _, t := range s.issues {
		if t.MetadataText(key) == value {
			with = append(with, t)
		}
	}
	return with, nil
}

func (s *specTree) Cycles(context.Context) ([][]string, error) {
	if s.fail == "Cycles" {
		return nil, errors.New("bd dep cycles: no database")
	}
	return s.cycles, nil
}

func (s *specTree) SetMetadata(_ context.Context, id, key, value string) error {
	if s.fail == "SetMetadata" {
		return errors.New("bd update: database is locked")
	}
	s.set = append(s.set, id+" "+key+"="+value)
	return nil
}

// issue is a ticket of a spec tree: its ID, type and parent, and its metadata and acceptance criteria.
func issue(id, kind, parent, metadata, acceptance string) dispatch.Ticket {
	return dispatch.Ticket{ID: id, IssueType: kind, Parent: parent, Status: dispatch.StatusOpen,
		Metadata: json.RawMessage(metadata), AcceptanceCriteria: acceptance}
}

// After a breakdown, orchestra checks the tree it filed: the root epic gets the spec's metadata it
// lacks; issues carrying the spec's metadata outside it, issues under it without that metadata, open
// tickets without files metadata or acceptance criteria, and blocks cycles among the spec's issues are
// warnings, and so is what can't be checked.
func TestCheckBreakdownWarns(t *testing.T) {
	t.Parallel()
	spec := `"spec":".orchestra/specs/app.md"`
	closed := issue("s-1.6", "task", "s-1", "{"+spec+"}", "")
	closed.Status = dispatch.StatusClosed
	tree := &specTree{issues: []dispatch.Ticket{
		issue("s-1", "epic", "", `{`+spec+`,"spec_sha256":"old"}`, ""),
		issue("s-1.1", "epic", "s-1", "{"+spec+"}", ""),
		issue("s-1.1.1", "task", "s-1.1", `{`+spec+`,"files":["a.go"]}`, "Does a."),
		issue("s-1.2", "task", "s-1", "{"+spec+"}", "Does b."),
		issue("s-1.3", "feature", "s-1", `{`+spec+`,"files":"c.go"}`, " "),
		issue("s-1.4", "task", "s-1", `{"files":["d.go"]}`, "Does d."),
		closed,
		issue("s-9", "task", "", "{"+spec+"}", "Strays."),
	}, cycles: [][]string{{"s-1.1.1", "s-1.2"}, {"x-1", "x-2"}}}
	warnings, err := checkBreakdown(context.Background(), tree, testSpec, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"the breakdown left s-1's spec_sha256, spec_commit metadata out or wrong; orchestra set it",
		"issues carrying the spec's metadata outside s-1, which the run leaves out (1): s-9",
		"issues under s-1 without the spec's metadata, whose workers aren't pointed to the spec (1): s-1.4",
		"tickets under s-1 without files metadata, whose files orchestra guesses (1): s-1.2",
		"tickets under s-1 without acceptance criteria (1): s-1.3",
		"a blocks cycle: s-1.1.1 → s-1.2 → s-1.1.1; none of them can start until it is broken (bd dep remove)",
	}
	if !reflect.DeepEqual(warnings, want) {
		t.Errorf("warnings\n%q\nwant\n%q", warnings, want)
	}
	if wantSet := []string{"s-1 spec_sha256=" + testSpec.SHA256, "s-1 spec_commit=c0ffee"}; !reflect.DeepEqual(tree.set, wantSet) {
		t.Errorf("set %q, want %q", tree.set, wantSet)
	}

	tree.fail = "Cycles"
	if warnings, err := checkBreakdown(context.Background(), tree, testSpec, "s-1"); err != nil ||
		warnings[len(warnings)-1] != "can't look for blocks cycles: bd dep cycles: no database" {
		t.Errorf("cycles unread: %q, %v", warnings, err)
	}
	tree.fail = "SetMetadata"
	if warnings, err := checkBreakdown(context.Background(), tree, testSpec, "s-1"); err != nil ||
		warnings[0] != "can't set s-1's spec_sha256 metadata: bd update: database is locked" {
		t.Errorf("metadata unset: %q, %v", warnings, err)
	}
	tree.fail = "Descendants"
	if warnings, err := checkBreakdown(context.Background(), tree, testSpec, "s-1"); err != nil ||
		warnings[len(warnings)-1] != "can't list the issues under s-1: bd list: database is locked" {
		t.Errorf("tree unread: %q, %v", warnings, err)
	}

	ids := make([]string, 12)
	for i := range ids {
		ids[i] = fmt.Sprintf("s-1.%d", i+1)
	}
	if got := idList(ids); got != "s-1.1, s-1.2, s-1.3, s-1.4, s-1.5, s-1.6, s-1.7, s-1.8, s-1.9, s-1.10 and 2 more" {
		t.Errorf("idList: %q", got)
	}
}

// A root epic that can't be read, isn't an epic, or is another spec's is no root epic to run.
func TestCheckBreakdownRefusesARootThatIsNotTheSpecs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		root dispatch.Ticket
		err  string
	}{
		{issue("s-2", "epic", "", "{}", ""), "can't read s-1, the root epic it named: no issue found matching s-1"},
		{issue("s-1", "task", "", "{}", ""), "s-1, which it named as the root epic, is a task, not an epic"},
		{issue("s-1", "epic", "", `{"spec":"docs/other.md"}`, ""),
			"s-1, which it named as the root epic, is the epic of another spec, docs/other.md"},
	} {
		tree := &specTree{issues: []dispatch.Ticket{tc.root}}
		if _, err := checkBreakdown(context.Background(), tree, testSpec, "s-1"); err == nil || err.Error() != tc.err {
			t.Errorf("error %v, want %q", err, tc.err)
		}
		if len(tree.set) > 0 {
			t.Errorf("set %q on a root it refused", tree.set)
		}
	}
}

// A spec run whose breakdown names a root epic that isn't the spec's ends with exit 2, listing what
// was filed, and logs the check's warnings.
func TestSpecRunRefusesARootThatIsNotAnEpic(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, _ := breakdownFixture(t, "", filing(t, "toolu_1", "bd create --type task --title 'An app' --metadata "+
		specMeta+" --silent")+answering(t, specAnswer{Root: "s-1"}))
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 2 || strings.Contains(stdout, "START") {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"orchestra won't run what the spec's breakdown filed: s-1, which it named as the root epic, is a task, not an epic\n",
		"It filed 0 epics and 1 ticket for .orchestra/specs/app.md:\n  s-1  task  An app\n",
		"Run it again to carry on: orchestra --spec " + spec + "\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	if log := read(t, filepath.Join(repo, project.Dir, "orchestra.log")); !strings.Contains(log,
		"SPEC: not run: s-1, which it named as the root epic, is a task, not an epic\n") {
		t.Errorf("the log:\n%s", log)
	}
}

// The checks' warnings are said on stderr and logged, and the run goes on. The root epic's SHA-256
// and commit, which orchestra sets after the headless breakdown's answer, are none of them.
func TestSpecRunWarnsOfWhatIsAmissInTheTree(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, tools := breakdownFixture(t, "", filing(t, "toolu_1", "bd create --type epic --title 'An app' "+
		"--metadata "+specMeta+" --silent && bd create --parent s-1 --type task --title 'A' --metadata "+specMeta+
		" --silent")+answering(t, specAnswer{Root: "s-1"}))
	if err := os.WriteFile(filepath.Join(tools, "cycles"), []byte(`[{"members":[{"id":"s-1.1"},{"id":"s-1"}]}]`),
		0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	log := read(t, filepath.Join(repo, project.Dir, "orchestra.log"))
	if strings.Contains(stderr, "metadata out or wrong") {
		t.Errorf("a warning that the root epic's metadata was left out:\n%s", stderr)
	}
	for _, want := range []string{
		"tickets under s-1 without files metadata, whose files orchestra guesses (1): s-1.1\n",
		"tickets under s-1 without acceptance criteria (1): s-1.1\n",
		"a blocks cycle: s-1.1 → s-1 → s-1.1; none of them can start until it is broken (bd dep remove)\n",
	} {
		if !strings.Contains(stderr, "warning: "+want) || !strings.Contains(log, "SPEC: warning: "+want) {
			t.Errorf("stderr or the log lacks %q:\n%s\n%s", want, stderr, log)
		}
	}
	if calls := read(t, filepath.Join(tools, "bd-calls")); !strings.Contains(calls, "update|s-1|--set-metadata|spec_sha256=") {
		t.Errorf("the root epic's SHA-256 wasn't set:\n%s", calls)
	}
}

// TestLiveSpec breaks a tiny spec down with the real claude, in a new repository with Beads, and
// checks the tree it filed. It calls claude, which takes minutes and costs money: run it with
//
//	SPEC_LIVE=1 go test -run TestLiveSpec -v -timeout 30m ./cmd/orchestra/
func TestLiveSpec(t *testing.T) {
	t.Parallel()
	if os.Getenv("SPEC_LIVE") != "1" {
		t.Skip("set SPEC_LIVE=1 to call the real claude")
	}
	ctx := context.Background()
	repo := t.TempDir()
	for _, args := range [][]string{{"git", "init", "-q"}, {"bd", "init", "--prefix", "live", "--quiet"}} {
		if out, err := command.Output(ctx, 2*time.Minute, repo, args[0], args[1:]...); err != nil {
			t.Fatalf("%s: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, project.Dir, project.RunName), 0o755); err != nil {
		t.Fatal(err)
	}
	writeSpec(t, filepath.Join(repo, "spec.md"), "# Greeter\n\nA command-line program, greet, in Go, that prints "+
		"\"Hello, <name>!\" for the name given as its one argument, and \"Hello, world!\" without one.\n\n"+
		"## Tests\n\nUnit tests for both cases.\n")
	spec, _, err := project.CommitSpec(ctx, repo, filepath.Join(repo, "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	h := headlessBreakdown{repo: repo, limit: 20 * time.Minute, effort: "medium", say: tui.Printer{Out: &out},
		tracker: beads.Tracker{Repo: repo}, countEvery: time.Minute}
	start := time.Now()
	res, err := h.BreakDown(ctx, breakdownRequest{Spec: spec})
	t.Logf("progress:\n%s", out.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("broken down in %s into %s; assumed: %q", time.Since(start).Round(time.Second), res.Root, res.Assumptions)
	warnings, err := checkBreakdown(ctx, beads.Tracker{Repo: repo}, spec, res.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range warnings {
		t.Logf("warning: %s", w)
	}
	under, err := beads.Tracker{Repo: repo}.Descendants(ctx, res.Root)
	if err != nil || len(under) == 0 {
		t.Errorf("nothing under %s: %v", res.Root, err)
	}
	for _, i := range under {
		t.Logf("%s %s %s files %v", i.ID, i.IssueType, i.Title, dispatch.TicketFiles(i))
	}
}
