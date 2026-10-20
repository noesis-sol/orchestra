package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/noesis-sol/orchestra/internal/update"
)

// releaseServer is a GitHub whose latest release is tag, its archive for the running platform holding a
// fake orchestra (text naming tag, which the Updater's Version reads), its checksums.txt and
// checksums.txt.sig, signed with a key the Updater trusts; corrupt makes the archive's sum wrong. It returns the Updater that reads it and the paths it was asked for.
func releaseServer(t *testing.T, tag string, corrupt bool) (update.Updater, func() []string) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := "orchestra " + tag + "\n"
	if err := tw.WriteHeader(&tar.Header{Name: "orchestra", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	name := update.ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(buf.Bytes())
	if corrupt {
		sum[0]++
	}
	sums := fmt.Appendf(nil, "%x  %s\n", sum, name)
	public, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{name: buf.Bytes(), update.Checksums: sums, update.Signature: ed25519.Sign(key, sums)}

	var mu sync.Mutex
	var got []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/releases/latest" {
			var assets []map[string]string
			for n := range files {
				assets = append(assets, map[string]string{"name": n, "browser_download_url": srv.URL + "/download/" + n})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": tag, "html_url": "https://example.com/releases/" + tag, "assets": assets})
			return
		}
		if b, ok := files[strings.TrimPrefix(r.URL.Path, "/download/")]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	u := update.Updater{API: srv.URL, Keys: []ed25519.PublicKey{public}, Version: func(_ context.Context, bin string) (string, error) {
		b, err := os.ReadFile(bin)
		return string(b), err
	}}
	return u, func() []string { mu.Lock(); defer mu.Unlock(); return slices.Clone(got) }
}

// oldBinary is an installed orchestra, in a folder of its own.
func oldBinary(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "orchestra")
	if err := os.WriteFile(exe, []byte("the old one\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

// updateIn runs orchestra update with args, as orchestra current at exe.
func updateIn(t *testing.T, current, exe string, u update.Updater, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut strings.Builder
	c, _ := findSubcommand("update") // stop signals handled as the table says
	stop, release := c.stops.catch(runCtx(t))
	defer release()
	code = runUpdate(stop, args, current, exe, u, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestUpdateInstallsANewerRelease(t *testing.T) {
	t.Parallel()
	u, _ := releaseServer(t, "v0.6.0", false)
	exe := oldBinary(t)

	code, out, errOut := updateIn(t, "v0.5.1", exe, u)
	resolved, _ := filepath.EvalSymlinks(exe)
	want := "Updated orchestra v0.5.1 -> v0.6.0 (" + resolved + ")\nRelease notes: https://example.com/releases/v0.6.0\n"
	if code != 0 || !strings.HasSuffix(out, want) {
		t.Errorf("exit %d, stdout:\n%s\nstderr:\n%s\nwant stdout to end %q", code, out, errOut, want)
	}
	if got := read(t, exe); got != "orchestra v0.6.0\n" {
		t.Errorf("the binary is %q, want the release's", got)
	}
}

// The latest release already, or a newer build, is left as it is: only the release's details are read.
func TestUpdateLeavesTheLatestOrANewerOne(t *testing.T) {
	t.Parallel()
	for current, want := range map[string]string{
		"v0.6.0": "orchestra v0.6.0 is the latest release already.\n",
		"v0.7.0": "orchestra v0.7.0 is newer than the latest release, v0.6.0: nothing to do (orchestra update --force installs v0.6.0).\n",
	} {
		t.Run(current, func(t *testing.T) {
			u, requested := releaseServer(t, "v0.6.0", false)
			exe := oldBinary(t)
			if code, out, errOut := updateIn(t, current, exe, u); code != 0 || out != want {
				t.Errorf("exit %d, stdout %q, stderr %q, want %q", code, out, errOut, want)
			}
			if got := read(t, exe); got != "the old one\n" {
				t.Errorf("the binary was replaced: %q", got)
			}
			if got := requested(); !slices.Equal(got, []string{"/releases/latest"}) {
				t.Errorf("requested %q", got)
			}
		})
	}
}

// --force installs the latest release over a newer build.
func TestUpdateForceInstallsOverANewerOne(t *testing.T) {
	t.Parallel()
	u, _ := releaseServer(t, "v0.6.0", false)
	exe := oldBinary(t)
	if code, out, errOut := updateIn(t, "v0.7.0", exe, u, "--force"); code != 0 ||
		!strings.Contains(out, "Updated orchestra v0.7.0 -> v0.6.0") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	if got := read(t, exe); got != "orchestra v0.6.0\n" {
		t.Errorf("the binary is %q, want the release's", got)
	}
}

// A build from a clone, dev or a pseudo-version, is replaced only with --force, which the refusal
// names along with what it would replace.
func TestUpdateReplacesACloneBuildOnlyWithForce(t *testing.T) {
	t.Parallel()
	for _, current := range []string{"dev", "v0.5.2-0.20261005062236-d70b642867ad", "v0.6.0+dirty"} {
		t.Run(current, func(t *testing.T) {
			u, requested := releaseServer(t, "v0.6.0", false)
			exe := oldBinary(t)
			code, out, errOut := updateIn(t, current, exe, u)
			want := "orchestra update: orchestra " + current + " at " + exe + " is a build from a clone, not a release. " +
				"orchestra update --force replaces it with v0.6.0.\n"
			if code != 2 || errOut != want || out != "" {
				t.Errorf("exit %d, stdout %q, stderr %q, want %q", code, out, errOut, want)
			}
			if got := requested(); !slices.Equal(got, []string{"/releases/latest"}) || read(t, exe) != "the old one\n" {
				t.Errorf("requested %q, binary %q: want it left alone", got, read(t, exe))
			}

			if code, out, errOut := updateIn(t, current, exe, u, "--force"); code != 0 ||
				!strings.Contains(out, "Updated orchestra "+current+" -> v0.6.0") {
				t.Errorf("--force: exit %d, stdout %q, stderr %q", code, out, errOut)
			}
			if got := read(t, exe); got != "orchestra v0.6.0\n" {
				t.Errorf("--force: the binary is %q, want the release's", got)
			}
		})
	}
}

// --check reports and changes nothing; it exits 1 when orchestra update would install the release.
func TestUpdateCheckChangesNothing(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		current string
		code    int
		want    string
	}{
		{"v0.5.1", 1, "orchestra v0.5.1; the latest release is v0.6.0 (https://example.com/releases/v0.6.0): orchestra update installs it.\n"},
		{"v0.6.0", 0, "orchestra v0.6.0 is the latest release.\n"},
		{"v0.7.0", 0, "orchestra v0.7.0 is newer than the latest release, v0.6.0.\n"},
		{"dev", 1, "orchestra dev is a build from a clone; the latest release is v0.6.0 (https://example.com/releases/v0.6.0): orchestra update --force installs it.\n"},
		{"v0.6.1-0.20261005062236-d70b642867ad", 0, "orchestra v0.6.1-0.20261005062236-d70b642867ad is newer than the latest release, v0.6.0.\n"},
	} {
		t.Run(c.current, func(t *testing.T) {
			u, requested := releaseServer(t, "v0.6.0", false)
			exe := oldBinary(t)
			if code, out, errOut := updateIn(t, c.current, exe, u, "--check"); code != c.code || out != c.want {
				t.Errorf("exit %d, stdout %q, stderr %q, want %d, %q", code, out, errOut, c.code, c.want)
			}
			if got := requested(); !slices.Equal(got, []string{"/releases/latest"}) || read(t, exe) != "the old one\n" {
				t.Errorf("requested %q, binary %q: want it left alone", got, read(t, exe))
			}
		})
	}
}

// A failed update says the binary is unchanged.
func TestUpdateChecksumMismatchLeavesTheBinary(t *testing.T) {
	t.Parallel()
	u, _ := releaseServer(t, "v0.6.0", true)
	exe := oldBinary(t)
	code, _, errOut := updateIn(t, "v0.5.1", exe, u)
	if resolved, _ := filepath.EvalSymlinks(exe); code != 4 || !strings.Contains(errOut, "checksums.txt says") ||
		!strings.HasSuffix(errOut, "\n"+resolved+" is unchanged.\n") {
		t.Errorf("exit %d, stderr:\n%s", code, errOut)
	}
	if got := read(t, exe); got != "the old one\n" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

// A folder it can't write in, root's /usr/local/bin say, needs sudo, which orchestra update leaves
// to the user.
func TestUpdateInAFolderItCannotWriteSaysSudo(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes in any folder")
	}
	u, _ := releaseServer(t, "v0.6.0", false)
	exe := oldBinary(t)
	if err := os.Chmod(filepath.Dir(exe), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(exe), 0o755) })

	code, _, errOut := updateIn(t, "v0.5.1", exe, u)
	if code != 2 || !strings.Contains(errOut, "permission denied") ||
		!strings.HasSuffix(errOut, "Run it as the folder's owner instead: sudo orchestra update\n") {
		t.Errorf("exit %d, stderr:\n%s", code, errOut)
	}
	if got := read(t, exe); got != "the old one\n" {
		t.Errorf("the binary was replaced: %q", got)
	}
}

func TestUpdateRejectsAnArgument(t *testing.T) {
	t.Parallel()
	u, requested := releaseServer(t, "v0.6.0", false)
	if code, _, errOut := updateIn(t, "v0.5.1", oldBinary(t), u, "now"); code != 2 ||
		errOut != "orchestra update: unexpected argument \"now\" (see orchestra update -h)\n" || len(requested()) > 0 {
		t.Errorf("exit %d, stderr %q, requested %q", code, errOut, requested())
	}
}
