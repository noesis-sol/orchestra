package dispatch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// records reads the event stream in the checkout repo, failing the test unless each line is one
// JSON object and the last line is whole.
func records(t *testing.T, repo string) []map[string]any {
	t.Helper()
	b := read(t, filepath.Join(repo, project.RunPath(EventsName)))
	if !strings.HasSuffix(b, "\n") {
		t.Fatalf("the stream doesn't end with a whole line:\n%s", b)
	}
	var recs []map[string]any
	for line := range strings.Lines(b) {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil || r == nil {
			t.Fatalf("not a JSON object (%v): %s", err, line)
		}
		recs = append(recs, r)
	}
	return recs
}

// timeString is t as the stream gives it.
func timeString(t *testing.T, at time.Time) string {
	t.Helper()
	b, err := json.Marshal(at)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Trim(string(b), `"`)
}

// has checks that record r gives each of want, and none of the fields in absent.
func has(t *testing.T, r map[string]any, want map[string]any, absent ...string) {
	t.Helper()
	for k, v := range want {
		if got, ok := r[k]; !ok || got != v {
			t.Errorf("%s record: %s is %#v, want %#v\n%v", r["kind"], k, got, v, r)
		}
	}
	for _, k := range absent {
		if got, ok := r[k]; ok {
			t.Errorf("%s record: %s is %#v, want none\n%v", r["kind"], k, got, r)
		}
	}
}

// A run's event stream: a start record, a record for each event the run emits, the queue updates
// the log leaves out included, each named by its kind and giving the run's start, and an end record
// with the exit code. Every line is a JSON object, and an event's text is its line in the log.
func TestARunRecordsItsEvents(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first <of three>", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.beads.mu.Lock() // both at once, for one queue update
			w.beads.addLocked("B", "second", 2)
			w.beads.addLocked("C", "third", 3)
			w.beads.mu.Unlock()
			time.Sleep(readyPoll + time.Second) // past the next poll, which updates the queue
			return finishes("a.txt")(w)
		})
		h.worker("B", finishes("b.txt"))
		h.worker("C", func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" })
		o := h.loop()
		started := time.Now()
		o.log.Begin(h.repo, RunStart{Started: started, Version: "v1.2.3", Repo: h.repo, Branch: "main", Concurrency: 1})
		code := o.Run(context.Background())
		o.log.End(code)
		lines := o.log.RunLines()
		if err := o.log.Close(); err != nil {
			t.Fatal(err)
		}
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}

		recs := records(t, h.repo)
		var kinds, texts []string
		byKind := map[string][]map[string]any{}
		for _, r := range recs {
			kind, _ := r["kind"].(string)
			if kind != "info" {
				kinds = append(kinds, kind)
			}
			byKind[kind] = append(byKind[kind], r)
			if text, ok := r["text"].(string); ok {
				texts = append(texts, text)
			}
			if r["run"] != timeString(t, started) {
				t.Errorf("%s record: run %v, want %s", kind, r["run"], timeString(t, started))
			}
			if s, _ := r["time"].(string); s == "" {
				t.Errorf("%s record without a time", kind)
			} else if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
				t.Errorf("%s record: time %q isn't RFC 3339: %v", kind, s, err)
			}
		}
		want := []string{"start", "dispatch", "queue", "closed", "dispatch", "closed", "dispatch", "deferred", "done", "end"}
		if !equal(kinds, want) {
			t.Fatalf("kinds, info left out:\n%s\nwant:\n%s", strings.Join(kinds, " "), strings.Join(want, " "))
		}
		if len(byKind["info"]) == 0 {
			t.Error("no info records, the START line's among them")
		}

		has(t, recs[0], map[string]any{"kind": "start", "version": "v1.2.3", "repo": h.repo, "branch": "main",
			"concurrency": 1.0}, "scope", "feature", "text", "code")
		has(t, byKind["dispatch"][0], map[string]any{"ticket": "A", "title": "first <of three>", "n": 1.0,
			"limit": 10.0, "queued": 0.0, "text": "[1/10] A dispatching: first <of three>"}, "solo", "detail")
		has(t, byKind["dispatch"][1], map[string]any{"ticket": "B", "n": 2.0, "queued": 1.0})
		has(t, byKind["queue"][0], map[string]any{"queued": 2.0}, "text", "ticket", "n", "limit", "solo")
		has(t, byKind["closed"][0], map[string]any{"ticket": "A"}, "queued", "n")
		if d, _ := byKind["closed"][0]["detail"].(string); !strings.HasSuffix(d, " merged into main") {
			t.Errorf("closed record's detail: %q", d)
		}
		has(t, byKind["deferred"][0], map[string]any{"ticket": "C", "detail": "by the worker"})
		has(t, byKind["done"][0], map[string]any{"text": "READY_EMPTY after 3 tickets"}, "ticket")
		has(t, recs[len(recs)-1], map[string]any{"kind": "end", "code": 0.0}, "text")
		if b := read(t, filepath.Join(h.repo, project.RunPath(EventsName))); !strings.Contains(b, "<of three>") {
			t.Errorf("the title's < and > are escaped:\n%s", b)
		}

		// The log is unchanged: its lines, in order, are the records' texts.
		for i, l := range lines {
			lines[i] = l[len(logTime)+1:]
		}
		if !equal(texts, lines) {
			t.Errorf("records' texts:\n%s\nlog:\n%s", strings.Join(texts, "\n"), strings.Join(lines, "\n"))
		}
	})
}

