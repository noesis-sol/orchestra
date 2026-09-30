package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// recordSink keeps events for assertions.
type recordSink struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordSink) Event(ev Event) { r.mu.Lock(); r.events = append(r.events, ev); r.mu.Unlock() }
func (r *recordSink) Status(Status)  {}
func (r *recordSink) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, ev := range r.events {
		b.WriteString(ev.Text + "\n")
	}
	return b.String()
}

// mergeFixture is a repository on main with a ticket branch in its own worktree, and an Orch set
// up to merge it.
type mergeFixture struct {
	repo string
	git  func(dir string, args ...string) string
	orch *Orch
	sink *recordSink
}

func newMergeFixture(t *testing.T, check string) *mergeFixture {
	t.Helper()
	tabClose = func(string) {}
	repo, git := gitRepo(t)
	git(repo, "branch", "-M", "main")
	os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("line 1\n"), 0o644)
	git(repo, "add", ".")
	git(repo, "commit", "-q", "-m", "shared file")
	log, err := openLogger(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	o := &Orch{cfg: Config{Repo: repo, Base: "main", Check: check, LogPath: "log"}, log: log, sink: sink}
	return &mergeFixture{repo: repo, git: git, orch: o, sink: sink}
}

// ticket makes wt/<id> in its own worktree with one commit writing file.
func (f *mergeFixture) ticket(t *testing.T, id, file, content string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), id)
	f.git(f.repo, "worktree", "add", "-q", "-b", "wt/"+id, wt, "main")
	os.WriteFile(filepath.Join(wt, file), []byte(content), 0o644)
	f.git(wt, "add", ".")
	f.git(wt, "commit", "-q", "-m", id+": change "+file)
	return wt
}

func (f *mergeFixture) onMain(t *testing.T, file, content string) {
	t.Helper()
	os.WriteFile(filepath.Join(f.repo, file), []byte(content), 0o644)
	f.git(f.repo, "add", ".")
	f.git(f.repo, "commit", "-q", "-m", "main moves on: "+file)
}

func TestMergeFastForwardsWhenMainHasNotMoved(t *testing.T) {
	f := newMergeFixture(t, "exit 1") // must not run: nothing to re-check
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	if s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab"); s != nil {
		t.Fatal(s.text)
	}
	if !strings.Contains(f.git(f.repo, "log", "--oneline", "-1"), "k-1: change a.txt") {
		t.Error("the ticket was not merged")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("the worktree should be removed after merging")
	}
	if !strings.Contains(f.sink.text(), "k-1 closed") {
		t.Errorf("events:\n%s", f.sink.text())
	}
}

func TestMergeRebasesAndRechecksWhenMainMoved(t *testing.T) {
	f := newMergeFixture(t, "test -f a.txt && test -f b.txt") // passes only on the rebased tree
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab"); s != nil {
		t.Fatal(s.text)
	}
	log := f.git(f.repo, "log", "--oneline")
	if !strings.Contains(log, "k-1: change a.txt") || !strings.Contains(log, "main moves on: b.txt") {
		t.Errorf("history:\n%s", log)
	}
	ev := f.sink.text()
	if !strings.Contains(ev, "rebased wt/k-1 onto main") || !strings.Contains(ev, "passes on the rebased") {
		t.Errorf("events:\n%s", ev)
	}
}

func TestMergeLeavesAConflictForReview(t *testing.T) {
	f := newMergeFixture(t, "true")
	wt := f.ticket(t, "k-1", "shared.txt", "line 1 from the ticket\n")
	f.onMain(t, "shared.txt", "line 1 from main\n")
	before := f.git(f.repo, "rev-parse", "main")
	if s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab"); s != nil {
		t.Fatal(s.text)
	}
	if f.git(f.repo, "rev-parse", "main") != before {
		t.Error("main must not change on a conflict")
	}
	if !strings.Contains(f.sink.text(), "MERGE_CONFLICT: k-1") {
		t.Errorf("events:\n%s", f.sink.text())
	}
	if st := f.git(wt, "status", "--porcelain"); st != "" {
		t.Errorf("the worktree should be left clean, not mid-rebase: %q", st)
	}
	if got := f.orch.setAside(); len(got) != 1 || got[0] != "k-1" {
		t.Errorf("set aside = %v", got)
	}
}

