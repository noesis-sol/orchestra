package dispatch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// denyingHelp is Claude Code 2.1.303's --help around --disallowedTools, which it lists by two names.
const denyingHelp = `Usage: claude [options] [command] [prompt]

Options:
  --disable-slash-commands              Disable all skills
  --disallowedTools, --disallowed-tools <tools...>
      Comma or space-separated list of tool names to deny (e.g. "Bash(git *)
      Edit")
  --effort <level>                      Effort level for the current session
  --no-chrome                           Disable Claude in Chrome integration
`

const (
	deniedWithSkill    = "--disallowedTools=Artifact,ListAgents,ReportFindings,ScheduleWakeup,SendFeedback,Workflow,Skill"
	deniedWithoutSkill = "--disallowedTools=Artifact,ListAgents,ReportFindings,ScheduleWakeup,SendFeedback,Workflow"
)

// denied is the --disallowedTools among args, "" when there is none.
func denied(args []string) string {
	for _, a := range args {
		if strings.HasPrefix(a, "--disallowedTools") {
			return a
		}
	}
	return ""
}

// Claude workers start without the tools they never use, new or resuming a session, when their claude
// lists --disallowedTools: Skill too, unless the project has skills of its own. An older claude, one
// whose help can't be read, or workers of another kind get no such flag, which they could refuse.
func TestWorkersStartWithoutUnusedTools(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, help, skillsIn, want string
	}{
		{"no skills of its own", "claude", denyingHelp, "", deniedWithSkill},
		{"skills of its own", "claude", denyingHelp, ".claude/skills/its-own", deniedWithoutSkill},
		{"commands of its own", "claude", denyingHelp, ".claude/commands/its-own", deniedWithoutSkill},
		{"an empty skills folder", "claude", denyingHelp, ".claude/skills", deniedWithSkill},
		{"an older claude", "claude", claudeHelp, "", ""},
		{"help unread", "claude", "", "", ""},
		{"codex workers", "codex", denyingHelp, "", ""},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.help, h.cfg.AgentKind = &fakeHelp{help: tc.help}, tc.kind
			h.cfg.WorkerArgs = []string{"--no-chrome"}
			if tc.skillsIn != "" {
				if err := os.MkdirAll(filepath.Join(h.cfg.Repo, tc.skillsIn), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			h.beads.add("A", "first", 1)
			starting := 1
			if tc.kind == "claude" { // and once more, resuming its session
				withSession(h, "A")
				h.worker("A", vanishes, finishes("a.txt"))
				starting = 2
			} else {
				h.worker("A", finishes("a.txt"))
			}
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("%s: exit %d, final %q\n%s", tc.name, code, o.Final(), h.sink.text())
			}
			starts := h.herdr.argsFor("A")
			if len(starts) != starting {
				t.Fatalf("%s: A's starts %q; want %d", tc.name, starts, starting)
			}
			for _, args := range starts {
				if got := denied(args); got != tc.want {
					t.Errorf("%s: a worker started with %q, want %q", tc.name, args, tc.want)
				}
			}
			said := strings.Contains(h.sink.text(), "workers start without the tools they never use: "+
				"Artifact, ListAgents, ReportFindings, ScheduleWakeup, SendFeedback, Workflow (")
			if said != (tc.want != "") {
				t.Errorf("%s: the run said it %v, want %v:\n%s", tc.name, said, tc.want != "", h.sink.text())
			}
		})
	}
}

// The probe starts as a worker does, without the tools workers never use, in their permission mode.
func TestTheProbeStartsWithoutUnusedTools(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		help := &fakeHelp{help: denyingHelp + claudeHelp[strings.Index(claudeHelp, "  --permission-mode"):]}
		h.help, h.cfg.WorkerPermissionMode = help, "auto"
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp, finishes("b.txt"))
		h.worker(probeID, runsProbe)
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		want := []string{"start", "--strict-mcp-config", "--permission-mode", "auto", deniedWithSkill}
		if got := h.herdr.argsFor(probeID); len(got) != 1 || !equal(got[0], want) {
			t.Errorf("probe started with %q, want once with %q", got, want)
		}
		if help.asked != 1 {
			t.Errorf("claude asked %d times, want once", help.asked)
		}
		for _, id := range []string{"A", "B"} {
			for _, args := range h.herdr.argsFor(id) {
				if !slices.Contains(args, deniedWithSkill) {
					t.Errorf("%s's worker started with %q, want %q", id, args, deniedWithSkill)
				}
			}
		}
	})
}
