package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
)

// fakeRelease is a GitHub serving one release: its details at /releases/latest and its files under
// /download/.
type fakeRelease struct {
	tag   string
	files map[string][]byte  // by name
	key   ed25519.PrivateKey // its release key, which signed checksums.txt
	mu    sync.Mutex
	got   []string // the paths requested
}

// archive is a release archive holding binary as orchestra, beside a README as GoReleaser's do.
func archive(t *testing.T, binary string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range []struct{ name, body string }{{"README.md", "# orchestra\n"}, {"orchestra", binary}} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newRelease is release tag with an archive for the running platform holding binary, its
// checksums.txt, and checksums.txt.sig signed with a release key of its own.
func newRelease(t *testing.T, tag, binary string) *fakeRelease {
	t.Helper()
	name := ArchiveName(tag, runtime.GOOS, runtime.GOARCH)
	a := archive(t, binary)
	sum := sha256.Sum256(a)
	sums := fmt.Appendf(nil, "%s  %s\n%s  orchestra_x_plan9_386.tar.gz\n", hex.EncodeToString(sum[:]), name, strings.Repeat("0", 64))
	key := newKey(t)
	return &fakeRelease{tag: tag, key: key, files: map[string][]byte{
		name:      a,
		Checksums: sums,
		Signature: ed25519.Sign(key, sums),
	}}
}

// newKey is a new ed25519 key pair.
func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// serve serves r and returns the Updater that reads it, trusting r's release key, its --version
// reading the new binary's file: the fake binaries are text, not programs.
func (r *fakeRelease) serve(t *testing.T) Updater {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.got = append(r.got, req.URL.Path)
		r.mu.Unlock()
		if req.URL.Path == "/releases/latest" {
			assets := []map[string]string{}
			for name := range r.files {
				assets = append(assets, map[string]string{"name": name, "browser_download_url": srv.URL + "/download/" + name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": r.tag, "html_url": "https://example.com/releases/" + r.tag, "assets": assets})
			return
		}
		if body, ok := r.files[strings.TrimPrefix(req.URL.Path, "/download/")]; ok {
			_, _ = w.Write(body)
			return
		}
		http.NotFound(w, req)
	}))
	t.Cleanup(srv.Close)
	return Updater{API: srv.URL, Version: readVersion, Keys: []ed25519.PublicKey{r.key.Public().(ed25519.PublicKey)}}
}

// requested is the paths r was asked for.
func (r *fakeRelease) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

// readVersion is a fake binary's --version: its text, once it is executable.
func readVersion(_ context.Context, bin string) (string, error) {
	fi, err := os.Stat(bin)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm() != 0o755 {
		return "", fmt.Errorf("%s has mode %v", bin, fi.Mode().Perm())
	}
	b, err := os.ReadFile(bin)
	return string(b), err
}

// installed is an old binary in a folder of its own, and the folder.
func installed(t *testing.T) (exe, dir string) {
	t.Helper()
	dir = t.TempDir()
	exe = filepath.Join(dir, "orchestra")
	if err := os.WriteFile(exe, []byte("orchestra v0.5.1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe, dir
}

// assertUntouched checks that the old binary is still in dir, alone, as installed left it.
func assertUntouched(t *testing.T, exe string) {
	t.Helper()
	if b, err := os.ReadFile(exe); err != nil || string(b) != "orchestra v0.5.1\n" {
		t.Errorf("the old binary is %q (%v), want it untouched", b, err)
	}
	assertAlone(t, filepath.Dir(exe), filepath.Base(exe))
}

// assertAlone checks that dir holds names and nothing else: no temporary file left.
func assertAlone(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	slices.Sort(names)
	if !slices.Equal(got, names) {
		t.Errorf("%s holds %q, want %q", dir, got, names)
	}
}

func latest(t *testing.T, u Updater) Release {
	t.Helper()
	rel, err := u.Latest(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return rel
}

// The release's binary replaces the one a symbolic link names, with the link left as it was.
func TestInstallReplacesTheBinary(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	u := r.serve(t)
	exe, dir := installed(t)
	link := filepath.Join(t.TempDir(), "orchestra")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	rel := latest(t, u)
	if rel.Tag != "v0.6.0" || rel.URL != "https://example.com/releases/v0.6.0" {
		t.Errorf("latest release %+v", rel)
	}

	path, err := u.Install(t.Context(), rel, link)
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(exe); path != want {
		t.Errorf("replaced %s, want %s", path, want)
	}
	if b, err := os.ReadFile(exe); err != nil || string(b) != "orchestra v0.6.0\n" {
		t.Errorf("the binary is %q (%v), want the release's", b, err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link was replaced: %v %v", fi.Mode(), err)
	}
	assertAlone(t, dir, "orchestra")
}

// An archive whose SHA-256 isn't checksums.txt's is refused, the old binary kept.
func TestInstallRefusesAChecksumMismatch(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	r.files[ArchiveName("v0.6.0", runtime.GOOS, runtime.GOARCH)] = archive(t, "orchestra v0.6.0, tampered with\n")
	u := r.serve(t)
	exe, _ := installed(t)

	_, err := u.Install(t.Context(), latest(t, u), exe)
	if ce, ok := errors.AsType[*ChecksumError](err); !ok || !strings.Contains(err.Error(), "checksums.txt says") {
		t.Errorf("err = %v (%#v), want a *ChecksumError", err, ce)
	}
	assertUntouched(t, exe)
}

// A release without an archive for the platform says which platform, without downloading anything.
func TestInstallNamesAMissingPlatform(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	u := r.serve(t)
	u.GOOS, u.GOARCH = "plan9", "386"
	exe, _ := installed(t)

	_, err := u.Install(t.Context(), latest(t, u), exe)
	if ma, ok := errors.AsType[*MissingAssetError](err); !ok || ma.Platform != "plan9/386" ||
		err.Error() != "release v0.6.0 has no archive for plan9/386 (orchestra_0.6.0_plan9_386.tar.gz)" {
		t.Errorf("err = %v, want a *MissingAssetError for plan9/386", err)
	}
	assertUntouched(t, exe)
	if got := r.requested(); !slices.Equal(got, []string{"/releases/latest"}) {
		t.Errorf("requested %q, want only the release's details", got)
	}
}

// A release without checksums.txt isn't installed: nothing would check the archive.
func TestInstallNeedsTheChecksums(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	delete(r.files, Checksums)
	u := r.serve(t)
	exe, _ := installed(t)

	_, err := u.Install(t.Context(), latest(t, u), exe)
	if _, ok := errors.AsType[*MissingAssetError](err); !ok || err.Error() != "release v0.6.0 has no checksums.txt" {
		t.Errorf("err = %v, want a *MissingAssetError for checksums.txt", err)
	}
	assertUntouched(t, exe)
}

// A folder orchestra update can't write in, such as root's /usr/local/bin, fails before the download.
func TestInstallFailsInAFolderItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes in any folder")
	}
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	u := r.serve(t)
	exe, dir := installed(t)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := u.Install(t.Context(), latest(t, u), exe)
	if resolved, _ := filepath.EvalSymlinks(dir); err == nil {
		t.Fatal("installed")
	} else if nw, ok := errors.AsType[*NotWritableError](err); !ok || nw.Dir != resolved || !errors.Is(err, os.ErrPermission) {
		t.Errorf("err = %v, want a *NotWritableError for %s", err, dir)
	}
	assertUntouched(t, exe)
	if got := r.requested(); !slices.Equal(got, []string{"/releases/latest"}) {
		t.Errorf("requested %q, want only the release's details", got)
	}
}

// A new binary that fails --version, or names another version, isn't put in place.
func TestInstallChecksTheNewBinaryRuns(t *testing.T) {
	for _, c := range []struct {
		name    string
		binary  string
		version func(context.Context, string) (string, error)
		want    string
	}{
		{"fails", "orchestra v0.6.0\n", func(context.Context, string) (string, error) {
			return "", errors.New("exec format error")
		}, "the new binary doesn't run: exec format error"},
		{"another version", "orchestra v0.5.9\n", readVersion,
			`the new binary's --version says "orchestra v0.5.9", not orchestra v0.6.0`},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newRelease(t, "v0.6.0", c.binary)
			u := r.serve(t)
			u.Version = c.version
			exe, _ := installed(t)

			_, err := u.Install(t.Context(), latest(t, u), exe)
			if err == nil || err.Error() != c.want {
				t.Errorf("err = %v, want %q", err, c.want)
			}
			assertUntouched(t, exe)
		})
	}
}

// Without a Version of its own, the Updater runs the new binary with --version.
func TestInstallRunsTheNewBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a new executable, which macOS scans first")
	}
	r := newRelease(t, "v0.6.0", "#!/bin/sh\n[ \"$1\" = --version ] && echo orchestra v0.6.0\n")
	u := r.serve(t)
	u.Version = nil
	exe, dir := installed(t)

	if _, err := u.Install(t.Context(), latest(t, u), exe); err != nil {
		t.Fatal(err)
	}
	assertAlone(t, dir, "orchestra")
}

// GitHub's refusal, a rate limit say, comes back as an *HTTPError with its message.
func TestLatestReportsGitHubsRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := Updater{API: srv.URL}.Latest(t.Context())
	if he, ok := errors.AsType[*HTTPError](err); !ok || he.Status != http.StatusForbidden ||
		!strings.HasSuffix(err.Error(), ": 403 Forbidden: API rate limit exceeded") {
		t.Errorf("err = %v, want a 403 *HTTPError with GitHub's message", err)
	}
}

// A request stops with its context.
func TestLatestStopsWithItsContext(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-done }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(done) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := (Updater{API: srv.URL}).Latest(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