func TestMergeLeavesAFailingRecheckForReview(t *testing.T) {
	f := newMergeFixture(t, "exit 3")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	before := f.git(f.repo, "rev-parse", "main")
	f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab")
	if f.git(f.repo, "rev-parse", "main") != before {
		t.Error("main must not change when the checks fail")
	}
	if !strings.Contains(f.sink.text(), "CHECKS_FAILED: k-1") {
		t.Errorf("events:\n%s", f.sink.text())
	}
}

func TestMergeWithoutACheckCommandSaysSo(t *testing.T) {
	f := newMergeFixture(t, "")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab")
	if ev := f.sink.text(); !strings.Contains(ev, "without checking the rebased code") || !strings.Contains(ev, "k-1 closed") {
		t.Errorf("events:\n%s", ev)
	}
}

func TestWorkersMergingAtTheSameTimeBothLand(t *testing.T) {
	f := newMergeFixture(t, "true")
	const n = 4
	wts := make([]string, n)
	for i := range wts {
		wts[i] = f.ticket(t, fmt.Sprintf("k-%d", i), fmt.Sprintf("f%d.txt", i), "x\n")
	}
	var wg sync.WaitGroup
	for i := range wts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if s := f.orch.merge(context.Background(), fmt.Sprintf("k-%d", i), fmt.Sprintf("wt/k-%d", i), wts[i], "tab"); s != nil {
				t.Errorf("k-%d: %s", i, s.text)
			}
		}(i)
	}
	wg.Wait()
	log := f.git(f.repo, "log", "--oneline")
	for i := 0; i < n; i++ {
		if !strings.Contains(log, fmt.Sprintf("k-%d: change f%d.txt", i, i)) {
			t.Errorf("k-%d missing from main:\n%s", i, log)
		}
	}
	if st := f.git(f.repo, "status", "--porcelain"); st != "" {
		t.Errorf("main checkout left dirty: %q", st)
	}
}

func TestPickNextSkipsRunningTickets(t *testing.T) {
	ready := []Ticket{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	if tk, q := pickNext(ready, map[string]bool{"a": true}); tk == nil || tk.ID != "b" || q != 1 {
		t.Errorf("got %v, %d", tk, q)
	}
	if tk, _ := pickNext(ready, map[string]bool{"a": true, "b": true, "c": true}); tk != nil {
		t.Errorf("everything is running, got %v", tk)
	}
}

func TestConcurrencyPrecedence(t *testing.T) {
	cases := []struct {
		flag, setting, want int
		fails               bool
	}{
		{0, 0, 1, false}, {0, 3, 3, false}, {2, 3, 2, false}, {17, 0, 0, true}, {-1, 0, 0, true},
	}
	for _, c := range cases {
		got, err := resolveConcurrency(c.flag, Settings{Concurrency: c.setting})
		if (err != nil) != c.fails || (!c.fails && got != c.want) {
			t.Errorf("resolveConcurrency(%d, %d) = %d, %v", c.flag, c.setting, got, err)
		}
	}
}

func TestDetectCheckAndDefaultChoice(t *testing.T) {
	kinieta := "- Check your work with `scripts/ci-local.sh`. It runs the CI jobs locally"
	if got := detectCheck(kinieta); got != "scripts/ci-local.sh" {
		t.Errorf("detectCheck = %q", got)
	}
	if got := detectCheck(promptTemplate); got != "" {
		t.Errorf("the template's placeholder is not a check command: %q", got)
	}
	c := defaultChoice(Settings{}, kinieta)
	if c.Check != "scripts/ci-local.sh" || c.checkFrom != "found in the worker prompt" || c.Concurrent != 1 || !c.unasked {
		t.Errorf("from the prompt: %+v", c)
	}
	c = defaultChoice(Settings{Check: "make check", Concurrency: 3}, kinieta)
	if c.Check != "make check" || c.Concurrent != 3 || c.unasked {
		t.Errorf("settings win: %+v", c)
	}
}

func TestApplySettingsSavesAndExplains(t *testing.T) {
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755)
	st, err := applySettings(repo, initChoice{Check: "make check", Concurrent: 3})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(settingsPath(repo))
	var m map[string]any
	json.Unmarshal(raw, &m)
	if m["concurrent"] != float64(3) || m["check"] != "make check" {
		t.Errorf("settings.json = %s", raw)
	}
	if st.kind != stepCaution || !strings.Contains(st.detail, "side by side") {
		t.Errorf("more than 1 should come with a caution: %+v", st)
	}
	st, _ = applySettings(repo, initChoice{Concurrent: 1, unasked: true})
	if st.kind != stepCaution || !strings.Contains(st.detail, "merges unchecked") || !strings.Contains(st.detail, "not asked") {
		t.Errorf("no check, not asked: %+v", st)
	}
	st, _ = applySettings(repo, initChoice{Check: "make check", Concurrent: 1})
	if st.kind != stepDone {
		t.Errorf("one at a time with a check is plain done: %+v", st)
	}
}

