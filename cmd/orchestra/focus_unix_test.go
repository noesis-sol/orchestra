//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The dashboard's focus callback returns while Herdr is still at it, and a failure ends up in the
// log. The fake herdr fails only once the test lets it go, after the callback has returned: a
// callback that waited for it would leave Herdr's own error out of the log.
func TestFocusTabRunsHerdrInTheBackgroundAndLogsAFailure(t *testing.T) {
	dir := t.TempDir()
	release := filepath.Join(dir, "release")
	script := "#!/bin/sh\nwhile [ ! -e '" + release + "' ]; do sleep 0.05; done\n" +
		`echo '{"error":{"code":"tab_not_found","message":"tab w1:t9 not found"}}' >&2` + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "herdr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "orchestra.log")
	log, err := dispatch.OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }() // the test reads the file itself

	focusTab(context.Background(), log)("w1:t9")
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		got := read(t, logPath)
		if strings.Contains(got, "cannot switch to tab w1:t9 from the dashboard") {
			if !strings.Contains(got, "tab w1:t9 not found") {
				t.Errorf("the log lacks Herdr's error:\n%s", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the failure was not logged:\n%s", got)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
