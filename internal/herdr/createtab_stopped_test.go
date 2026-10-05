package herdr

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// createCall is how CreateTab runs 'herdr tab create' in these tests, as a failed call's error names it.
const createCall = "herdr tab create --workspace w1 --cwd /tmp --label t-1 --no-focus"

// pressCtrlCOnceOpened cancels the context it returns once the fake herdr in dir has made the file
// opened, as its 'tab create' does once the tab is open.
func pressCtrlCOnceOpened(t *testing.T, dir string) context.Context {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(filepath.Join(dir, "opened")); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	return ctx
}

// A 'tab create' stopped by Ctrl+C, or failing without Herdr's own error, may have opened the tab
// all the same: CreateTab closes it, though the run is cancelled, and says what became of it.
func TestCreateTabClosesATabWhenTheCallFails(t *testing.T) {
	const earlier = "w1:t3 t-1"
	const hang = `: > "$d/opened"; exec sleep 30` + "\n"
	for _, c := range []struct {
		name     string
		after    string // what the fake herdr does once the tab is open
		nocreate bool   // it opens none
		ctrlC    bool   // Ctrl+C is pressed once it has
		want     string // the error
	}{
		{"stopped by Ctrl+C", hang, false, true,
			createCall + ": context canceled; tab w1:t10, which Herdr opened, is closed again"},
		{"stopped by Ctrl+C before Herdr opened it", hang, true, true,
			createCall + ": context canceled; Herdr has no new tab labelled t-1"},
		{"crashed", "echo \"thread 'main' panicked\" >&2; exit 101\n", false, false,
			createCall + ": exit status 101: thread 'main' panicked; tab w1:t10, which Herdr opened, is closed again"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.ctrlC && testing.Short() {
				t.Skip("skipped by -short: waits out the grace of the stopped 'tab create'")
			}
			dir := tabsHerdr(t, earlier)
			writeFile(t, dir, "after", c.after)
			if c.nocreate {
				writeFile(t, dir, "nocreate", "")
			}
			ctx := t.Context()
			if c.ctrlC {
				ctx = pressCtrlCOnceOpened(t, dir)
			}
			tab, pane, err := (Terminal{}).CreateTab(ctx, "w1", "/tmp", "t-1")
			if tab != "" || pane != "" || err == nil || err.Error() != c.want {
				t.Fatalf("got %q %q %v\nwant the error %s", tab, pane, err, c.want)
			}
			if c.ctrlC && !errors.Is(err, context.Canceled) {
				t.Errorf("error %v, want it to wrap the cancellation", err)
			}
			if got := openTabs(t, dir); got != earlier+"\n" {
				t.Errorf("open tabs:\n%s\nwant only the earlier one", got)
			}
		})
	}
}

// Herdr refusing 'tab create' opened no tab, and nor did a herdr that never started: CreateTab
// returns the call's error as it is, without looking for one.
func TestCreateTabLooksForNoTabHerdrDidNotOpen(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		dir := tabsHerdr(t)
		writeFile(t, dir, "nocreate", "")
		writeFile(t, dir, "after", `echo '{"error":{"code":"workspace_not_found","message":"no workspace w1"}}' >&2`+
			"\nexit 1\n")
		_, _, err := (Terminal{}).CreateTab(t.Context(), "w1", "/tmp", "t-1")
		if !HasCode(err, "workspace_not_found") || strings.Contains(err.Error(), ";") {
			t.Errorf("error %v, want Herdr's own alone", err)
		}
		calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
		if want := "tab list --workspace w1\n" + strings.TrimPrefix(createCall, "herdr ") + "\n"; string(calls) != want {
			t.Errorf("ran herdr:\n%s\nwant:\n%s", calls, want)
		}
	})
	t.Run("not started", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // no herdr on it
		_, _, err := (Terminal{}).CreateTab(t.Context(), "w1", "/tmp", "t-1")
		if !errors.Is(err, exec.ErrNotFound) || strings.Contains(err.Error(), ";") {
			t.Errorf("error %v, want the start's alone", err)
		}
	})
}
