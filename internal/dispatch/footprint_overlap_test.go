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

// sourcesIn reports which functions files mention, as repoFiles.mentions does, from their contents.
func sourcesIn(contents map[string]string) func(file, fn string) bool {
	r := &repoFiles{read: func(file string) ([]byte, error) {
		c, ok := contents[file]
		if !ok {
			return nil, os.ErrNotExist
		}
		return []byte(c), nil
	}}
	return r.mentions
}

// mergeSources are merge.go and run.go as TestSharedComparesFunctionsWhenBothNameThem has them.
var mergeSources = sourcesIn(map[string]string{
	"merge.go": "func (o *Loop) merge() { o.runCheck() }\nfunc (o *Loop) runCheck() {}",
	"run.go":   "func pickNext() {}",
})

var trackedNested = []string{
	"run.go", "README.md", "docs/running.md", "internal/dispatch/run.go", "internal/dispatch/loop.go",
	"internal/dispatch/loop_test.go", "internal/tui/run.go", "internal/tui/view/view.go",
}

// A bare name means every file of that name, the one at the top included.
func TestTicketFootprintMeansEveryFileOfThatName(t *testing.T) {
	all := []string{"internal/dispatch/run.go", "internal/tui/run.go", "run.go"}
	if fp := TicketFootprint(Ticket{Description: "Picking lives in run.go."}, trackedNested, ""); !slices.Equal(fp.Files, all) {
		t.Errorf("in the text: %q, want %q", fp.Files, all)
	}
	if fp := TicketFootprint(Ticket{Metadata: []byte(`{"files": "run.go"}`)}, trackedNested, ""); !slices.Equal(fp.Files, all) {
		t.Errorf("in the metadata: %q, want %q", fp.Files, all)
	}
	if fp := TicketFootprint(Ticket{Description: "See dispatch/run.go."}, trackedNested, ""); !slices.Equal(fp.Files, []string{"internal/dispatch/run.go"}) {
		t.Errorf("a partial path: %q", fp.Files)
	}
	other := TicketFootprint(Ticket{Description: "Change internal/dispatch/run.go."}, trackedNested, "")
	if what := shared(TicketFootprint(Ticket{Description: "Change run.go."}, trackedNested, ""), other, nil, nil); what != "internal/dispatch/run.go" {
		t.Errorf("run.go and internal/dispatch/run.go share %q", what)
	}
}

// A folder or a glob in the files metadata means the files under it or matching it.
func TestFilesMetadataTakesFoldersAndGlobs(t *testing.T) {
	cases := []struct {
		files string
		want  []string
	}{
		{"internal/dispatch/", []string{"internal/dispatch/loop.go", "internal/dispatch/loop_test.go", "internal/dispatch/run.go"}},
		{"internal/tui", []string{"internal/tui/run.go", "internal/tui/view/view.go"}},
		{"dispatch/", []string{"internal/dispatch/loop.go", "internal/dispatch/loop_test.go", "internal/dispatch/run.go"}},
		{"*.md", []string{"README.md", "docs/running.md"}},
		{"internal/dispatch/*_test.go", []string{"internal/dispatch/loop_test.go"}},
		{"internal/**/view.go", []string{"internal/tui/view/view.go"}},
		{"internal/*", []string{"internal/dispatch/loop.go", "internal/dispatch/loop_test.go", "internal/dispatch/run.go",
			"internal/tui/run.go", "internal/tui/view/view.go"}},
		{"*.swift", nil},
		{"internal/new/", []string{"internal/new/"}}, // a folder the ticket adds, holding the files under it
	}
	for _, c := range cases {
		fp := TicketFootprint(Ticket{Metadata: []byte(`{"files": ["` + c.files + `"]}`)}, trackedNested, "")
		if !slices.Equal(fp.Files, c.want) {
			t.Errorf("%s: %q, want %q", c.files, fp.Files, c.want)
		}
	}
	folder := TicketFootprint(Ticket{Metadata: []byte(`{"files": "internal/dispatch/"}`)}, trackedNested, "")
	file := TicketFootprint(Ticket{Description: "Fix internal/dispatch/loop.go."}, trackedNested, "")
	if what := shared(file, folder, nil, nil); what != "internal/dispatch/loop.go" {
		t.Errorf("a file in a folder another ticket lists: shared %q", what)
	}
	// Text names files, not folders: "the .orchestra folder" is no list of its files.
	if fp := TicketFootprint(Ticket{Description: "Look in internal/dispatch/ and *.md."}, trackedNested, ""); !fp.Empty() {
		t.Errorf("a folder in the text: %+v", fp)
	}
}