// The kinds' names are the stream's interface: each kind has its own, lowercase.
func TestKindNames(t *testing.T) {
	want := map[Kind]string{EvInfo: "info", EvDispatch: "dispatch", EvClosed: "closed", EvDeferred: "deferred",
		EvWarn: "warn", EvStop: "stop", EvDone: "done", EvTriage: "triage", EvAsked: "asked", EvHold: "hold",
		EvDrain: "drain", EvResume: "resume", EvQueue: "queue", EvProbed: "probed", EvAnswered: "answered",
		EvFullCheck: "full_check"}
	if len(kindNames) != len(want) {
		t.Errorf("%d names for %d kinds", len(kindNames), len(want))
	}
	for k, name := range want {
		if got := k.String(); got != name {
			t.Errorf("kind %d is %q, want %q", int(k), got, name)
		}
	}
	if got := Kind(99).String(); got != "Kind(99)" {
		t.Errorf("an unknown kind is %q", got)
	}
}

// openLog opens a log in a folder of its own, closed when the test ends, and returns it with its path.
func openLog(t *testing.T) (*Log, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "orchestra.log")
	l, err := OpenLog(path, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() }) // closed already where the test closes it
	return l, path
}

// The stream keeps every run, as the log does, each run's records giving its own start.
func TestTheEventStreamKeepsEveryRun(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	first := time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local)
	for i, started := range []time.Time{first, first.Add(time.Hour)} {
		l, _ := openLog(t)
		l.Begin(repo, RunStart{Started: started, Concurrency: 1})
		l.Record(Event{Kind: EvInfo, Time: started, Text: "START"})
		l.End(i)
	}
	recs := records(t, repo)
	if len(recs) != 6 {
		t.Fatalf("%d records, want 6: %v", len(recs), recs)
	}
	for i, r := range recs {
		started := first.Add(time.Duration(i/3) * time.Hour)
		has(t, r, map[string]any{"run": timeString(t, started), "kind": []string{"start", "info", "end"}[i%3]})
	}
	has(t, recs[5], map[string]any{"code": 1.0})
}

// A record that can't be written is logged once, not as one of the run's lines, and the run goes on.
func TestAFailedRecordIsLoggedOnce(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	l, path := openLog(t)
	l.Begin(repo, RunStart{Started: time.Now(), Concurrency: 1})
	l.mu.Lock()
	_ = l.events.Close() // writes fail from here
	l.mu.Unlock()
	l.Record(Event{Kind: EvInfo, Time: time.Now(), Text: "one"})
	l.Record(Event{Kind: EvWarn, Time: time.Now(), Text: "two"})
	l.End(ExitOK)
	logged := read(t, path)
	if n := strings.Count(logged, "events not recorded in .orchestra/run/events.jsonl: "); n != 1 {
		t.Errorf("said %d times:\n%s", n, logged)
	}
	if lines := l.RunLines(); len(lines) > 0 {
		t.Errorf("among the run's lines: %q", lines)
	}
	if recs := records(t, repo); len(recs) != 1 || recs[0]["kind"] != "start" {
		t.Errorf("records: %v", recs)
	}
}

// A stream that can't be opened, here as .orchestra/run is a symlink, is logged once, and the run
// goes on without it.
func TestAnEventStreamThatCantBeOpenedIsLogged(t *testing.T) {
	t.Parallel()
	repo, elsewhere := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, project.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(repo, project.Dir, project.RunName)); err != nil {
		t.Fatal(err)
	}
	l, path := openLog(t)
	l.Begin(repo, RunStart{Started: time.Now(), Concurrency: 1})
	l.Record(Event{Kind: EvInfo, Time: time.Now(), Text: "START"})
	l.End(ExitOK)
	logged := read(t, path)
	want := "events not recorded in .orchestra/run/events.jsonl: .orchestra/run in " + repo + " is a symlink"
	if strings.Count(logged, want) != 1 {
		t.Errorf("logged:\n%s\nwant once: %s", logged, want)
	}
	if exists(filepath.Join(elsewhere, EventsName)) {
		t.Error("written through the symlink")
	}
}

// A symlink in place of the stream, inside the checkout, is replaced by the file rather than
// written through.
func TestASymlinkedEventStreamIsReplaced(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	readme := filepath.Join(repo, "README.md")
	if err := os.WriteFile(readme, []byte("# readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stream := filepath.Join(repo, project.RunPath(EventsName))
	if err := os.MkdirAll(filepath.Dir(stream), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "..", "README.md"), stream); err != nil {
		t.Fatal(err)
	}
	l, _ := openLog(t)
	l.Begin(repo, RunStart{Started: time.Now(), Concurrency: 1})
	l.End(ExitOK)
	if got := read(t, readme); got != "# readme\n" {
		t.Errorf("written through the symlink:\n%s", got)
	}
	if fi, err := os.Lstat(stream); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("the stream is not a file: %v, %v", fi, err)
	}
	if recs := records(t, repo); len(recs) != 2 {
		t.Errorf("records: %v", recs)
	}
}
