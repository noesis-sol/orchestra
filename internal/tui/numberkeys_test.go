package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// numberedDashboard is a dashboard with n workers running, k-01 the oldest, recording the tabs it
// asks to focus. Each worker's tab is t<its number>, but k-03's: it has none.
func numberedDashboard(n int, focused *[]string) Dashboard {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: n}, func() {}, func(bool) {},
		func(tab string) { *focused = append(*focused, tab) })
	m.width, m.height = 80, 60
	m.active = map[string]dispatch.Status{}
	now := time.Now()
	for i := 1; i <= n; i++ {
		id, tab := fmt.Sprintf("k-%02d", i), fmt.Sprintf("t%d", i)
		if i == 3 {
			tab = ""
		}
		m.active[id] = dispatch.Status{Ticket: id, Title: "Ticket " + id, Tab: tab,
			Started: now.Add(-time.Duration(n-i) * time.Minute), Agent: "working"}
	}
	return m
}

// Pressing a worker's number focuses its tab; not a number with no worker, one beyond the ninth,
// nor a worker without a tab.
func TestNumberKeysFocusTheWorkersTabs(t *testing.T) {
	var focused []string
	press(numberedDashboard(11, &focused), "2", "1", "3", "9", "0", "a")
	if want := []string{"t2", "t1", "t9"}; strings.Join(focused, " ") != strings.Join(want, " ") {
		t.Errorf("focused %v, want %v", focused, want)
	}

	focused = nil
	press(numberedDashboard(2, &focused), "3", "5")
	if len(focused) != 0 {
		t.Errorf("numbers with no worker focused %v", focused)
	}
}

// The numbers follow the list as it stands: when a worker finishes, the next one takes its number.
func TestNumberKeysFollowTheRunningWorkers(t *testing.T) {
	var focused []string
	m := numberedDashboard(2, &focused)
	next, _ := m.Update(statusMsg(dispatch.Status{Ticket: "k-01", Gone: true}))
	press(next.(Dashboard), "1", "2")
	if strings.Join(focused, " ") != "t2" {
		t.Errorf("focused %v, want [t2]", focused)
	}
}

// While the stop question is open the numbers do nothing, and afterwards they work again.
func TestNumberKeysWaitForTheStopQuestion(t *testing.T) {
	var focused []string
	m := press(numberedDashboard(2, &focused), "s", "1", "2")
	if len(focused) != 0 || !m.asking {
		t.Errorf("focused %v with the question open (asking %v)", focused, m.asking)
	}
	if v := ansi.Strip(m.hintLine(80)); strings.Contains(v, "worker") {
		t.Errorf("the hint offers the numbers with the question open: %q", v)
	}
	press(m, "n", "1")
	if strings.Join(focused, " ") != "t1" {
		t.Errorf("focused %v after the question, want [t1]", focused)
	}
}

// Each running worker's line starts with its number, in the boxes and in the one-line list; the
// tenth and later get none, but keep their line in line with the others.
func TestWorkersShowTheirNumbers(t *testing.T) {
	var focused []string
	m := numberedDashboard(10, &focused)
	numbered := regexp.MustCompile(`│ (\d+)? +\S+ +(k-\d+)  working`)
	for name, view := range map[string]string{"boxes": m.workerPanels(80), "list": m.workerList(80)} {
		var got []string
		heads := map[int]bool{}
		for l := range strings.SplitSeq(ansi.Strip(view), "\n") {
			if s := numbered.FindStringSubmatch(l); s != nil {
				got = append(got, s[1]+" "+s[2])
				heads[strings.Index(l, "k-")] = true
			}
		}
		want := "1 k-01,2 k-02,3 k-03,4 k-04,5 k-05,6 k-06,7 k-07,8 k-08,9 k-09, k-10"
		if strings.Join(got, ",") != want {
			t.Errorf("%s: numbered %q, want %q:\n%s", name, strings.Join(got, ","), want, ansi.Strip(view))
		}
		if len(heads) != 1 {
			t.Errorf("%s: the ticket IDs start in %d columns:\n%s", name, len(heads), ansi.Strip(view))
		}
	}
}

// The hint names the numbers there are, and keeps them as long as it can in a narrow pane: the short
// form first, then s alone.
func TestHintNamesTheNumberKeys(t *testing.T) {
	var focused []string
	cases := []struct {
		workers, w int
		want       string
	}{
		{4, 80, "  1–4 to go to a worker's tab · s to stop after the current tickets"},
		{1, 80, "  1 to go to the worker's tab · s to stop after the current tickets"},
		{12, 80, "  1–9 to go to a worker's tab · s to stop after the current tickets"},
		{4, 67, "  1–4 to go to a worker's tab · s to stop after the current tickets"},
		{4, 66, " 1–4 worker tab · s stop after current"},
		{4, 38, " 1–4 worker tab · s stop after current"},
		{4, 37, "  s to stop after the current tickets"},
		{4, 36, " s stop after current"},
		{4, 20, " s stop after curre…"},
		{0, 80, "  s to stop after the current tickets"},
	}
	for _, c := range cases {
		m := numberedDashboard(c.workers, &focused)
		if got := ansi.Strip(m.hintLine(c.w)); got != c.want {
			t.Errorf("%d workers, %d wide: %q, want %q", c.workers, c.w, got, c.want)
		}
	}
	m := numberedDashboard(2, &focused)
	m.stopping = true
	if got := ansi.Strip(m.hintLine(80)); got != "  1–2 to go to a worker's tab" {
		t.Errorf("stopping: %q", got)
	}
	if got := ansi.Strip(m.hintLine(20)); got != " 1–2 worker tab" {
		t.Errorf("stopping, 20 wide: %q", got)
	}
	m.stopping, m.draining = false, true
	if got := ansi.Strip(m.hintLine(80)); got != "  1–2 to go to a worker's tab · s to keep taking tickets" {
		t.Errorf("winding down: %q", got)
	}
	if got := ansi.Strip(m.hintLine(42)); got != " 1–2 worker tab · s keep going" {
		t.Errorf("winding down, 42 wide: %q", got)
	}
	if got := ansi.Strip(m.hintLine(29)); got != "  s to keep taking tickets" {
		t.Errorf("winding down, 29 wide: %q", got)
	}
}
