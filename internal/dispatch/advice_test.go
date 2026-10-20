package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/organ"
)

func TestSetAsideKeepsOrderWithoutRepeats(t *testing.T) {
	t.Parallel()
	o := New(Config{}, nil, "", Deps{})
	for _, id := range []string{"a", "b", "a", "c"} {
		o.book.setAside(id)
	}
	if got := o.asideList(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v", got)
	}
}

func TestLastLines(t *testing.T) {
	t.Parallel()
	if got := lastLines("1\n2\n3\n4\n", 2); got != "3\n4" {
		t.Errorf("got %q", got)
	}
}

// TestLiveOrgans calls the real claude on a real repository without writing anything:
//
//	ORGAN_LIVE=1 LIVE_REPO=~/Projects/kinieta LIVE_BASE=<branch> LIVE_START=<commit> \
//	LIVE_TICKET=<deferred id> LIVE_WT=<its worktree> go test -run TestLiveOrgans -v
func TestLiveOrgans(t *testing.T) {
	t.Parallel()
	if os.Getenv("ORGAN_LIVE") != "1" {
		t.Skip("set ORGAN_LIVE=1 to call the real claude")
	}
	repo, id := os.Getenv("LIVE_REPO"), os.Getenv("LIVE_TICKET")
	if id == "" {
		t.Skip("set LIVE_TICKET to the deferred ticket to triage")
	}
	o := New(Config{Repo: repo, Base: os.Getenv("LIVE_BASE")}, &Log{}, "",
		Deps{Tickets: liveTickets{repo}, Advisor: organ.Client{Bin: "claude"}})
	o.startHead, o.started = os.Getenv("LIVE_START"), time.Now().Add(-time.Hour)
	b, _ := os.ReadFile(filepath.Join(repo, ".claude", "orchestrate.log"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i, line := range slices.Backward(lines) {
		if strings.Contains(line, " START orchestra") {
			o.log.lines = lines[i:]
			break
		}
	}

	d := o.gatherDeferral(context.Background(), id, "the worker deferred it", os.Getenv("LIVE_WT"))
	start := time.Now()
	tr, err := o.organ.Triage(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("triage (%s):\n%s", time.Since(start).Round(time.Second), tr.Note())

	o.book.setAside(id)
	start = time.Now()
	report, err := o.organ.Review(context.Background(), o.reviewInput(context.Background(), ExitOK, "(live test: the run is still going)"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report (%s):\n%s", time.Since(start).Round(time.Second), report)
}

// liveTickets reads Beads for TestLiveOrgans (the beads adapter imports this package, so the test
// can't use it).
type liveTickets struct{ repo string }

func (l liveTickets) Ready(context.Context, string) ([]Ticket, error)       { return nil, nil }
func (l liveTickets) Unclosed(ctx context.Context) ([]Ticket, error)        { return nil, nil }
func (l liveTickets) Descendants(context.Context, string) ([]Ticket, error) { return nil, nil }
func (l liveTickets) Show(ctx context.Context, id string) (Ticket, error)   { return Ticket{ID: id}, nil }
func (l liveTickets) Status(ctx context.Context, id string) (TicketStatus, error) {
	return "unknown", nil
}
func (l liveTickets) Closed(ctx context.Context, label string) ([]Ticket, error) { return nil, nil }
func (l liveTickets) Describe(ctx context.Context, id string) string {
	out, _ := command.Output(context.Background(), 0, l.repo, "bd", "show", id)
	return out
}

func TestSaveReportNamesTheFileAfterTheRunStart(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "reports")
	o := &Loop{cfg: Config{ReportsDir: dir}, started: time.Date(2026, 9, 30, 14, 5, 9, 0, time.Local)}
	path, err := o.SaveReport("# report\n")
	if err != nil || path != filepath.Join(dir, "2026-09-30-140509.md") {
		t.Fatalf("saved to %q: %v", path, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "# report\n" {
		t.Errorf("saved %q", b)
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	o.cfg.ReportsDir = filepath.Join(blocked, "reports")
	if path, err := o.SaveReport("# report\n"); err == nil || path != "" {
		t.Errorf("reports folder under a file: saved to %q, err %v", path, err)
	}
}

func TestTriageQueuedAfterFinishIsDropped(t *testing.T) {
	t.Parallel()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{}, log, "", Deps{Advisor: organ.Client{Bin: filepath.Join(t.TempDir(), "no-claude")},
		AdviceCtx: context.Background()})
	sink := &recordSink{}
	o.sink = sink
	o.queueTriage(context.Background(), organ.Deferral{ID: "before-start"}) // triage off: nothing happens
	o.StartTriage()
	o.queueTriage(context.Background(), organ.Deferral{ID: "A"})
	o.FinishTriage(context.Background())
	o.queueTriage(context.Background(), organ.Deferral{ID: "B"})
	o.FinishTriage(context.Background()) // a second call returns too
	if !sink.warned("TRIAGE_FAILED", "A") || len(sink.ofTicket("B")) > 0 || len(sink.ofTicket("before-start")) > 0 {
		t.Errorf("A should be triaged (and fail, without claude), B and before-start dropped:\n%s", sink.text())
	}
	if len(o.triageQ) != 0 {
		t.Errorf("queue = %v", o.triageQ)
	}
}
