package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A run says at its end when a newer release is out (orchestra update installs it): it starts a
// Check as it starts, which reads the latest release in the background, at most once a day, and
// keeps the answer in a cache file between runs. A check never fails or slows a run: its errors are
// ignored, and one still going as the run ends is stopped and says nothing.

// Time limits on the check.
const (
	CheckLimit = 5 * time.Second // the request for the latest release
	CheckEvery = 24 * time.Hour  // how long an answer in the cache stands before GitHub is asked again
)

// CacheName is the cache file's name, in CacheDir.
const CacheName = "latest-release.json"

// CacheDir is the folder of the check's cache, orchestra's in the user's cache folder
// (os.UserCacheDir: ~/Library/Caches on macOS, $XDG_CACHE_HOME or ~/.cache on Linux).
func CacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "orchestra"), nil
}

// cached is the cache file: the latest release's tag, as of when GitHub was last asked.
type cached struct {
	Latest  string    `json:"latest,omitempty"` // "" when GitHub has never answered
	Checked time.Time `json:"checked"`
}

// Checker checks whether a newer release than the running build is out.
type Checker struct {
	Updater Updater
	Current string           // the running build's version
	Cache   string           // the cache's folder; "" for none, which asks GitHub every time
	Now     func() time.Time // nil for time.Now
}

// Check is a check Start started.
type Check struct {
	cancel context.CancelFunc
	done   chan struct{}
	newer  string // set before done closes
}

// Start starts the check in the background, under ctx, unless the running build isn't a release
// (dev, a pseudo-version): it then does nothing, nor does the Check it returns.
func (c Checker) Start(ctx context.Context) *Check {
	check := &Check{done: make(chan struct{})}
	if !IsRelease(c.Current) {
		close(check.done)
		return check
	}
	ctx, check.cancel = context.WithCancel(ctx)
	go func() {
		defer close(check.done)
		check.newer = c.newer(ctx)
	}()
	return check
}

// Newer stops the check if it is still going, waits for it to return, and gives the newer
// release's tag: "" when there is none, or the check couldn't say in time. Safe on a nil Check.
func (k *Check) Newer() string {
	if k == nil {
		return ""
	}
	if k.cancel != nil {
		k.cancel()
	}
	<-k.done
	return k.newer
}

// Notice is the line a run ends with when release newer is out and current runs.
func Notice(newer, current string) string {
	return fmt.Sprintf("orchestra %s is available (you have %s): orchestra update", newer, current)
}

// newer returns the latest release's tag when it is newer than c.Current, from the cache when GitHub
// was asked within CheckEvery, or else from GitHub, recording its answer. A failed request is
// recorded too, keeping the last answer, so that GitHub isn't asked again within a day; one stopped
// with ctx, as the run ended before it, isn't.
func (c Checker) newer(ctx context.Context) string {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	last := c.read()
	if age := now.Sub(last.Checked); age < 0 || age >= CheckEvery {
		reqCtx, cancel := context.WithTimeoutCause(ctx, CheckLimit,
			fmt.Errorf("timed out after %s: %w", command.ShortDuration(CheckLimit), context.DeadlineExceeded))
		rel, err := c.Updater.Latest(reqCtx)
		cancel()
		switch {
		case err == nil:
			last = cached{Latest: rel.Tag, Checked: now}
		case ctx.Err() != nil:
			return "" // stopped before GitHub answered: asked again next time
		default:
			last.Checked = now
		}
		c.write(last)
	}
	if order, ok := Compare(c.Current, last.Latest); ok && order < 0 {
		return last.Latest
	}
	return ""
}

// read reads the cache, which is empty when there is no cache or it can't be read.
func (c Checker) read() cached {
	var last cached
	if c.Cache == "" {
		return last
	}
	b, err := os.ReadFile(filepath.Join(c.Cache, CacheName))
	if err != nil {
		return last
	}
	if json.Unmarshal(b, &last) != nil {
		return cached{}
	}
	return last
}

// write replaces the cache with last, through a temporary file renamed over it, so that a run
// reading it never sees half of it. A cache that can't be written is asked about again next run.
func (c Checker) write(last cached) {
	if c.Cache == "" {
		return
	}
	b, err := json.Marshal(last)
	if err != nil {
		return
	}
	if os.MkdirAll(c.Cache, 0o755) != nil {
		return
	}
	tmp, err := os.CreateTemp(c.Cache, "."+CacheName+"-*")
	if err != nil {
		return
	}
	_, err = tmp.Write(b)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(c.Cache, CacheName))
	}
	if err != nil {
		_ = os.Remove(tmp.Name()) // a leftover temporary file breaks nothing
	}
}
