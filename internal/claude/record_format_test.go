package claude

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// jsonRecords are the records the hooks write as JSON, one object each or a log of them a line each.
var jsonRecords = []string{activityName, turnName, sessionName, eventsName, noticeName}

// recordFormats returns the format of each JSON record in the records folder, a line each for the
// events log: "v" as written, 0 where there is none.
func recordFormats(t *testing.T, records string) map[string][]int {
	t.Helper()
	formats := map[string][]int{}
	for _, name := range jsonRecords {
		b, err := os.ReadFile(filepath.Join(records, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		d := json.NewDecoder(bytes.NewReader(b))
		for d.More() {
			var r struct {
				V int `json:"v"`
			}
			if err := d.Decode(&r); err != nil {
				t.Fatalf("%s: %v\n%s", name, err, b)
			}
			formats[name] = append(formats[name], r.V)
		}
	}
	return formats
}

// rewriteRecords replaces from with to in each JSON record in the records folder, keeping its time,
// which the readers order the records by.
func rewriteRecords(t *testing.T, records, from, to string) {
	t.Helper()
	for _, name := range jsonRecords {
		p := filepath.Join(records, name)
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, bytes.ReplaceAll(b, []byte(from), []byte(to)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
			t.Fatal(err)
		}
	}
}

// Each JSON record the hooks write starts with the format it is in; the readers read records in that
// format and records with none, written before records carried it, alike, and take one in a newer
// format for unreadable rather than misread it, which the log says once for the worker.
func TestHookRecordsCarryTheirFormat(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "it's a worktree")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := reportingSettings(t, wt, false)
	records := Records(wt, "")
	transcript := transcriptAt(t, t.TempDir(), testID, `{"type":"user","message":{"role":"user","content":"Work on A."}}`)
	for _, s := range []hookStep{stepAt("SessionStart", " \t"+sessionInput(testID, transcript)), stepPrompt, stepUseBash,
		stepCompact("auto"), stepStop, stepNotification(idlePrompt)} {
		runHooks(t, settings, s.event, s.input)
		for name, vs := range recordFormats(t, records) {
			if slices.ContainsFunc(vs, func(v int) bool { return v != recordFormat }) {
				t.Errorf("after %s, %s is in formats %v, not %d", s.event, name, vs, recordFormat)
			}
		}
	}
	if got := recordFormats(t, records); len(got) != len(jsonRecords) {
		t.Fatalf("records written: %v", got)
	}

	var (
		mu    sync.Mutex
		lines []string
	)
	r := NewReporter(func(text string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, text)
	})
	read := func(t *testing.T) {
		t.Helper()
		u, ok := r.LastToolUse(wt)
		if !ok || u.Event != "Stop" || u.Stopped.IsZero() {
			t.Errorf("LastToolUse = %+v, %v", u, ok)
		}
		if rec := r.HookRecord(wt); !slices.Equal(rec.Compactions, []string{"auto"}) {
			t.Errorf("HookRecord = %+v", rec)
		}
		if s, ok := r.Session(wt); !ok || s.ID != testID || s.Transcript != transcript {
			t.Errorf("Session = %+v, %v", s, ok)
		}
		if st := State(records); st != dispatch.StateIdle {
			t.Errorf("State = %v", st)
		}
	}
	t.Run("current format", read)
	v := `{"v":1,`
	rewriteRecords(t, records, v, `{`)
	if got := recordFormats(t, records); slices.ContainsFunc(slices.Collect(maps.Values(got)), func(vs []int) bool {
		return slices.ContainsFunc(vs, func(v int) bool { return v != 0 })
	}) {
		t.Fatalf("formats left: %v", got)
	}
	t.Run("no format", read)
	if len(lines) > 0 {
		t.Errorf("logged: %q", lines)
	}

	rewriteRecords(t, records, `{`, `{"v":2,`)
	for range 3 {
		if u, ok := r.LastToolUse(wt); ok {
			t.Errorf("LastToolUse = %+v, read from a newer format", u)
		}
		if rec := r.HookRecord(wt); rec.Compactions != nil || rec.Subagents != 0 {
			t.Errorf("HookRecord = %+v, read from a newer format", rec)
		}
		if s, ok := r.Session(wt); ok {
			t.Errorf("Session = %+v, read from a newer format", s)
		}
		if st := State(records); st != dispatch.StateUnknown {
			t.Errorf("State = %v, read from a newer format", st)
		}
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "format 2") || !strings.Contains(lines[0], wt) {
		t.Errorf("logged: %q", lines)
	}
}
