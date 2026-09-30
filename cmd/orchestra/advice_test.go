package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/organ"
)

func TestSetAsideKeepsOrderWithoutRepeats(t *testing.T) {
	o := &Orch{}
	for _, id := range []string{"a", "b", "a", "c"} {
		o.markAside(id)
	}
	if got := o.setAside(); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("got %v", got)
	}
}

func TestLastLines(t *testing.T) {
	if got := lastLines("1\n2\n3\n4\n", 2); got != "3\n4" {
		t.Errorf("got %q", got)
	}
}

func TestTriageLineIsPurpleDiamondWithCause(t *testing.T) {
	at := time.Date(2026, 9, 28, 17, 0, 0, 0, time.Local)
	line := ansi.Strip(renderEvent(Event{Time: at, Kind: EvTriage, Ticket: "kinieta-jqm", Detail: "environment · high", Title: "visionOS runtime missing"}))
	if line != "17:00:00 ◆ kinieta-jqm triage: environment · high  visionOS runtime missing" {
		t.Errorf("line = %q", line)
	}
}

// TestLiveOrgans calls the real claude on a real repository without writing anything:
//
//	ORGAN_LIVE=1 LIVE_REPO=~/Projects/kinieta LIVE_BASE=<branch> LIVE_START=<commit> \
//	LIVE_TICKET=<deferred id> LIVE_WT=<its worktree> go test -run TestLiveOrgans -v
func TestLiveOrgans(t *testing.T) {
	if os.Getenv("ORGAN_LIVE") != "1" {
		t.Skip("set ORGAN_LIVE=1 to call the real claude")
	}
	repo, id := os.Getenv("LIVE_REPO"), os.Getenv("LIVE_TICKET")
	o := &Orch{cfg: Config{Repo: repo, Base: os.Getenv("LIVE_BASE")}, organ: organ.Client{Bin: "claude"},
		startHead: os.Getenv("LIVE_START"), started: time.Now().Add(-time.Hour), log: &Logger{}}
	b, _ := os.ReadFile(filepath.Join(repo, ".claude", "orchestrate.log"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], " START orchestra") {
			o.log.lines = lines[i:]
			break
		}
	}

	d := o.gatherDeferral(id, "", "the worker deferred it", os.Getenv("LIVE_WT"))
	start := time.Now()
	tr, err := o.organ.Triage(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("triage (%s):\n%s", time.Since(start).Round(time.Second), tr.Note())

	o.markAside(id)
	start = time.Now()
	report, err := o.organ.Review(context.Background(), o.reviewInput(exitOK, "(live test: the run is still going)"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("report (%s):\n%s", time.Since(start).Round(time.Second), report)
}
