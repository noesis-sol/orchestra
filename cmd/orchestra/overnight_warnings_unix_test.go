//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/load"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's warnings, each shown and passing, and its fix made where it offers one.

func TestUsageWaitCheck(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, settings string
		verdict        tui.CheckVerdict
		says           string
	}{
		{"the default", "", tui.CheckPassed, "the run waits up to 6h for the account's usage limit to reset"},
		{"5h", `{"usage_limit_wait": "5h"}`, tui.CheckPassed, "waits up to 5h"},
		{"under 5h", `{"usage_limit_wait": "2h"}`, tui.CheckWarned,
			"usage_limit_wait is 2h, under the 5h the session limit takes to reset: the limit, reached in the night, " +
				"would stop the run"},
		{"0", `{"usage_limit_wait": "0"}`, tui.CheckWarned, "usage_limit_wait is 0: the account's usage limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := checkSession(t, nil, nil, tc.settings)
			r := usageWaitCheck(runCtx(t), s)
			wantResult(t, r, tc.verdict, tc.says)
			if tc.verdict == tui.CheckPassed {
				if r.fix != nil {
					t.Errorf("a passing check offers a fix: %s", r.fix.question)
				}
				return
			}
			fixWith(t, s, r, "Wait up to 6h for the usage limit to reset?")
			if s.cfg.UsageLimitWait != 6*time.Hour {
				t.Errorf("the fix left usage_limit_wait at %v", s.cfg.UsageLimitWait)
			}
		})
	}
}

func TestUsageNowCheck(t *testing.T) {
	t.Parallel()
	const limited = `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,` +
		`"api_error":"usage_limit_reached","result":"You've hit your session limit · resets 7pm","num_turns":1}`
	for _, tc := range []struct {
		name, out string
		code      int
		verdict   tui.CheckVerdict
		says      string
	}{
		{"answering", `{"type":"result","subtype":"success","result":"ok","num_turns":1}`, 0, tui.CheckPassed,
			"the account isn't at its usage limit now"},
		{"at its limit", limited, 1, tui.CheckWarned,
			`the account is at its usage limit now ("You've hit your session limit · resets 7pm"): ` +
				"no ticket starts until it resets, at "},
		{"failing", `Invalid API key`, 1, tui.CheckWarned,
			"orchestra couldn't tell whether the account is at its usage limit now: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			bin := faketool.Write(t, dir, "claude", fmt.Sprintf("#!/bin/sh\necho \"$*\" > '%s/args'\n"+
				"cat > /dev/null\ncat <<'JSON'\n%s\nJSON\nexit %d\n", dir, tc.out, tc.code))
			r := usageNowCheck(bin)(runCtx(t), checkSession(t, nil, nil, ""))
			wantResult(t, r, tc.verdict, tc.says)
			if args := read(t, filepath.Join(dir, "args")); !strings.Contains(args, "-p --tools  --strict-mcp-config") {
				t.Errorf("claude was asked as %q, not as an organ is", args)
			}
		})
	}
	r := usageNowCheck(filepath.Join(t.TempDir(), "claude"))(runCtx(t),
		checkSession(t, []string{"--agent", "codex"}, nil, ""))
	wantResult(t, r, tui.CheckPassed, "workers are codex")
}

// The account at its limit says when it resets, and whether the run's wait for it runs out first.
func TestUsageLimitedNow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.Local)
	said := "You've hit your session limit · resets 7pm"
	if got := usageLimitedNow(said, 6*time.Hour, now); !strings.HasSuffix(got, "until it resets, at 19:00, in 4h") {
		t.Errorf("with 6h to wait: %q", got)
	}
	if got := usageLimitedNow(said, 2*time.Hour, now); !strings.HasSuffix(got,
		"in 4h, after the run's wait for it (usage_limit_wait 2h) runs out: the run would stop") {
		t.Errorf("with 2h to wait: %q", got)
	}
	if got := usageLimitedNow("You've hit your limit", 2*time.Hour, now); !strings.HasSuffix(got,
		"no ticket starts until it resets") {
		t.Errorf("with no reset said: %q", got)
	}
}

func TestTicketLimitCheck(t *testing.T) {
	t.Parallel()
	wantResult(t, ticketLimitCheck(runCtx(t), checkSession(t, nil, nil, `{"ticket_limit": "3h"}`)),
		tui.CheckPassed, "each ticket's worker has 3h (ticket_limit)")
	s := checkSession(t, nil, nil, "")
	r := ticketLimitCheck(runCtx(t), s)
	wantResult(t, r, tui.CheckWarned, "no ticket limit (ticket_limit in settings.json, --ticket-limit): "+
		"a hung worker would hold its slot all night")
	fixWith(t, s, r, "Give each ticket's worker 2h?")
	if s.cfg.TicketLimit != 2*time.Hour {
		t.Errorf("the fix set the ticket limit %v", s.cfg.TicketLimit)
	}
}

func TestOtherRunsCheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	env := map[string]string{runsDirEnv: dir}
	wantResult(t, otherRunsCheck(runCtx(t), checkSession(t, nil, env, "")), tui.CheckPassed,
		"no other orchestra run on the machine")
	body := fmt.Sprintf(`{"pid":%d,"repo":"/src/other","concurrent":3,"started":%q,"running":2}`, os.Getpid(),
		time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(filepath.Join(dir, "run-1-a.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wantResult(t, otherRunsCheck(runCtx(t), checkSession(t, nil, env, "")), tui.CheckWarned,
		fmt.Sprintf("another orchestra run is going on the machine, in /src/other (PID %d, 3 tickets at a time, "+
			"2 running): their workers and checks share the machine", os.Getpid()))
}

// processes is a listing of the machine's processes, for the load and VM checks.
func processes(ps ...namedProcess) func(context.Context) ([]namedProcess, error) {
	return func(context.Context) ([]namedProcess, error) { return ps, nil }
}

func TestLoadCheck(t *testing.T) {
	t.Parallel()
	kernel := func(k load.Kernel, err error) func(context.Context) (load.Kernel, error) {
		return func(context.Context) (load.Kernel, error) { return k, err }
	}
	ps := processes(namedProcess{pid: 2, name: "cc1plus", cpu: 390, memory: gb},
		namedProcess{pid: 3, name: "Google Chrome", cpu: 12, memory: 6 * gb},
		namedProcess{pid: 4, name: "idle", memory: 1024}, namedProcess{pid: 5, name: "Xcode", cpu: 80, memory: 3 * gb},
		namedProcess{pid: 6, name: "Slack", cpu: 1, memory: 2 * gb})
	for _, tc := range []struct {
		name    string
		k       load.Kernel
		err     error
		verdict tui.CheckVerdict
		says    string
	}{
		{"idle", load.Kernel{Load1: 3.2, Cores: 10, Pressure: load.PressureNormal}, nil, tui.CheckPassed,
			"the machine isn't busy: a load of 3.2 on 10 cores, memory pressure normal"},
		{"busy", load.Kernel{Load1: 23.4, Cores: 10, Pressure: load.PressureNormal}, nil, tui.CheckWarned,
			"the machine is busy, a load of 23.4 on 10 cores, memory pressure normal (most of it cc1plus 390%, " +
				"Xcode 80% and Google Chrome 12%): the workers and their checks would run slowly"},
		{"short of memory", load.Kernel{Load1: 1, Cores: 10, Pressure: load.PressureWarn}, nil, tui.CheckWarned,
			"memory is short (the kernel's memory pressure is warn), most of it held by Google Chrome 6.0 GB, " +
				"Xcode 3.0 GB and Slack 2.0 GB: "},
		{"unreadable", load.Kernel{}, errors.ErrUnsupported, tui.CheckWarned, "orchestra couldn't read the machine's load"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantResult(t, loadCheck(kernel(tc.k, tc.err), ps)(runCtx(t), nil), tc.verdict, tc.says)
		})
	}
}

func TestVMCheck(t *testing.T) {
	t.Parallel()
	vm := namedProcess{pid: 40, parent: 1, name: "com.apple.Virtualization.VirtualMachine", memory: 8 * gb}
	for _, tc := range []struct {
		name    string
		ps      []namedProcess
		verdict tui.CheckVerdict
		says    string
	}{
		{"none", []namedProcess{{pid: 2, name: "zsh", memory: gb}}, tui.CheckPassed,
			"no virtual machine holds more than 4.0 GB of memory"},
		{"small", []namedProcess{{pid: 40, name: vm.name, memory: 2 * gb}}, tui.CheckPassed, "no virtual machine"},
		{"Docker's, by its app running", []namedProcess{vm, {pid: 30, parent: 1, name: "com.docker.backend"}},
			tui.CheckWarned, "a virtual machine holds 8.0 GB of memory (com.apple.Virtualization.VirtualMachine, PID 40); " +
				"Docker Desktop is running: the workers and their checks get only what it leaves. Stop it"},
		{"under its app", []namedProcess{{pid: 7, parent: 1, name: "limactl"},
			{pid: 8, parent: 7, name: "qemu-system-aarch64", memory: 12 * gb}}, tui.CheckWarned,
			"Lima (Colima)'s virtual machine holds 12 GB of memory (qemu-system-aarch64, PID 8): "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantResult(t, vmCheck(processes(tc.ps...))(runCtx(t), nil), tc.verdict, tc.says)
		})
	}
	failing := func(context.Context) ([]namedProcess, error) { return nil, errors.New("ps: not found") }
	wantResult(t, vmCheck(failing)(runCtx(t), nil), tui.CheckWarned, "couldn't list the machine's processes")
}

