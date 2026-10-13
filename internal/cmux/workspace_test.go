package cmux

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A worker's workspace opens named with its ticket's ID, in the folder given, without the focus; it
// and its terminal are kept in the book by their UUIDs.
func TestCreateTabOpensANamedWorkspace(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	f.answer("workspace", `{"group_id": null, "surface_id": "`+strings.ToLower(workSF)+`", "window_id": "`+
		"22222222-2222-4222-8222-222222222222"+`", "workspace_id": "`+workWS+`"}`)
	tab, pane, err := term.CreateTab(bg, "ignored", "/work/x-1", "x-1")
	if err != nil || tab != workWS || pane != workSF {
		t.Fatalf("got %q %q %v", tab, pane, err)
	}
	want := []string{"--id-format", "uuids", "--json", "workspace", "create", "--name", "x-1", "--cwd", "/work/x-1",
		"--focus", "false"}
	if got := f.call("workspace"); !slices.Equal(got, want) {
		t.Errorf("cmux got %q, want %q", got, want)
	}
	b, err := term.view()
	if err != nil {
		t.Fatal(err)
	}
	if b.Workspaces[workWS].Label != "x-1" || b.Surfaces[workSF] != (surfaceEntry{Workspace: workWS, Dir: "/work/x-1"}) {
		t.Errorf("the book has %+v", b)
	}
}

func TestCreateTabClosesAWorkspaceItCannotUse(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	f.answer("workspace", `{"workspace_id": "`+workWS+`"}`)
	f.answerNth("tree", 1, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{workWS, "x-1", []string{workSF}}))
	f.answerNth("tree", 2, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	_, _, err := term.CreateTab(bg, "", "/w", "x-1")
	if err == nil || !strings.Contains(err.Error(), "which cmux opened, is closed again") {
		t.Errorf("an answer naming the workspace but no terminal: %v", err)
	}
	if got := f.call("close-workspace"); !slices.Equal(cmdOf(got), []string{"close-workspace", "--workspace", workWS}) {
		t.Errorf("close-workspace got %q", got)
	}

	f = newFakeCmux(t)
	f.answer("workspace", "garbled")
	if _, _, err := term.CreateTab(bg, "", "/w", "x-1"); err == nil || !strings.Contains(err.Error(), "may be left open") {
		t.Errorf("an answer naming no workspace: %v", err)
	}
	if slices.Contains(f.ops(), "close-workspace") {
		t.Error("no workspace to close")
	}
}

// bookOf returns a terminal whose book holds the worker's workspace, labelled x-1, and its terminal.
func bookOf(t *testing.T) Terminal {
	term := terminal(t)
	keep(t, term, book{Workspaces: map[string]workspaceEntry{workWS: {Label: "x-1"}},
		Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS, Dir: t.TempDir()}}})
	return term
}

// Closing a worker's workspace closes it by its UUID, checks that it went and every other stayed,
// and drops it from the book.
func TestCloseTabClosesItsWorkspaceOnly(t *testing.T) {
	f := newFakeCmux(t)
	term := bookOf(t)
	f.answerNth("tree", 1, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{userWS, "mine", nil},
		ws{workWS, "x-1", []string{workSF}}))
	f.answerNth("tree", 2, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{userWS, "mine", nil}))
	if err := term.CloseTab(bg, strings.ToLower(workWS)); err != nil {
		t.Fatal(err)
	}
	if got := cmdOf(f.call("close-workspace")); !slices.Equal(got, []string{"close-workspace", "--workspace",
		strings.ToLower(workWS)}) {
		t.Errorf("close-workspace got %q", got)
	}
	if b, _ := term.view(); len(b.Workspaces) != 0 || len(b.Surfaces) != 0 {
		t.Errorf("the book still has %+v", b)
	}
}

