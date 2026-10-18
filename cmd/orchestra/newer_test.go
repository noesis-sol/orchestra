package main

import (
	"context"
	"encoding/json"
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

// latestRelease makes this build orchestra v (buildVersion) and serves, in GitHub's place, a latest
// release tagged tag, until the test ends. It returns how many times it was asked.
func latestRelease(t *testing.T, v, tag string) *atomic.Int32 {
	t.Helper()
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "html_url": "https://example.com/" + tag})
	}))
	oldVersion, oldReleases := version, releases
	version, releases = v, update.Updater{API: srv.URL}
	t.Cleanup(func() {
		version, releases = oldVersion, oldReleases
		srv.Close()
	})
	return &asked
}

// cacheIn gives the test a user cache folder of its own and returns the check's cache folder in it.
// It sets the process's environment, as update.CacheDir reads it (os.UserCacheDir): a test that calls
// it runs serially, as does one that calls latestRelease, which sets this package's version and
// releases.
func cacheIn(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home+"/.cache")
	dir, err := update.CacheDir()
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
	latestRelease(t, "v0.5.1", "v0.6.0")
	cacheRelease(t, cacheIn(t), "v0.6.0")
	for _, dashboard := range []bool{true, false} {
		check := startUpdateCheck(context.Background(), func(string) string { return "" })
		var stdout, stderr strings.Builder
		showNewer(check, dashboard, &stdout, &stderr)
		if stdout.Len() > 0 || stderr.Len() > 0 {
			t.Errorf("dashboard %v: stdout %q, stderr %q, want nothing", dashboard, stdout.String(), stderr.String())
		}
	}
}

// ORCHESTRA_NO_UPDATE_CHECK=1 starts no check; 0 is as unset.
func TestUpdateCheckTurnedOff(t *testing.T) {
	latestRelease(t, "v0.5.1", "v0.6.0")
	cacheIn(t)
	for value, off := range map[string]bool{"1": true, "0": false, "": false} {
		check := startUpdateCheck(context.Background(), func(k string) string {
			if k == noUpdateCheck {
				return value
			}
			return ""
		})
		if (check == nil) != off {
			t.Errorf("%s=%q: check %v, want off %v", noUpdateCheck, value, check, off)
		}
		check.Newer()
	}
}