// ps's listing is read with each process's name, macOS's paths with spaces and all.
func TestParseNamed(t *testing.T) {
	t.Parallel()
	ps, err := parseNamed("    1     0  10704   0.8 /sbin/launchd\n" +
		"  111 99603  81856  11,1 /Applications/Google Chrome.app/Contents/MacOS/Google Chrome Helper (GPU)\n\n" +
		"  220     1   2048   0.0 kworker/0:1\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []namedProcess{{pid: 1, parent: 0, memory: 10704 * 1024, cpu: 0.8, name: "launchd"},
		{pid: 111, parent: 99603, memory: 81856 * 1024, cpu: 11.1, name: "Google Chrome Helper (GPU)"},
		{pid: 220, parent: 1, memory: 2048 * 1024, name: "kworker/0:1"}}
	if fmt.Sprint(ps) != fmt.Sprint(want) {
		t.Errorf("parseNamed = %+v, want %+v", ps, want)
	}
	for _, bad := range []string{"1 0 10\n", "x 0 10 0.0 sh\n", "1 0 10 much sh\n"} {
		if _, err := parseNamed(bad); err == nil {
			t.Errorf("parseNamed(%q) read it", bad)
		}
	}
	if ps, err := listNamed(runCtx(t)); err != nil || len(ps) == 0 {
		t.Errorf("listNamed: %d processes, %v", len(ps), err)
	}
}

func TestBatteryCheck(t *testing.T) {
	t.Parallel()
	power := func(battery bool, err error) func(context.Context, string) (bool, error) {
		return func(context.Context, string) (bool, error) { return battery, err }
	}
	wantResult(t, batteryCheck("darwin", power(false, nil))(runCtx(t), nil), tui.CheckPassed, "the machine is plugged in")
	wantResult(t, batteryCheck("darwin", power(true, nil))(runCtx(t), nil), tui.CheckWarned,
		"the machine runs on battery: it would run flat, or sleep, in the night. Plug it in")
	wantResult(t, batteryCheck("plan9", power(false, errors.ErrUnsupported))(runCtx(t), nil), tui.CheckWarned,
		"orchestra couldn't tell whether the machine runs on battery")
}

func TestPmsetOnBattery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		out     string
		battery bool
		err     error
	}{
		{"Now drawing from 'Battery Power'\n -InternalBattery-0 (id=37224547)\t63%; discharging; 1:15 remaining\n",
			true, nil},
		{"Now drawing from 'AC Power'\n -InternalBattery-0 (id=37224547)\t100%; charged; 0:00 remaining\n", false, nil},
		{"Now drawing from 'AC Power'\n", false, nil}, // a Mac with no battery
		{"Now drawing from 'UPS Power'\n", false, nil},
		{"", false, errNoPowerSource},
	} {
		battery, err := pmsetOnBattery(tc.out)
		if battery != tc.battery || !errors.Is(err, tc.err) {
			t.Errorf("pmsetOnBattery(%q) = %v, %v", tc.out, battery, err)
		}
	}
}

func TestSysfsOnBattery(t *testing.T) {
	t.Parallel()
	supply := func(kind, online, status string) fstest.MapFS {
		fsys := fstest.MapFS{}
		for name, text := range map[string]string{"type": kind, "online": online, "status": status} {
			if text != "" {
				fsys[name] = &fstest.MapFile{Data: []byte(text + "\n")}
			}
		}
		return fsys
	}
	machine := func(supplies map[string]fstest.MapFS) fstest.MapFS {
		fsys := fstest.MapFS{}
		for dir, files := range supplies {
			for name, f := range files {
				fsys[dir+"/"+name] = f
			}
		}
		return fsys
	}
	for _, tc := range []struct {
		name    string
		fsys    fstest.MapFS
		battery bool
	}{
		{"discharging", machine(map[string]fstest.MapFS{"AC": supply("Mains", "0", ""),
			"BAT0": supply("Battery", "", "Discharging")}), true},
		{"plugged in", machine(map[string]fstest.MapFS{"AC": supply("Mains", "1", ""),
			"BAT0": supply("Battery", "", "Charging")}), false},
		{"a desktop", fstest.MapFS{}, false},
	} {
		battery, err := sysfsOnBattery(tc.fsys)
		if err != nil || battery != tc.battery {
			t.Errorf("%s: %v, %v", tc.name, battery, err)
		}
	}
}

