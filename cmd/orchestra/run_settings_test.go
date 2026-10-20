package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// settle reads args and env as a run's flags and resolves the project's settings.json (settings,
// "" for none) under them, as loadConfig does but without a repository, a terminal or a checkout.
func settle(t *testing.T, args []string, env map[string]string, settings string) (options, project.Settings, []string) {
	t.Helper()
	f, problems, err := readFlags(args, func(k string) string { return env[k] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	var s project.Settings
	if settings != "" {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, project.Dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(project.SettingsPath(repo), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if s, _, err = project.LoadSettings(repo); err != nil {
			t.Fatal(err)
		}
	}
	c := f.options
	return c, s, append(problems, resolveSettings(&c, f, s)...)
}

// Every run setting is taken from its flag over its environment variable, over the project's
// settings.json where it has one, over its default.
func TestPrecedence(t *testing.T) {
	t.Parallel()
	terminal := func(c options, s project.Settings) any {
		got, err := project.ResolveTerminal(c.terminalAsked, s, terminalChoices())
		if err != nil {
			return err
		}
		return got
	}
	for _, o := range []struct {
		name                            string
		flag                            []string // the flag giving flagWant
		env, val                        string   // the variable and its value giving envWant; "" for none
		settings                        string   // settings.json giving setting; "" for none
		get                             func(options, project.Settings) any
		def, setting, envWant, flagWant any
	}{
		{"--concurrent", []string{"--concurrent", "5"}, "ORCHESTRA_CONCURRENT", "3", `{"concurrent": 2}`,
			func(c options, _ project.Settings) any { return c.Concurrency }, 1, 2, 3, 5},
		{"-c", []string{"-c", "4"}, "ORCHESTRA_CONCURRENT", "3", `{"concurrent": 2}`,
			func(c options, _ project.Settings) any { return c.Concurrency }, 1, 2, 3, 4},
		{"--ticket-limit", []string{"--ticket-limit", "0"}, "TICKET_LIMIT", "90m", `{"ticket_limit": "2h"}`,
			func(c options, _ project.Settings) any { return c.TicketLimit },
			time.Duration(0), 2 * time.Hour, 90 * time.Minute, time.Duration(0)},
		{"--check-timeout", []string{"--check-timeout", "45m"}, "ORCHESTRA_CHECK_TIMEOUT", "10m",
			`{"check_fast_timeout": "5m"}`, func(c options, _ project.Settings) any { return c.CheckTimeout },
			30 * time.Minute, 5 * time.Minute, 10 * time.Minute, 45 * time.Minute},
		{"--check-timeout over the old setting", []string{"--check-timeout", "45m"}, "ORCHESTRA_CHECK_TIMEOUT", "10m",
			`{"check_timeout": "5m"}`, func(c options, _ project.Settings) any { return c.CheckTimeout },
			30 * time.Minute, 5 * time.Minute, 10 * time.Minute, 45 * time.Minute},
		{"--check-full-timeout", []string{"--check-full-timeout", "45m"}, "ORCHESTRA_CHECK_FULL_TIMEOUT", "3h",
			`{"check_full_timeout": "90m"}`, func(c options, _ project.Settings) any { return c.CheckFullTimeout },
			60 * time.Minute, 90 * time.Minute, 3 * time.Hour, 45 * time.Minute},
		{"--resolve-conflicts", []string{"--resolve-conflicts"}, "", "", `{"resolve_conflicts": false}`,
			func(c options, _ project.Settings) any { return c.ResolveConflicts }, true, false, nil, true},
		{"--resolve-conflicts=false", []string{"--resolve-conflicts=false"}, "", "", `{"resolve_conflicts": true}`,
			func(c options, _ project.Settings) any { return c.ResolveConflicts }, true, true, nil, false},
		{"--worker-effort", []string{"--worker-effort", "xhigh"}, "WORKER_EFFORT", "max", `{"worker_effort": "medium"}`,
			func(c options, _ project.Settings) any { return c.WorkerEffort }, "high", "medium", "max", "xhigh"},
		{"--worker-permission-mode", []string{"--worker-permission-mode", "acceptEdits"}, "WORKER_PERMISSION_MODE",
			"default", `{"worker_permission_mode": "auto"}`,
			func(c options, _ project.Settings) any { return c.WorkerPermissionMode }, "", "auto", "default", "acceptEdits"},
		{"--organ-effort", []string{"--organ-effort", "low"}, "ORGAN_EFFORT", "max", `{"organ_effort": "high"}`,
			func(c options, _ project.Settings) any { return c.OrganEffort }, "", "high", "max", "low"},
		{"--terminal", []string{"--terminal", "tmux"}, "ORCHESTRA_TERMINAL", "herdr", `{"terminal": "cmux"}`,
			terminal, project.TerminalAuto, project.TerminalCmux, project.TerminalHerdr, project.TerminalTmux},
		{"--limit", []string{"--limit", "5"}, "LIMIT", "7", "",
			func(c options, _ project.Settings) any { return c.Limit }, dispatch.NoLimit, nil, 7, 5},
		{"--done-so-far", []string{"--done-so-far", "1"}, "DONE_SO_FAR", "2", "",
			func(c options, _ project.Settings) any { return c.DoneSoFar }, 0, nil, 2, 1},
		{"--agent", []string{"--agent", "codex"}, "AGENT_KIND", "pi", "",
			func(c options, _ project.Settings) any { return c.AgentKind }, "claude", nil, "pi", "codex"},
		{"--prompt", []string{"--prompt", "mine.md"}, "WORKER_PROMPT", "env.md", "",
			func(c options, _ project.Settings) any { return c.WorkerPrompt }, "", nil, "env.md", "mine.md"},
		{"--worktrees", []string{"--worktrees", "../wt2"}, "WT_ROOT", "../wt", "",
			func(c options, _ project.Settings) any { return c.WTRoot }, "", nil, "../wt", "../wt2"},
		{"--organ-model", []string{"--organ-model", "opus"}, "ORGAN_MODEL", "haiku", "",
			func(c options, _ project.Settings) any { return c.OrganModel }, "", nil, "haiku", "opus"},
		{"--ticket", []string{"--ticket", "x-2"}, "ORCHESTRA_TICKET", "x-1", "",
			func(c options, _ project.Settings) any { return c.Ticket }, "", nil, "x-1", "x-2"},
		{"--notify", []string{"--notify"}, "NOTIFY", "off", "",
			func(c options, _ project.Settings) any { return c.Notify }, true, nil, false, true},
		{"--triage", []string{"--triage=true"}, "TRIAGE", "false", "",
			func(c options, _ project.Settings) any { return c.Triage }, true, nil, false, true},
		{"--review", []string{"--review=1"}, "REVIEW", "no", "",
			func(c options, _ project.Settings) any { return c.Review }, true, nil, false, true},
		{"--tickets", []string{"--tickets=false"}, "ORCHESTRA_TICKETS", "true", "",
			func(c options, _ project.Settings) any { return c.Tickets }, false, nil, true, false},
		{"--prompt-at-launch", []string{"--prompt-at-launch"}, "PROMPT_AT_LAUNCH", "FALSE", "",
			func(c options, _ project.Settings) any { return c.LaunchPrompt }, true, nil, false, true},
	} {
		env := map[string]string{}
		check := func(layer string, args []string, settings string, want any) {
			t.Helper()
			c, s, problems := settle(t, args, env, settings)
			if got := o.get(c, s); got != want || len(problems) > 0 {
				t.Errorf("%s, %s: %v, want %v (problems %v)", o.name, layer, got, want, problems)
			}
		}
		check("by default", nil, "", o.def)
		if o.settings != "" {
			check("settings.json", nil, o.settings, o.setting)
		}
		if o.env != "" {
			env[o.env] = o.val
			check(o.env+" over settings.json", nil, o.settings, o.envWant)
		}
		check("the flag over all", o.flag, o.settings, o.flagWant)
	}
}

// A run setting's problems, each worded as before: a variable's only where no flag overrides it,
// in the order the flags are bound, and a flag held to its variable's rule.
func TestRunSettingProblems(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		args     []string
		env      map[string]string
		settings string
		want     []string
	}{
		{"variables", nil, map[string]string{"LIMIT": "many", "ORCHESTRA_CONCURRENT": "x", "TICKET_LIMIT": "soon",
			"ORCHESTRA_CHECK_TIMEOUT": "0", "NOTIFY": "loud", "PROMPT_AT_LAUNCH": "maybe", noUpdateCheck: "sometimes"}, "",
			[]string{
				"LIMIT must be a whole number (got 'many').",
				"ORCHESTRA_CONCURRENT must be a whole number (got 'x').",
				"TICKET_LIMIT must be a duration such as 2h, or 0 for none (got 'soon').",
				"ORCHESTRA_CHECK_TIMEOUT must be a positive duration such as 5m (got '0').",
				"NOTIFY must be 1 or 0, true or false, yes or no, or on or off (got 'loud').",
				"PROMPT_AT_LAUNCH must be 1 or 0, true or false, yes or no, or on or off (got 'maybe').",
				noUpdateCheck + " must be 1 or 0, true or false, yes or no, or on or off (got 'sometimes').",
			}},
		{"flags over invalid variables", []string{"--limit", "5", "--done-so-far", "1", "-c", "2", "--ticket-limit", "3h",
			"--check-timeout", "2m", "--check-full-timeout", "2m", "--triage=true"},
			map[string]string{"LIMIT": "abc", "DONE_SO_FAR": "-2", "ORCHESTRA_CONCURRENT": "x", "TICKET_LIMIT": "soon",
				"ORCHESTRA_CHECK_TIMEOUT": "0", "ORCHESTRA_CHECK_FULL_TIMEOUT": "never", "TRIAGE": "nope"}, "", nil},
		{"the flags held to the variables' rules", []string{"--limit", "-1", "--done-so-far", "-3", "-c", "-1"}, nil, "",
			[]string{
				"--limit must be a whole number (got -1).",
				"--done-so-far must be a whole number (got -3).",
				"--concurrent must be between 1 and 16 (got -1).",
			}},
		{"-c 20", []string{"-c", "20"}, nil, "",
			[]string{"--concurrent must be between 1 and 16 (got 20)."}},
		{"--concurrent 0 is out of range, not unset", []string{"--concurrent", "0"}, nil, `{"concurrent": 3}`,
			[]string{"--concurrent must be between 1 and 16 (got 0)."}},
		{"-c 0", []string{"-c", "0"}, nil, `{"concurrent": 3}`,
			[]string{"--concurrent must be between 1 and 16 (got 0)."}},
		{"ORCHESTRA_CONCURRENT=0", nil, map[string]string{"ORCHESTRA_CONCURRENT": "0"}, `{"concurrent": 3}`,
			[]string{"ORCHESTRA_CONCURRENT must be between 1 and 16 (got 0)."}},
		{"the flag over a variable of 0", []string{"-c", "2"}, map[string]string{"ORCHESTRA_CONCURRENT": "0"}, "", nil},
		{"concurrent in settings.json", nil, nil, `{"concurrent": 17}`,
			[]string{".orchestra/settings.json: concurrent must be between 1 and 16 (got 17)."}},
		{"--ticket-limit -1h", []string{"--ticket-limit", "-1h"}, nil, "",
			[]string{"--ticket-limit must not be negative (got -1h0m0s)."}},
		{"ticket_limit", nil, nil, `{"ticket_limit": "2 hours"}`,
			[]string{".orchestra/settings.json: ticket_limit must be a duration such as 2h, or 0 for none (got '2 hours')."}},
		{"--check-timeout 0", []string{"--check-timeout", "0"}, nil, "",
			[]string{"--check-timeout must be a positive duration such as 5m (got 0s)."}},
		{"check_timeout", nil, nil, `{"check_timeout": "5 minutes"}`,
			[]string{".orchestra/settings.json: check_timeout must be a positive duration such as 5m (got '5 minutes')."}},
		{"check_fast_timeout", nil, nil, `{"check_fast_timeout": "0"}`,
			[]string{".orchestra/settings.json: check_fast_timeout must be a positive duration such as 5m (got '0')."}},
		{"--check-full-timeout 0", []string{"--check-full-timeout", "0"}, nil, "",
			[]string{"--check-full-timeout must be a positive duration such as 5m (got 0s)."}},
		{"ORCHESTRA_CHECK_FULL_TIMEOUT", nil, map[string]string{"ORCHESTRA_CHECK_FULL_TIMEOUT": "never"}, "",
			[]string{"ORCHESTRA_CHECK_FULL_TIMEOUT must be a positive duration such as 5m (got 'never')."}},
		{"resolve_timeout", nil, nil, `{"resolve_timeout": "soon"}`,
			[]string{".orchestra/settings.json: resolve_timeout must be a positive duration such as 20m (got 'soon')."}},
		{"--worker-effort extreme", []string{"--worker-effort", "extreme"}, nil, "",
			[]string{"--worker-effort must be one of low, medium, high, xhigh, max (got 'extreme')."}},
		{"--worker-permission-mode plan", []string{"--worker-permission-mode", "plan"}, nil, "",
			[]string{"--worker-permission-mode can't be plan: a worker in it only plans, " +
				"and never changes a file. Use one of default, manual, acceptEdits, auto."}},
		{"WORKER_PERMISSION_MODE", nil, map[string]string{"WORKER_PERMISSION_MODE": "bypassPermissions"}, "",
			[]string{"WORKER_PERMISSION_MODE can't be bypassPermissions: a worker in it " +
				"would run every command unchecked, where auto has Claude Code's classifier check them. " +
				"Use one of default, manual, acceptEdits, auto."}},
		{"worker_permission_mode", nil, nil, `{"worker_permission_mode": "Auto"}`,
			[]string{".orchestra/settings.json: worker_permission_mode must be one of default, manual, acceptEdits, " +
				"auto (got 'Auto')."}},
		{"organ_effort", nil, nil, `{"organ_effort": "hi"}`,
			[]string{".orchestra/settings.json: organ_effort must be one of low, medium, high, xhigh, max (got 'hi')."}},
		{"exclude_types", nil, nil, `{"exclude_types": ["epic,decision"]}`,
			[]string{`.orchestra/settings.json: exclude_types must be a list of issue types such as ["epic"] ` +
				`(got "epic,decision").`}},
	} {
		if _, _, got := settle(t, tc.args, tc.env, tc.settings); !slices.Equal(got, tc.want) {
			t.Errorf("%s: problems\n%q\nwant\n%q", tc.name, got, tc.want)
		}
	}
}

// What the project's settings.json gives a run where no flag does: its checks, under their old
// names too, how conflicts are resolved, the workers' effort as an argument, the types left out.
func TestRunSettingsFromTheProject(t *testing.T) {
	t.Parallel()
	c, _, p := settle(t, nil, nil, `{"check": "make check", "check_timeout": "5m"}`)
	if len(p) > 0 || c.Check != "make check" || c.CheckTimeout != 5*time.Minute || c.CheckFull != "" ||
		c.CheckFullTimeout != project.DefaultCheckFullTimeout {
		t.Errorf("old: %q %s %q %s %v", c.Check, c.CheckTimeout, c.CheckFull, c.CheckFullTimeout, p)
	}
	c, _, p = settle(t, nil, nil, `{"check_fast": "scripts/check-fast.sh", "check_full": "scripts/check-full.sh", `+
		`"check_fast_timeout": "7m", "check_full_timeout": "2h"}`)
	if len(p) > 0 || c.Check != project.FastRunner || c.CheckTimeout != 7*time.Minute ||
		c.CheckFull != project.FullRunner || c.CheckFullTimeout != 2*time.Hour {
		t.Errorf("new: %q %s %q %s %v", c.Check, c.CheckTimeout, c.CheckFull, c.CheckFullTimeout, p)
	}

	if c, _, p := settle(t, nil, nil, `{}`); len(p) > 0 || !c.ResolveConflicts || c.ResolveTimeout != 20*time.Minute {
		t.Errorf("resolving by default: on %v, limit %s, %v", c.ResolveConflicts, c.ResolveTimeout, p)
	}
	c, _, p = settle(t, nil, nil, `{"resolve_conflicts": false, "resolve_timeout": "5m"}`)
	if len(p) > 0 || c.ResolveConflicts || c.ResolveTimeout != 5*time.Minute {
		t.Errorf("resolving in settings: on %v, limit %s, %v", c.ResolveConflicts, c.ResolveTimeout, p)
	}

	// Claude workers start at the effort given, else at high.
	for _, tc := range []struct {
		args     []string
		env      map[string]string
		settings string
		want     string
	}{
		{nil, nil, `{"mcp_servers": []}`, "--no-chrome --effort high"},
		{nil, nil, `{"mcp_servers": [], "worker_effort": "medium"}`, "--no-chrome --effort medium"},
		{[]string{"--worker-effort", "xhigh"}, nil, `{"mcp_servers": [], "worker_effort": "medium"}`,
			"--no-chrome --effort xhigh"},
		{nil, map[string]string{"WORKER_EFFORT": "max"}, `{"mcp_servers": []}`, "--no-chrome --effort max"},
		{[]string{"--worker-effort", "extreme"}, nil, `{"mcp_servers": []}`, "--no-chrome"},
	} {
		if c, _, _ := settle(t, tc.args, tc.env, tc.settings); strings.Join(c.WorkerArgs, " ") != tc.want {
			t.Errorf("%v %v %s: worker arguments %q, want %q", tc.args, tc.env, tc.settings, c.WorkerArgs, tc.want)
		}
	}

	for settings, want := range map[string]string{`{}`: "epic", `{"exclude_types": ["epic", "decision"]}`: "epic decision",
		`{"exclude_types": []}`: ""} {
		if c, _, p := settle(t, nil, nil, settings); len(p) > 0 || strings.Join(c.ExcludeTypes, " ") != want {
			t.Errorf("%s: types left out %v, want %q (%v)", settings, c.ExcludeTypes, want, p)
		}
	}
}

// readEnv gives the default for an unset variable, and for one parse doesn't take, with the problem.
func TestReadEnv(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value string
		want        int
		given       bool
		problem     string
	}{
		{"LIMIT", "", 40, false, ""},
		{"LIMIT", "7", 7, true, ""},
		{"LIMIT", "0", 0, true, ""},
		{"LIMIT", "-1", 40, false, "LIMIT must be a whole number (got '-1')."},
		{"LIMIT", "many", 40, false, "LIMIT must be a whole number (got 'many')."},
		{"", "7", 40, false, ""}, // no variable
	} {
		got, given, problem := readEnv(func(string) string { return tc.value }, tc.name, 40, parseWhole)
		if got != tc.want || given != tc.given || problem != tc.problem {
			t.Errorf("%s=%q: %d, %v, %q", tc.name, tc.value, got, given, problem)
		}
	}
}
