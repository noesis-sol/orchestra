package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/update"
)

// latestRelease makes the test's orchestra v (buildOf) and serves, in GitHub's place, a latest
// release tagged tag, until the test ends. It returns how many times it was asked.
func latestRelease(t *testing.T, v, tag string) *atomic.Int32 {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "html_url": "https://example.com/" + tag})
	}))
	t.Cleanup(srv.Close)
	buildOf(t, v, update.Updater{API: srv.URL})
	return &asked
}

// cacheIn gives the test a user cache folder of its own, in its environment, and returns the check's
// cache folder in it.
func cacheIn(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	setenv(t, "HOME", home)
	setenv(t, "XDG_CACHE_HOME", home+"/.cache")
	dir, err := update.CacheDir(getenv(t))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// cacheRelease writes the cache in dir as a check an hour ago that found latest.
func cacheRelease(t *testing.T, dir, latest string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"latest":"` + latest + `","checked":"` + hourAgo() + `"}`
	if err := os.WriteFile(filepath.Join(dir, update.CacheName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hourAgo is the time an hour ago, as the cache writes it.
func hourAgo() string { return time.Now().Add(-time.Hour).Format(time.RFC3339Nano) }

// Without a terminal, on stdout for the dashboard or on stderr, a newer release found goes unsaid.
func TestNewerReleaseUnsaidOffATerminal(t *testing.T) {
	t.Parallel()
	latestRelease(t, "v0.5.1", "v0.6.0")
	cacheRelease(t, cacheIn(t), "v0.6.0")
	for _, dashboard := range []bool{true, false} {
		var stdout, stderr strings.Builder
		e := testCmdEnv(t, nil, &stdout, &stderr)
		check := startUpdateCheck(context.Background(), e)
		if check == nil {
			t.Fatal("no check started")
		}
		showNewer(check, dashboard, e)
		if stdout.Len() > 0 || stderr.Len() > 0 {
			t.Errorf("dashboard %v: stdout %q, stderr %q, want nothing", dashboard, stdout.String(), stderr.String())
		}
	}
}

// ORCHESTRA_NO_UPDATE_CHECK=1 starts no check; 0 is as unset.
func TestUpdateCheckTurnedOff(t *testing.T) {
	t.Parallel()
	latestRelease(t, "v0.5.1", "v0.6.0")
	cacheIn(t)
	for value, off := range map[string]bool{"1": true, "0": false, "": false} {
		setenv(t, noUpdateCheck, value)
		check := startUpdateCheck(context.Background(), testCmdEnv(t, nil, io.Discard, io.Discard))
		if (check == nil) != off {
			t.Errorf("%s=%q: check %v, want off %v", noUpdateCheck, value, check, off)
		}
		check.Newer()
	}
}
