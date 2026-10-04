//go:build unix

package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run that learns its feature once it holds the lock says so from then on: to RunHolder, to a
// second run refused, and in the file, which holds the new details alone, shorter ones included.
func TestRunLockRewrite(t *testing.T) {
	repo, _ := gitRepo(t)
	plain := Holder{PID: 44497, Started: time.Now().Truncate(time.Second), Version: "v1.2.3", Branch: "main",
		Pane: "w2B:p60"}
	l := lockedBy(t, repo, plain)

	feature := plain
	feature.Feature = "Add a --json flag\nto the list command"
	if err := l.Rewrite(feature); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	if h, ok, err := RunHolder(t.Context(), repo); err != nil || !ok || !sameHolder(h, feature) {
		t.Errorf("RunHolder after Rewrite: %+v %v %v, want %+v", h, ok, err, feature)
	}
	_, err := LockRun(t.Context(), repo, Holder{PID: 2})
	var held *HeldError
	if !errors.As(err, &held) || !sameHolder(held.Holder, feature) {
		t.Fatalf("second LockRun: %v, want a *HeldError naming %+v", err, feature)
	}
	if want := `--feature "Add a --json flag to the list command"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q lacks %s", err, want)
	}

	// Back to fewer details: nothing of the longer ones is left after them.
	if err := l.Rewrite(plain); err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(repo, ".git", LockName))
	if err != nil {
		t.Fatal(err)
	}
	var h Holder
	if err := json.Unmarshal(b, &h); err != nil || !sameHolder(h, plain) {
		t.Errorf("the lock's file after a shorter Rewrite: %+v %v\n%s", h, err, b)
	}
}

// A run without a lock (no flock on its system) has nothing to rewrite.
func TestNilRunLockRewritesNothing(t *testing.T) {
	var l *RunLock
	if err := l.Rewrite(Holder{PID: 1, Feature: "Add a --json flag"}); err != nil {
		t.Errorf("Rewrite: %v", err)
	}
}