// A workspace renamed since, or not orchestra's, or orchestra's own, is never closed; one already
// gone is no error.
func TestCloseTabLeavesOtherWorkspacesOpen(t *testing.T) {
	f := newFakeCmux(t)
	term := bookOf(t)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{workWS, "my notes", []string{workSF}},
		ws{userWS, "x-2", nil}))
	if err := term.CloseTab(bg, workWS); err == nil || !strings.Contains(err.Error(), `named "my notes" now`) {
		t.Errorf("a renamed workspace: %v", err)
	}
	if err := term.CloseTab(bg, userWS); err == nil || !strings.Contains(err.Error(), "didn't open") {
		t.Errorf("a workspace of the user's: %v", err)
	}
	if err := term.CloseTab(bg, "workspace:2"); !Missing(err) {
		t.Error("a short ref closed")
	}
	keep(t, term, book{Workspaces: map[string]workspaceEntry{ownWS: {Label: "orchestra"}}})
	if err := term.CloseTab(bg, ownWS); err == nil || !strings.Contains(err.Error(), "orchestra's own") {
		t.Errorf("orchestra's own workspace: %v", err)
	}
	if slices.Contains(f.ops(), "close-workspace") {
		t.Errorf("cmux closed a workspace: %v", f.ops())
	}

	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	term = bookOf(t)
	if err := term.CloseTab(bg, workWS); err != nil {
		t.Errorf("a workspace already gone: %v", err)
	}
	if err := term.CloseTab(bg, userWS); err != nil {
		t.Errorf("a workspace not orchestra's, gone: %v", err)
	}
	if slices.Contains(f.ops(), "close-workspace") {
		t.Errorf("cmux closed a workspace: %v", f.ops())
	}
}

// cmux closing another workspace than the one asked for (manaflow-ai/cmux#12524), or none, is an
// error naming it.
func TestCloseTabChecksWhatCmuxClosed(t *testing.T) {
	f := newFakeCmux(t)
	term := bookOf(t)
	f.answerNth("tree", 1, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{workWS, "x-1", []string{workSF}}))
	f.answerNth("tree", 2, treeJSON(ws{workWS, "x-1", []string{workSF}}))
	if err := term.CloseTab(bg, workWS); err == nil || !strings.Contains(err.Error(), "closed other workspaces too: "+
		ownWS+" (orchestra)") {
		t.Errorf("the wrong workspace closed: %v", err)
	}

	f = newFakeCmux(t)
	f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
	if err := term.CloseTab(bg, workWS); err == nil || !strings.Contains(err.Error(), "still has the workspace") {
		t.Errorf("a workspace that stayed: %v", err)
	}
	f.fail("close-workspace", "Error: protected: Pinned workspaces can't be closed")
	if err := term.CloseTab(bg, workWS); err == nil || !strings.Contains(err.Error(), "protected") {
		t.Errorf("a refused close: %v", err)
	}
	if b, _ := term.view(); b.Workspaces[workWS].Label != "x-1" {
		t.Error("a workspace still open was dropped from the book")
	}
}

func TestTabLabel(t *testing.T) {
	f := newFakeCmux(t)
	f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
	term := terminal(t)
	if label, open, err := term.TabLabel(bg, strings.ToLower(workWS)); label != "x-1" || !open || err != nil {
		t.Errorf("an open workspace: %q %v %v", label, open, err)
	}
	if _, open, err := term.TabLabel(bg, userWS); open || err != nil {
		t.Errorf("a workspace cmux doesn't have: %v %v", open, err)
	}
	if _, open, err := term.TabLabel(bg, "workspace:1"); open || err != nil {
		t.Errorf("a short ref: %v %v", open, err)
	}
	f.answer("tree", "garbled")
	if _, _, err := term.TabLabel(bg, workWS); err == nil {
		t.Error("an answer that can't be read")
	}
}

// cmux not running (its socket missing, or refusing connections) has no workspaces, which is no
// error; cmux refusing orchestra while running, or failing otherwise, is.
func TestCmuxNotRunning(t *testing.T) {
	f := newFakeCmux(t)
	f.fail("tree", "Error: Socket not found at "+os.Getenv("CMUX_SOCKET_PATH"))
	f.fail("ping", "Error: Socket not found at "+os.Getenv("CMUX_SOCKET_PATH"))
	term := terminal(t)
	if _, open, err := term.TabLabel(bg, workWS); open || err != nil {
		t.Errorf("no socket: %v %v", open, err)
	}
	if err := term.Ping(bg); !NoServer(err) {
		t.Errorf("ping without a socket: %v", err)
	}

	f.serve()
	f.fail("tree", "Error: ERROR: Access denied - only processes started inside cmux can connect")
	if _, _, err := term.TabLabel(bg, workWS); err == nil || NoServer(err) ||
		!strings.Contains(err.Error(), "Access denied") {
		t.Errorf("access denied: %v", err)
	}
}

