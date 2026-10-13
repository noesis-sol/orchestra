package scripts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// shellcheckVersion is the version scripts/shellcheck.sh pins.
const shellcheckVersion = "v0.11.0"

// shellcheckArchive is the name of the release's archive for this platform, as shellcheck.sh downloads it.
func shellcheckArchive(t *testing.T) string {
	t.Helper()
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" || arch == "" {
		t.Skipf("ShellCheck has no binary pinned for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return "shellcheck-" + shellcheckVersion + "." + runtime.GOOS + "." + arch + ".tar.gz"
}

// shellcheckServer serves archive as the releases' download address does, and counts the requests.
type shellcheckServer struct {
	url      string // for SHELLCHECK_DOWNLOAD_URL
	requests atomic.Int32
}

func serveShellcheck(t *testing.T, archive []byte) *shellcheckServer {
	t.Helper()
	s := &shellcheckServer{}
	name := shellcheckArchive(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s.requests.Add(1)
		if req.URL.Path != "/"+shellcheckVersion+"/"+name {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(archive)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// shellcheckRun is a run of shellcheck.sh, with only the environment it needs: not the test's CI.
type shellcheckRun struct {
	cache string            // XDG_CACHE_HOME
	env   map[string]string // added to its environment
	out   string            // what it printed
}

// run runs shellcheck.sh with args, and returns the error it exited with.
func (r *shellcheckRun) run(t *testing.T, args ...string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "./shellcheck.sh", args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "TMPDIR=" + t.TempDir(),
		"XDG_CACHE_HOME=" + r.cache}
	for k, v := range r.env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	r.out = string(out)
	return err
}

// shellcheckCache is where shellcheck.sh keeps ShellCheck under the cache folder cache.
func shellcheckCache(cache string) string {
	return filepath.Join(cache, "orchestra-dev", "shellcheck-"+shellcheckVersion)
}

// closedURL is the address of a server that is gone, as a download address offline would be.
func closedURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

// The first run downloads the archive, checks its sum and keeps ShellCheck in the cache; the next runs
// that one without downloading it again.
func TestShellcheckDownloadsOnceIntoTheCache(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs a ShellCheck it has just unpacked, which macOS scans first")
	}
	archive := tarGz(t, map[string]string{
		"shellcheck-" + shellcheckVersion + "/shellcheck": "#!/bin/sh\necho \"fake shellcheck $*\"\n",
		"shellcheck-" + shellcheckVersion + "/README.txt": "ShellCheck\n",
	})
	sum := sha256.Sum256(archive)
	s := serveShellcheck(t, archive)
	r := &shellcheckRun{cache: t.TempDir(), env: map[string]string{
		"SHELLCHECK_DOWNLOAD_URL": s.url,
		"SHELLCHECK_TEST_SHA256":  hex.EncodeToString(sum[:]),
	}}
	for range 2 {
		if err := r.run(t, "-s", "sh", "a.sh"); err != nil {
			t.Fatalf("%v:\n%s", err, r.out)
		}
		if r.out != "fake shellcheck -s sh a.sh\n" {
			t.Errorf("it printed %q, want the fake's line alone", r.out)
		}
	}
	if n := s.requests.Load(); n != 1 {
		t.Errorf("%d downloads, want 1", n)
	}
	entries, err := os.ReadDir(shellcheckCache(r.cache))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "shellcheck" {
		t.Errorf("the cache holds %v, want shellcheck alone", entries)
	}
}

// An archive whose SHA-256 isn't the pinned one fails the run, offline skip or not, and leaves nothing in
// the cache.
func TestShellcheckFailsOnAWrongSum(t *testing.T) {
	archive := tarGz(t, map[string]string{"shellcheck-" + shellcheckVersion + "/shellcheck": "#!/bin/sh\n"})
	s := serveShellcheck(t, archive)
	r := &shellcheckRun{cache: t.TempDir(), env: map[string]string{"SHELLCHECK_DOWNLOAD_URL": s.url}}
	if err := r.run(t, "a.sh"); err == nil {
		t.Fatalf("it passed with a wrong sum:\n%s", r.out)
	}
	sum := sha256.Sum256(archive)
	if want := "SHA-256 is " + hex.EncodeToString(sum[:]) + ", not the pinned"; !strings.Contains(r.out, want) {
		t.Errorf("the output lacks %q:\n%s", want, r.out)
	}
	if strings.Contains(r.out, "shellcheck skipped") {
		t.Errorf("a wrong sum was skipped:\n%s", r.out)
	}
	entries, err := os.ReadDir(shellcheckCache(r.cache))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("the cache holds %v, want nothing", entries)
	}
}

// Offline with nothing cached, the run passes saying ShellCheck was skipped, except under CI=true.
func TestShellcheckSkipsOfflineUnlessCI(t *testing.T) {
	shellcheckArchive(t)
	url := closedURL(t)
	r := &shellcheckRun{cache: t.TempDir(), env: map[string]string{"SHELLCHECK_DOWNLOAD_URL": url}}
	if err := r.run(t, "a.sh"); err != nil {
		t.Fatalf("%v:\n%s", err, r.out)
	}
	if !strings.HasPrefix(r.out, "shellcheck skipped: cannot download "+url+"/") {
		t.Errorf("it printed %q, want a line saying it skipped ShellCheck and why", r.out)
	}

	r.env["CI"] = "true"
	if err := r.run(t, "a.sh"); err == nil {
		t.Fatalf("it passed offline under CI=true:\n%s", r.out)
	}
	if !strings.Contains(r.out, "cannot download") || !strings.Contains(r.out, "CI=true") {
		t.Errorf("the output doesn't say why it failed:\n%s", r.out)
	}
}

// A ShellCheck in the cache runs offline.
func TestShellcheckRunsTheCachedOneOffline(t *testing.T) {
	shellcheckArchive(t)
	r := &shellcheckRun{cache: t.TempDir(), env: map[string]string{
		"SHELLCHECK_DOWNLOAD_URL": closedURL(t), "CI": "true",
	}}
	dir := shellcheckCache(r.cache)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	faketool.Write(t, dir, "shellcheck", `echo "fake shellcheck $*"`)
	if err := r.run(t, "a.sh"); err != nil {
		t.Fatalf("%v:\n%s", err, r.out)
	}
	if r.out != "fake shellcheck a.sh\n" {
		t.Errorf("it printed %q, want the fake's line alone", r.out)
	}
}

// With no arguments it checks every shell script git tracks, with -s sh where the shebang is /bin/sh, and
// fails when one has a finding, having checked the others.
func TestShellcheckChecksEveryTrackedScript(t *testing.T) {
	shellcheckArchive(t)
	r := &shellcheckRun{cache: t.TempDir(), env: map[string]string{"SHELLCHECK_DOWNLOAD_URL": closedURL(t)}}
	dir := shellcheckCache(r.cache)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	faketool.Write(t, dir, "shellcheck", `echo "fake shellcheck $*"
case "$*" in *install.sh) echo "a finding"; exit 1 ;; esac`)
	if err := r.run(t); err == nil {
		t.Fatalf("it passed with a finding:\n%s", r.out)
	}
	for _, want := range []string{
		"fake shellcheck -s sh -- scripts/check.sh\n",
		"fake shellcheck -s sh -- scripts/install.sh\na finding\n",
		"fake shellcheck -s sh -- scripts/release-notes.sh\n",
	} {
		if !strings.Contains(r.out, want) {
			t.Errorf("the output lacks %q:\n%s", want, r.out)
		}
	}
}
