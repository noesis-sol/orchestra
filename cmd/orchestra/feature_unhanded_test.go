//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// filedUnder is newFeatureTree with the feature's issues, f-1 and everything under it, carrying the
// feature run run in their metadata, as the session files them, and their parents; x-1 carries
// another run's.
func filedUnder(run string) *featureTree {
	tree := newFeatureTree()
	for parent, ids := range tree.children {
		for _, id := range ids {
			t := tree.issues[id]
			t.Parent = parent
			tree.issues[id] = t
		}
	}
	for id, t := range tree.issues {
		meta := map[string]any{}
		if len(t.Metadata) > 0 {
			if err := json.Unmarshal(t.Metadata, &meta); err != nil {
				panic(err)
			}
		}
		meta[project.FeatureRunKey] = run
		if id == "x-1" {
			meta[project.FeatureRunKey] = "20261016T080000Z"
		}
		t.Metadata, _ = json.Marshal(meta)
		tree.issues[id] = t
	}
	return tree
}

// The feature run is the run's start in UTC, to the second, and the session's first message names
// it before the file with the description.
func TestFeatureRunNamesTheRunsStart(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 17, 11, 30, 5, 0, time.FixedZone("CEST", 2*3600))
	if got := featureRun(started); got != paneRun {
		t.Errorf("featureRun = %q, want %q", got, paneRun)
	}
	if got, want := featureMessage(paneRun, ".orchestra/run/feature-request.md"), "Here is the feature I'd like "+
		"to talk through (feature run 20261017T093005Z): @.orchestra/run/feature-request.md"; got != want {
		t.Errorf("featureMessage = %q, want %q", got, want)
	}
}

// A session left without handing the epic back, having filed the feature all the same, runs nothing
// and exits 0, but says what it filed under its feature run, and not another run's, and how to start
// the run on its epic. With nothing filed under the run it says nothing was filed; when bd can't
// list it, it says so.
func TestFeatureNotHandedBackSaysWhatWasFiled(t *testing.T) {
	t.Parallel()
	repo := featureRepo(t)
	for _, tc := range []struct {
		name      string
		tree      *featureTree
		out, errs string
	}{
		{"filed", filedUnder(paneRun), "The interview ended without handing the feature back. It filed 2 epics " +
			"and 3 tickets:\n" +
			"  f-1  epic  Export\n" +
			"  f-1.1  task  Add the encoder\n" +
			"  f-1.2  task  Add the flag\n" +
			"  f-1.3  epic  The CSV part\n" +
			"  f-1.3.1  feature  Add CSV\n" +
			"Start the run on it with: orchestra --ticket f-1\n", ""},
		{"nothing filed", newFeatureTree(), noFeature + "\n", ""},
		{"bd fails", func() *featureTree {
			tree := filedUnder(paneRun)
			tree.fail[project.FeatureRunKey] = true
			return tree
		}(), "", "orchestra: the interview handed no feature back, and orchestra can't list what it filed: " +
			"bd list --json: exit status 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := talkFeatureThrough(t, repo, tc.tree, "")
			if r.epic != "" || r.code != dispatch.ExitOK || r.out != tc.out || r.err != tc.errs {
				t.Errorf("got %q, exit %d; stdout:\n%s\nwant:\n%s\nstderr:\n%s", r.epic, r.code, r.out, tc.out, r.err)
			}
		})
	}
}

// A stop during the session, before it handed the epic back, names the epic it filed under its
// feature run, as bd has it, and how to start the run on it.
func TestFeatureStoppedBeforeHandingBackNamesItsEpic(t *testing.T) {
	t.Parallel()
	repo := featureRepo(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out, errOut strings.Builder
	var message string
	b := refineBreakdown{repo: repo, out: &out, err: &errOut,
		terminal: func(_ context.Context, m string, _ project.RefineMode) error {
			message = m
			cancel()
			return context.Canceled
		}}
	epic, code := featureTalk{request: paneRequest, runID: paneRun, repo: repo, breakdown: b,
		tracker: filedUnder(paneRun), out: &out, err: &errOut}.run(ctx)
	if want := "orchestra: stopped before the run started.\nThe interview filed f-1; start the run on it with: " +
		"orchestra --ticket f-1\n"; epic != "" || code != dispatch.ExitInterrupted || errOut.String() != want {
		t.Errorf("got %q, exit %d; stderr:\n%s\nwant:\n%s", epic, code, errOut.String(), want)
	}
	if message != paneMessage {
		t.Errorf("the session's first message is %q, want %q", message, paneMessage)
	}
}