func TestNextStepsOnlyListWhatIsLeft(t *testing.T) {
	repo, git := gitRepo(t)
	steps, _ := initProject(repo, "make check", false)
	next := nextSteps(repo, steps, prerequisites(repo))
	joined := strings.Join(next, "\n")
	if !strings.Contains(joined, "Commit .orchestra/") || !strings.Contains(joined, "orchestra") {
		t.Errorf("fresh init: %q", next)
	}
	if strings.Contains(joined, "placeholders") {
		t.Errorf("--check filled the placeholders: %q", next)
	}
	git(repo, "add", ".orchestra")
	git(repo, "commit", "-q", "-m", "setup")
	steps, _ = initProject(repo, "", false)
	if joined := strings.Join(nextSteps(repo, steps, nil), "\n"); strings.Contains(joined, "Commit") || strings.Contains(joined, "Read ") {
		t.Errorf("nothing to commit or read on a second run: %q", joined)
	}
}

func TestDashboardShowsSeveralWorkers(t *testing.T) {
	m := newModel(Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {})
	m.active = map[string]Status{}
	for i, title := range []string{"Competing timelines on the same view and property fight each other every frame",
		"Open the property model", "Warn in debug builds when a chain call is silently ignored"} {
		id := fmt.Sprintf("kinieta-%d", i)
		m.active[id] = Status{Ticket: id, Title: title, Started: time.Now().Add(-time.Duration(i) * time.Minute), Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}
	}
	m.width, m.height = 66, 40
	v := m.View()
	if lines := strings.Split(v, "\n"); len(lines) > m.height {
		t.Errorf("view is %d lines", len(lines))
	}
	for _, l := range strings.Split(v, "\n") {
		if ansi.StringWidth(l) > m.width {
			t.Errorf("line %d wide", ansi.StringWidth(l))
		}
	}
	plain := ansi.Strip(v)
	for _, want := range []string{"kinieta-0", "kinieta-1", "kinieta-2", "3 running of 3"} {
		if !strings.Contains(plain, want) {
			t.Errorf("view lacks %q", want)
		}
	}
	m = runEvents(m, Event{Kind: EvHold, Ticket: "kinieta-0", Text: "HOLD: PAUSED: kinieta-0 …"})
	if !strings.Contains(ansi.Strip(m.View()), "stopping") {
		t.Error("a hold should show the run as stopping")
	}
}

func TestDashboardFitsShortPanes(t *testing.T) {
	m := newModel(Config{Limit: 40, Base: "batch/2026-09-28", Concurrency: 3}, func() {})
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("kinieta-%03d", i)
		m = runEvents(m, Event{Kind: EvDispatch, N: i + 1, Ticket: id, Title: "A ticket"},
			Event{Kind: EvClosed, Ticket: id, Detail: "abc1234 merged"})
	}
	m.active = map[string]Status{}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("kinieta-w%d", i)
		m.active[id] = Status{Ticket: id, Title: "Competing timelines on the same view and property fight each other every frame",
			Started: time.Now(), Agent: "working", Activity: "⏺ Bash(scripts/ci-local.sh)"}
	}
	for _, size := range [][2]int{{140, 16}, {66, 12}, {40, 8}, {66, 24}, {120, 50}} {
		m.width, m.height = size[0], size[1]
		v := m.View()
		lines := strings.Split(v, "\n")
		if len(lines) > m.height {
			t.Errorf("%dx%d: view is %d lines", size[0], size[1], len(lines))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > m.width {
				t.Errorf("%dx%d: line %d wide", size[0], size[1], ansi.StringWidth(l))
			}
		}
		plain := ansi.Strip(v)
		if !strings.Contains(plain, "kinieta-w0") || !strings.Contains(plain, "running of") {
			t.Errorf("%dx%d: workers not shown:\n%s", size[0], size[1], plain)
		}
	}
}