// Without cmux on the PATH or the app, every call fails saying so.
func TestCmuxMissing(t *testing.T) {
	newFakeCmux(t)
	t.Setenv("PATH", t.TempDir())
	if Bin() != "" {
		t.Fatalf("found %s", Bin())
	}
	if err := (Terminal{}).Ping(bg); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("ping: %v", err)
	}
	app := filepath.Join(t.TempDir(), "cmux")
	if err := os.WriteFile(app, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	appBin = app
	if Bin() != app {
		t.Errorf("the app's cmux: %q", Bin())
	}
}

func TestFocusTabSelectsTheWorkspace(t *testing.T) {
	f := newFakeCmux(t)
	if err := terminal(t).FocusTab(bg, workWS); err != nil {
		t.Fatal(err)
	}
	if got := cmdOf(f.call("select-workspace")); !slices.Equal(got, []string{"select-workspace", "--workspace", workWS}) {
		t.Errorf("select-workspace got %q", got)
	}
	if err := terminal(t).FocusTab(bg, "workspace:3"); !Missing(err) {
		t.Errorf("a short ref: %v", err)
	}
}

// The interview's terminal is split off orchestra's, in orchestra's workspace, with the focus; it
// is closed by its UUID within that workspace, and cmux closing another terminal too is an error.
func TestSplitAndClosePane(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	f.answer("new-split", `{"pane_id": "33333333-3333-4333-8333-333333333333", "surface_id": "`+splitSF+
		`", "type": "terminal", "workspace_id": "`+ownWS+`"}`)
	split, err := term.SplitPane(bg, ownSF, "/repo")
	if err != nil || split != splitSF {
		t.Fatalf("got %q %v", split, err)
	}
	if got := f.call("new-split"); !slices.Equal(got, []string{"--id-format", "uuids", "--json", "new-split", "right",
		"--workspace", ownWS, "--surface", ownSF, "--focus", "true"}) {
		t.Errorf("new-split got %q", got)
	}
	if b, _ := term.view(); b.Surfaces[splitSF] != (surfaceEntry{Workspace: ownWS, Dir: "/repo", Split: true}) {
		t.Errorf("the book has %+v", b.Surfaces)
	}

	f = newFakeCmux(t)
	f.answerNth("tree", 1, treeJSON(ws{ownWS, "orchestra", []string{ownSF, splitSF}}))
	f.answerNth("tree", 2, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	if err := term.ClosePane(bg, splitSF); err != nil {
		t.Fatal(err)
	}
	if got := cmdOf(f.call("close-surface")); !slices.Equal(got, []string{"close-surface", "--workspace", ownWS,
		"--surface", splitSF}) {
		t.Errorf("close-surface got %q", got)
	}
	if err := term.ClosePane(bg, ownSF); err == nil || !strings.Contains(err.Error(), "didn't split") {
		t.Errorf("orchestra's own terminal: %v", err)
	}

	keep(t, term, book{Surfaces: map[string]surfaceEntry{splitSF: {Workspace: ownWS, Split: true}}})
	f = newFakeCmux(t)
	f.answerNth("tree", 1, treeJSON(ws{ownWS, "orchestra", []string{ownSF, splitSF}}))
	f.answerNth("tree", 2, treeJSON(ws{ownWS, "orchestra", []string{splitSF}}))
	if err := term.ClosePane(bg, splitSF); err == nil || !strings.Contains(err.Error(), "closed other terminals too: "+ownSF) {
		t.Errorf("the wrong terminal closed: %v", err)
	}

	f = newFakeCmux(t)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	if err := term.ClosePane(bg, splitSF); err != nil || slices.Contains(f.ops(), "close-surface") {
		t.Errorf("a terminal already gone: %v, %v", err, f.ops())
	}
	if open, err := term.PaneOpen(bg, splitSF); open || err != nil {
		t.Errorf("PaneOpen: %v %v", open, err)
	}
	if open, err := term.PaneOpen(bg, ownSF); !open || err != nil {
		t.Errorf("PaneOpen of orchestra's: %v %v", open, err)
	}
}