func TestTemplatesCheck(t *testing.T) {
	t.Parallel()
	s := checkSession(t, nil, nil, "")
	s.cfg.Repo = olderPromptRepo(t)
	r := templatesCheck(runCtx(t), s)
	wantResult(t, r, tui.CheckWarned, ".orchestra/worker-prompt.md is older than orchestra's worker prompt template",
		"orchestra init --update rewrites those sections")
	fixWith(t, s, r, "Bring them up to date (orchestra init --update) and commit that?")
	if notes := project.TemplateNotes(s.cfg.Repo); len(notes) != 0 {
		t.Errorf("notes after the fix: %q", notes)
	}
	if prompt := read(t, filepath.Join(s.cfg.Repo, project.Dir, "worker-prompt.md")); !strings.Contains(prompt,
		"## Project notes\n") {
		t.Errorf("the fix lost the project's own section:\n%s", prompt)
	}
	_, git := gitRepo(t)
	if dirty := git(s.cfg.Repo, "status", "--porcelain", "--", project.Dir); dirty != "" {
		t.Errorf("the fix left changes uncommitted:\n%s", dirty)
	}
	if msg := git(s.cfg.Repo, "log", "-1", "--format=%s"); !strings.Contains(msg, "Bring the worker prompt") {
		t.Errorf("the last commit is %q", msg)
	}
	wantResult(t, templatesCheck(runCtx(t), s), tui.CheckPassed, "up to date with orchestra's templates")
	s.cfg.ownPrompt = true
	wantResult(t, templatesCheck(runCtx(t), s), tui.CheckPassed, "the run's worker prompt is its own")
}

// readyTickets is a backlog with its ready tickets, by scope.
type readyTickets struct {
	askedTickets
	ready map[string][]dispatch.Ticket
}

func (r readyTickets) Ready(_ context.Context, scope string) ([]dispatch.Ticket, error) {
	return r.ready[scope], nil
}

func TestReadyCheck(t *testing.T) {
	t.Parallel()
	human := []string{dispatch.HumanLabel}
	question := func(id string) dispatch.Ticket {
		return dispatch.Ticket{ID: id, Title: "Which?", Status: dispatch.StatusOpen, Labels: human}
	}
	on := func(id string, deps ...dispatch.Ticket) dispatch.Ticket {
		for i := range deps {
			deps[i].DependencyType = "blocks"
		}
		return dispatch.Ticket{ID: id, Status: dispatch.StatusOpen, Dependencies: deps}
	}
	open := dispatch.Ticket{ID: "k-4", Status: dispatch.StatusOpen}
	closed := dispatch.Ticket{ID: "k-6", Status: dispatch.StatusClosed}
	answered := dispatch.Ticket{ID: "q-3", Status: dispatch.StatusClosed, Labels: human}
	backlog := askedTickets{"q-1": question("q-1"), "q-2": question("q-2"), "k-4": open, "k-6": closed, "q-3": answered,
		"k-1.1": on("k-1.1", question("q-1")),       // waits only on a question
		"k-2":   on("k-2", question("q-1"), open),   // on a ticket too
		"k-3":   on("k-3", question("q-2"), closed), // on a question, the rest closed
		"k-5":   on("k-5", answered),                // on a question answered
	}
	two := []dispatch.Ticket{{ID: "k-7"}, {ID: "k-8"}}
	for _, tc := range []struct {
		name, scope string
		ready       []dispatch.Ticket
		verdict     tui.CheckVerdict
		says        string
	}{
		{"ready", "", two, tui.CheckPassed, "2 tickets are ready, and 2 more wait only on questions, " +
			"to be ready once those are answered"},
		{"one in scope", "k-1", two[:1], tui.CheckPassed, "1 ticket is ready, and 1 more waits only on questions"},
		{"none", "", nil, tui.CheckWarned, "no ticket is ready, and 2 more wait only on questions, to be ready once " +
			"those are answered: with nothing to work on, the run would end at once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := readyTickets{backlog, map[string][]dispatch.Ticket{tc.scope: tc.ready}}
			s := checkSession(t, nil, nil, "")
			s.cfg.Ticket = tc.scope
			r := readyCheck(func(*session) readyTracker { return tr })(runCtx(t), s)
			wantResult(t, r, tc.verdict, tc.says)
		})
	}
	tr := readyTickets{askedTickets{}, map[string][]dispatch.Ticket{"": two}}
	wantResult(t, readyCheck(func(*session) readyTracker { return tr })(runCtx(t), checkSession(t, nil, nil, "")),
		tui.CheckPassed, "2 tickets are ready")
}
