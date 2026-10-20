package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/load"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The environment ticket is the first ticket filed in the tree of the root epic, the first issue
// filed, past the epics under it and anything filed outside it, when it is labelled solo, has
// priority 0 and is open; otherwise there is none, and the watch looks no further.
func TestEnvWatchFindsTheEnvironmentTicket(t *testing.T) {
	t.Parallel()
	zero, one := 0, 1
	root := dispatch.Ticket{ID: "s-1", Status: dispatch.StatusOpen, IssueType: "epic", Priority: &one}
	part := dispatch.Ticket{ID: "s-1.1", Status: dispatch.StatusOpen, IssueType: "epic", Priority: &one, Parent: "s-1"}
	env := func(id, parent string, prio *int, labels ...string) dispatch.Ticket {
		return dispatch.Ticket{ID: id, Status: dispatch.StatusOpen, IssueType: "task", Priority: prio, Parent: parent,
			Labels: labels}
	}
	other := dispatch.Ticket{ID: "x-9", Status: dispatch.StatusOpen, IssueType: "bug", Priority: &zero,
		Labels: []string{dispatch.SoloLabel}}
	for _, tc := range []struct {
		name   string
		issues []dispatch.Ticket
		want   string // the environment ticket found; "" for none
	}{
		{"under the root epic", []dispatch.Ticket{root, env("s-1.2", "s-1", &zero, dispatch.SoloLabel)}, "s-1.2"},
		{"under a child epic", []dispatch.Ticket{root, part, env("s-1.1.1", "s-1.1", &zero, "x", dispatch.SoloLabel)},
			"s-1.1.1"},
		{"past an issue outside the tree", []dispatch.Ticket{root, other, env("s-1.2", "s-1", &zero, dispatch.SoloLabel)},
			"s-1.2"},
		{"not solo", []dispatch.Ticket{root, env("s-1.2", "s-1", &zero), env("s-1.3", "s-1", &zero, dispatch.SoloLabel)},
			""},
		{"priority 1", []dispatch.Ticket{root, env("s-1.2", "s-1", &one, dispatch.SoloLabel)}, ""},
		{"no priority", []dispatch.Ticket{root, env("s-1.2", "s-1", nil, dispatch.SoloLabel)}, ""},
		{"no root epic first", []dispatch.Ticket{env("s-1.2", "s-1", &zero, dispatch.SoloLabel)}, ""},
		{"a child epic first", []dispatch.Ticket{part, env("s-1.1.1", "s-1.1", &zero, dispatch.SoloLabel)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newEnvWatch(&specTree{issues: tc.issues})
			for _, i := range tc.issues { // one at a time, as filed
				w.filed(i.ID)
			}
			found := make(chan envStart, 1)
			ctx, cancel := context.WithTimeout(t.Context(), patience)
			defer cancel()
			w.watch(ctx, found)
			if ctx.Err() != nil {
				t.Fatal("the watch didn't decide")
			}
			got := ""
			select {
			case e := <-found:
				got = e.env.ID
				if e.root != "s-1" {
					t.Errorf("root %q", e.root)
				}
			default:
			}
			if got != tc.want {
				t.Errorf("found %q, want %q", got, tc.want)
			}
		})
	}
}

// The watch goes on as the breakdown files: issues filed after it started are looked at in turn,
// and bd failing to show one ends it.
func TestEnvWatchWaitsForWhatIsFiled(t *testing.T) {
	t.Parallel()
	zero := 0
	tree := &specTree{issues: []dispatch.Ticket{
		{ID: "s-1", Status: dispatch.StatusOpen, IssueType: "epic"},
		{ID: "s-1.1", Status: dispatch.StatusOpen, IssueType: "task", Priority: &zero, Parent: "s-1",
			Labels: []string{dispatch.SoloLabel}},
	}}
	w := newEnvWatch(tree)
	found := make(chan envStart, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.watch(t.Context(), found)
	}()
	w.filed("s-1")
	time.Sleep(10 * time.Millisecond)
	w.filed("s-1.1")
	select {
	case e := <-found:
		if e.root != "s-1" || e.env.ID != "s-1.1" {
			t.Errorf("found %+v", e)
		}
	case <-time.After(patience):
		t.Fatal("found nothing")
	}
	<-done

	w = newEnvWatch(&specTree{})
	w.filed("s-1")
	w.watch(t.Context(), found) // returns: bd has no s-1
	if len(found) != 0 {
		t.Error("found an environment ticket bd can't show")
	}
}

// sinkRecord keeps what a sink was given, in order.
type sinkRecord struct {
	mu  sync.Mutex
	got []string
}

func (s *sinkRecord) add(what string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, what)
}

func (s *sinkRecord) Event(ev dispatch.Event) { s.add("event " + ev.Text) }
func (s *sinkRecord) Status(st dispatch.Status) {
	s.add("status " + st.Ticket + " " + string(st.Agent))
}
func (s *sinkRecord) Load(load.Reading) { s.add("load") }

// The held sink prints each event as it comes and keeps it, with each status, for the dashboard,
// which gets them all in order once it is handed them, and from then on everything, the machine's load
// included; nothing more is printed.
func TestHeldSinkHandsTheDashboardWhatCameBefore(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	h := &heldSink{out: tui.Printer{Out: &out}}
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	h.Event(dispatch.Event{Kind: dispatch.EvInfo, Text: "START", Time: now})
	h.Status(dispatch.Status{Ticket: "s-1.1", Agent: "starting"})
	h.Load(load.Reading{})
	h.Event(dispatch.Event{Kind: dispatch.EvDispatch, Ticket: "s-1.1", Text: "[1] s-1.1 dispatching", Time: now})
	if want := "2026-10-20 09:00:00 START\n2026-10-20 09:00:00 [1] s-1.1 dispatching\n"; out.String() != want {
		t.Errorf("printed:\n%s\nwant:\n%s", out.String(), want)
	}
	var dash sinkRecord
	h.handTo(&dash)
	h.Status(dispatch.Status{Ticket: "s-1.1", Agent: "working"})
	h.Load(load.Reading{})
	h.Event(dispatch.Event{Kind: dispatch.EvInfo, Text: "later", Time: now})
	want := []string{"event START", "status s-1.1 starting", "event [1] s-1.1 dispatching", "status s-1.1 working",
		"load", "event later"}
	if !reflect.DeepEqual(dash.got, want) {
		t.Errorf("the dashboard got %q, want %q", dash.got, want)
	}
	if strings.Contains(out.String(), "later") {
		t.Errorf("printed once handed to the dashboard:\n%s", out.String())
	}
}

// The headless breakdown tells Filed the IDs of what its session files, in the order it files them.
func TestHeadlessBreakdownTellsWhatItFiles(t *testing.T) {
	t.Parallel()
	repo, tools := breakdownRepo(t), t.TempDir()
	faketool.Write(t, tools, "bd", filingBd)
	fakeSession(t, tools, "", fileTree(t)+answering(t, specAnswer{Root: "s-1"}))
	onPath(t, tools)
	var (
		out strings.Builder
		mu  sync.Mutex
		ids []string
	)
	r := breakdownRequest{Spec: testSpec, Filed: func(filed ...string) {
		mu.Lock()
		defer mu.Unlock()
		ids = append(ids, filed...)
	}}
	if _, err := specBreakdownIn(repo, &out).BreakDown(testCtx(context.Background(), t), r); err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(ids), "[s-1 s-1.1 s-1.1.1 s-1.2]"; got != want {
		t.Errorf("told %s, want %s", got, want)
	}
}