// A ticket naming only functions overlaps one naming the file they are in, and one naming a
// function the shared file doesn't mention compares by file.
func TestSharedLocatesFunctionsInFiles(t *testing.T) {
	in := sourcesIn(map[string]string{
		"loop.go": "type Loop struct{}\nfunc (o *Loop) merge() { pickNext() }\nfunc (o *Loop) wait() {}",
		"pick.go": "func pickNext() {}",
	})
	cases := []struct {
		name         string
		ready, run   Footprint
		edited, want string
	}{
		{"functions against their file", Footprint{Funcs: []string{"Loop.merge"}}, Footprint{Files: []string{"loop.go"}}, "", "loop.go"},
		{"a file against its functions", Footprint{Files: []string{"loop.go"}}, Footprint{Funcs: []string{"merge"}}, "", "loop.go"},
		{"functions against a file without them", Footprint{Funcs: []string{"Loop.merge"}}, Footprint{Files: []string{"pick.go"}}, "", ""},
		{"functions against an edited file", Footprint{Funcs: []string{"wait"}}, Footprint{Files: []string{"pick.go"}}, "loop.go", "loop.go"},
		{"other functions in the file", Footprint{Funcs: []string{"wait"}}, Footprint{Files: []string{"loop.go"}, Funcs: []string{"Loop.merge"}}, "", ""},
		{"a stray function", Footprint{Files: []string{"loop.go"}, Funcs: []string{"Run"}}, Footprint{Files: []string{"loop.go"}, Funcs: []string{"Loop.merge"}}, "", "loop.go"},
		{"functions in another file", Footprint{Files: []string{"pick.go"}, Funcs: []string{"pickNext"}}, Footprint{Files: []string{"loop.go"}, Funcs: []string{"wait"}}, "", ""},
		{"a file that can't be read", Footprint{Funcs: []string{"merge"}}, Footprint{Files: []string{"new.go"}}, "", ""},
	}
	for _, c := range cases {
		var edited []string
		if c.edited != "" {
			edited = []string{c.edited}
		}
		if got := shared(c.ready, c.run, edited, in); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// t.Run(), GitHub and iOS are no functions of the project's; o.log.Raw() is one.
func TestTicketFootprintTightensFunctionNames(t *testing.T) {
	cases := []struct {
		name, text string
		want       []string
	}{
		{"a test's methods", "Wrap it in t.Run() and `t.Run(name, func(t *testing.T){…})`, with b.Loop() and `t.Helper()`.", nil},
		{"brands", "Like `GitHub` does on `iOS` and `macOS`, `waitSettled`.", []string{"waitSettled"}},
		{"a field named like a package", "Log it with o.log.Raw() and `o.json.Decode(v)`, not log.Printf().", []string{"Decode", "Raw"}},
		{"a standard package's variable", "Write to os.Stdout.Write() and `http.DefaultClient.Do(req)`.", nil},
		{"a method of a type, through a field", "Call h.Loop.merge().", []string{"Loop.merge"}},
		{"a method named like a test's, not on a test", "Call `w.Run(ctx)` and o.Fatal().", []string{"Fatal", "Run"}},
	}
	for _, c := range cases {
		fp := TicketFootprint(Ticket{ID: "x", Description: c.text}, trackedHere, "")
		if !slices.Equal(fp.Funcs, c.want) {
			t.Errorf("%s: functions %q, want %q", c.name, fp.Funcs, c.want)
		}
	}
}

// A ticket naming a function waits for one running on the file it is in, which the loop reads
// from the checkout.
func TestTicketNamingAFunctionWaitsForItsFile(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.commitFiles("internal/dispatch/loop.go", "internal/dispatch/pick.go")
		for f, src := range map[string]string{
			"internal/dispatch/loop.go": "package dispatch\n\ntype Loop struct{}\n\nfunc (o *Loop) merge() {}\n",
			"internal/dispatch/pick.go": "package dispatch\n\nfunc pickNext() {}\n",
		} {
			if err := os.MkdirAll(filepath.Join(h.repo, filepath.Dir(f)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.repo, f), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		h.beads.add("A", "rework the loop", 1)
		h.beads.describe("A", "Split internal/dispatch/loop.go in two.")
		h.beads.add("B", "time the merge", 2)
		h.beads.describe("B", "Log how long `Loop.merge` takes.")
		h.beads.add("C", "pick faster", 3)
		h.beads.describe("C", "Speed up pickNext().")
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls, with C finished and a slot free
			return finishes("a.txt")(w)
		}))
		h.worker("B", v.runs("B", finishes("b.txt")))
		h.worker("C", v.runs("C", finishes("c.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := v.of("B"); slices.Contains(got, "A") {
			t.Errorf("B ran beside A")
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A", "C", "B"}) {
			t.Errorf("dispatched %v", got)
		}
		if log := h.logged(); !strings.Contains(log, "skipping B: touches internal/dispatch/loop.go, like running A") {
			t.Errorf("the skip wasn't logged:\n%s", log)
		}
	})
}
