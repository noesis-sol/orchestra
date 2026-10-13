package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// day is the time the checks in these tests run at.
var day = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

// checker is a Checker of version current on u, its cache in a folder of its own, at day.
func checker(t *testing.T, u Updater, current string) Checker {
	t.Helper()
	return Checker{Updater: u, Current: current, Cache: filepath.Join(t.TempDir(), "orchestra"),
		Now: func() time.Time { return day }}
}

// answer waits for the check to return by itself, then gives its answer.
func (k *Check) answer() string {
	<-k.done
	return k.Newer()
}

// seed writes c's cache as a check at checked that found latest.
func seed(t *testing.T, c Checker, latest string, checked time.Time) {
	t.Helper()
	if err := os.MkdirAll(c.Cache, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(cached{Latest: latest, Checked: checked})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Cache, CacheName), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// cacheOf is c's cache, the zero cached when there is none.
func cacheOf(t *testing.T, c Checker) cached {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(c.Cache, CacheName))
	if os.IsNotExist(err) {
		return cached{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var got cached
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

// A release build with a newer release out finds it, and records it with the time it asked.
func TestCheckFindsANewerRelease(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	c := checker(t, r.serve(t), "v0.5.1")

	if got := c.Start(t.Context()).answer(); got != "v0.6.0" {
		t.Errorf("newer = %q, want v0.6.0", got)
	}
	if got := cacheOf(t, c); got.Latest != "v0.6.0" || !got.Checked.Equal(day) {
		t.Errorf("cache %+v, want v0.6.0 checked at %v", got, day)
	}
	assertAlone(t, c.Cache, CacheName)
	if got := Notice("v0.6.0", "v0.5.1"); got != "orchestra v0.6.0 is available (you have v0.5.1): orchestra update" {
		t.Errorf("notice %q", got)
	}
}

// Up to date, or newer than the latest release, there is nothing to say.
func TestCheckSaysNothingWhenUpToDate(t *testing.T) {
	for _, current := range []string{"v0.6.0", "v0.7.0"} {
		r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
		if got := checker(t, r.serve(t), current).Start(t.Context()).answer(); got != "" {
			t.Errorf("%s: newer = %q, want none", current, got)
		}
	}
}

// Within a day of the last request GitHub isn't asked again: the cache answers. After a day, it is.
func TestCheckAsksGitHubOnceADay(t *testing.T) {
	for _, tc := range []struct {
		name    string
		checked time.Time
		asked   bool
		want    string
	}{
		{"an hour ago", day.Add(-time.Hour), false, "v0.5.2"},
		{"just under a day ago", day.Add(-CheckEvery + time.Minute), false, "v0.5.2"},
		{"a day ago", day.Add(-CheckEvery), true, "v0.6.0"},
		{"in the future", day.Add(time.Hour), true, "v0.6.0"}, // the clock went back
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
			c := checker(t, r.serve(t), "v0.5.1")
			seed(t, c, "v0.5.2", tc.checked)

			if got := c.Start(t.Context()).answer(); got != tc.want {
				t.Errorf("newer = %q, want %q", got, tc.want)
			}
			if asked := len(r.requested()) > 0; asked != tc.asked {
				t.Errorf("asked GitHub: %v, want %v", asked, tc.asked)
			}
		})
	}
}

// A dev or pseudo-version build checks nothing: no request, no cache.
func TestCheckSkipsBuildsFromAClone(t *testing.T) {
	for _, current := range []string{"dev", "v0.5.2-0.20261005062236-d70b642867ad", "v0.5.1+dirty"} {
		r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
		c := checker(t, r.serve(t), current)
		if got := c.Start(t.Context()).answer(); got != "" {
			t.Errorf("%s: newer = %q, want none", current, got)
		}
		if got := r.requested(); len(got) > 0 {
			t.Errorf("%s: requested %q", current, got)
		}
		if _, err := os.Stat(c.Cache); !os.IsNotExist(err) {
			t.Errorf("%s: the cache's folder exists (%v)", current, err)
		}
	}
	if got := (*Check)(nil).Newer(); got != "" {
		t.Errorf("a nil check's newer = %q", got)
	}
}

// A failed request says nothing, and counts as the day's: the last answer is kept, and GitHub isn't
// asked again until the day is out.
func TestCheckIgnoresAFailedRequest(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	t.Cleanup(srv.Close)
	c := checker(t, Updater{API: srv.URL}, "v0.5.1")
	seed(t, c, "v0.5.2", day.Add(-2*CheckEvery))

	if got := c.Start(t.Context()).answer(); got != "v0.5.2" {
		t.Errorf("newer = %q, want the last answer, v0.5.2", got)
	}
	if got := cacheOf(t, c); got.Latest != "v0.5.2" || !got.Checked.Equal(day) {
		t.Errorf("cache %+v, want v0.5.2 checked at %v", got, day)
	}
	c.Now = func() time.Time { return day.Add(time.Hour) }
	c.Start(t.Context()).answer()
	if n := asked.Load(); n != 1 {
		t.Errorf("asked GitHub %d times, want once", n)
	}
}

// A check still waiting on GitHub as the run ends is stopped at once and says nothing, leaving the
// cache for the next run to ask again.
func TestCheckStopsWhenTheRunEnds(t *testing.T) {
	asked := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		close(asked)
		select {
		case <-req.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	c := checker(t, Updater{API: srv.URL}, "v0.5.1")

	check := c.Start(t.Context())
	<-asked
	began := time.Now()
	if got := check.Newer(); got != "" {
		t.Errorf("newer = %q, want none", got)
	}
	if waited := time.Since(began); waited > time.Second {
		t.Errorf("waited %v for the check to stop", waited)
	}
	if got := cacheOf(t, c); got != (cached{}) {
		t.Errorf("cache %+v, want none", got)
	}
}

// An unreadable cache is no cache.
func TestCheckIgnoresABrokenCache(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	c := checker(t, r.serve(t), "v0.5.1")
	if err := os.MkdirAll(c.Cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Cache, CacheName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := c.Start(t.Context()).answer(); got != "v0.6.0" {
		t.Errorf("newer = %q, want v0.6.0", got)
	}
	if got := r.requested(); !slices.Equal(got, []string{"/releases/latest"}) {
		t.Errorf("requested %q", got)
	}
}
