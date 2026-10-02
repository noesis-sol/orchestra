package dispatch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// trackedWithCheck is trackedHere with a check script.
var trackedWithCheck = append(slices.Clone(trackedHere), "scripts/check.sh")

// The files the check command names are left out of the files a ticket's text names, and of its
// predicted files, whatever words the command wraps them in; its files metadata still lists them.
func TestTicketFootprintLeavesOutTheCheckCommandsFiles(t *testing.T) {
	text := Ticket{Description: "Say hello in internal/tui/run.go.", AcceptanceCriteria: "scripts/check.sh passes."}
	for _, check := range []string{"scripts/check.sh", "./scripts/check.sh -fast", "go vet ./... && sh scripts/check.sh",
		"bash check.sh"} {
		for _, tracked := range [][]string{trackedWithCheck, nil} {
			if check == "bash check.sh" && tracked == nil {
				continue // a bare name finds its folder only among the repository's files
			}
			if fp := TicketFootprint(text, tracked, check); !slices.Equal(fp.Files, []string{"internal/tui/run.go"}) {
				t.Errorf("check %q, tracked %v: files %q, want only internal/tui/run.go", check, tracked != nil, fp.Files)
			}
		}
	}
	for _, check := range []string{"", "make check", "go test ./..."} {
		if fp := TicketFootprint(text, trackedWithCheck, check); !slices.Contains(fp.Files, "scripts/check.sh") {
			t.Errorf("check %q doesn't name scripts/check.sh, yet it is left out: %q", check, fp.Files)
		}
	}

	only := Ticket{Title: "Say hello", AcceptanceCriteria: "`scripts/check.sh` passes"}
	if fp := TicketFootprint(only, trackedWithCheck, "scripts/check.sh"); !fp.Empty() {
		t.Errorf("a ticket naming only the check script: footprint %q, want empty", fp)
	}
	listed := only
	listed.Metadata = []byte(`{"files": "scripts/check.sh"}`)
	if fp := TicketFootprint(listed, trackedWithCheck, "scripts/check.sh"); !slices.Equal(fp.Files, []string{"scripts/check.sh"}) {
		t.Errorf("a ticket listing the check script: files %q", fp.Files)
	}
	predicted := only
	predicted.Metadata = []byte(`{"predicted_files": "scripts/check.sh,README.md"}`)
	if fp := TicketFootprint(predicted, trackedWithCheck, "scripts/check.sh"); !slices.Equal(fp.Files, []string{"README.md"}) || !fp.Predicted {
		t.Errorf("predicted files: %q (predicted %v), want README.md", fp.Files, fp.Predicted)
	}
}

// Tickets asking only for the check to pass don't share its script: they run side by side, while
// two tickets listing it in their files metadata still overlap.
func TestTicketsNamingTheCheckScriptRunSideBySide(t *testing.T) {
	t.Parallel()
	a := Footprint{Files: []string{"scripts/check.sh"}}
	if what := shared(a, a, nil); what != "scripts/check.sh" {
		t.Errorf("two tickets listing the check script share %q", what)
	}

	h := newHarness(t)
	script := filepath.Join(h.repo, "scripts", "check.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "check script")
	h.cfg.Check = "scripts/check.sh"
	h.cfg.Concurrency = 2
	h.beads.add("A", "say hello", 1)
	h.beads.describe("A", "Say hello. scripts/check.sh passes.")
	h.beads.add("B", "say goodbye", 2)
	h.beads.describe("B", "Say goodbye. Acceptance: `scripts/check.sh` passes.")
	h.worker("A", func(w *fakeWorker) AgentState {
		for deadline := time.Now().Add(patience); !h.sink.dispatchedYet("B"); time.Sleep(time.Millisecond) {
			if time.Now().After(deadline) {
				t.Error("B didn't run beside A")
				break
			}
		}
		return finishes("a.txt")(w)
	})
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	log := h.logged()
	if strings.Contains(log, "skipping") {
		t.Errorf("a ticket was skipped:\n%s", log)
	}
	for _, want := range []string{"A footprint: nothing named\n", "B footprint: nothing named\n", "'scripts/check.sh' passes"} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
}

// orchestra plan links no two tickets for the check script they ask to pass, and still links two
// that list it in their files metadata.
func TestPlanLinksLeaveOutTheCheckCommandsFiles(t *testing.T) {
	listing := func(tk Ticket) Ticket {
		tk.Metadata = []byte(`{"files": ["scripts/check.sh"]}`)
		return tk
	}
	open := []Ticket{
		planTicket("a", 1, "2026-09-01T00:00:00Z", "Say hello. scripts/check.sh passes."),
		planTicket("b", 2, "2026-09-01T00:00:00Z", "Say goodbye. `./scripts/check.sh` passes."),
		listing(planTicket("c", 3, "2026-09-01T00:00:00Z", "Make the check faster.")),
		listing(planTicket("d", 4, "2026-09-01T00:00:00Z", "Lint in the check.")),
	}
	got := linkList(PlanLinks(open, nil, trackedWithCheck, "scripts/check.sh", linesOf(50, nil)))
	if want := []string{"c>d:scripts/check.sh"}; !slices.Equal(got, want) {
		t.Errorf("links %q, want %q", got, want)
	}
	got = linkList(PlanLinks(open, nil, trackedWithCheck, "", linesOf(50, nil)))
	if want := []string{"a>b:scripts/check.sh", "b>c:scripts/check.sh", "c>d:scripts/check.sh"}; !slices.Equal(got, want) {
		t.Errorf("without a check command: links %q, want %q", got, want)
	}
}
