// Package scripts holds the tests of the project's shell scripts.
package scripts

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/update"
)

func TestMain(m *testing.M) { os.Exit(faketool.Main(m).Run()) }

// releasesPath is where the fake releases are served, as GitHub serves orchestra's.
const releasesPath = "/noesis-sol/orchestra/releases"

// release is a fake release: its files by name.
type release struct {
	tag   string
	files map[string][]byte
}

// newRelease is a release of tag, its checksums.txt signed with key, with an archive for each platform
// holding an orchestra that prints "orchestra <tag>".
func newRelease(t *testing.T, key ed25519.PrivateKey, tag string) *release {
	t.Helper()
	archive := tarGz(t, map[string]string{
		"orchestra": fmt.Sprintf("#!/bin/sh\necho 'orchestra %s'\n", tag),
		"README.md": "# Agent Orchestra\n",
	})
	r := &release{tag: tag, files: map[string][]byte{}}
	var sums strings.Builder
	for _, goos := range []string{"darwin", "linux"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			name := update.ArchiveName(tag, goos, goarch)
			r.files[name] = archive
			sum := sha256.Sum256(archive)
			fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		}
	}
	r.sign(key, sums.String())
	return r
}

// sign replaces the release's checksums.txt with sums, signed with key.
func (r *release) sign(key ed25519.PrivateKey, sums string) {
	r.files[update.Checksums] = []byte(sums)
	r.files[update.Signature] = ed25519.Sign(key, []byte(sums))
}

// tarGz is a tar.gz archive of files, each executable.
func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)),
			Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// server serves releases as GitHub does: …/releases/latest redirects to the latest's page, …/tag/<tag>,
// and each file is at …/download/<tag>/<name>. It records the paths requested.
type server struct {
	url string // the releases' address, for ORCHESTRA_DOWNLOAD_URL

	mu        sync.Mutex
	requested []string
	agents    []string // the User-Agent of each request
}

// serve serves releases, the latest last.
func serve(t *testing.T, releases ...*release) *server {
	t.Helper()
	s := &server{}
	latest := releases[len(releases)-1].tag
	mux := http.NewServeMux()
	mux.HandleFunc(releasesPath+"/latest", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, releasesPath+"/tag/"+latest, http.StatusFound)
	})
	mux.HandleFunc(releasesPath+"/tag/{tag}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>a release</html>"))
	})
	mux.HandleFunc(releasesPath+"/download/{tag}/{name}", func(w http.ResponseWriter, req *http.Request) {
		for _, r := range releases {
			if b, ok := r.files[req.PathValue("name")]; ok && r.tag == req.PathValue("tag") {
				_, _ = w.Write(b)
				return
			}
		}
		http.NotFound(w, req)
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s.mu.Lock()
		s.requested = append(s.requested, req.URL.Path)
		s.agents = append(s.agents, req.UserAgent())
		s.mu.Unlock()
		mux.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL + releasesPath
	return s
}

// downloaded reports whether the file name was requested.
func (s *server) downloaded(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.requested, func(p string) bool { return filepath.Base(p) == name })
}

// install is a run of the install script.
type install struct {
	shell string            // the shell that runs it; "" for sh
	env   map[string]string // added to its environment
	path  []string          // folders put before the PATH
	only  []string          // when set, the PATH is these folders alone, rather than the test's PATH

	home, system string // its HOME and the folder in place of /usr/local/bin, made by run
	out          string // what it printed
}

// run runs the script piped into the shell, as curl … | sh does, against s, trusting key's public half,
// and returns the error it exited with.
func (in *install) run(t *testing.T, s *server, key ed25519.PrivateKey) error {
	t.Helper()
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	in.home, in.system = t.TempDir(), t.TempDir()
	path := append(slices.Clone(in.path), filepath.SplitList(os.Getenv("PATH"))...)
	if in.only != nil {
		path = in.only
	}
	env := map[string]string{
		"PATH":                       strings.Join(path, string(os.PathListSeparator)),
		"HOME":                       in.home,
		"TMPDIR":                     t.TempDir(),
		"SHELL":                      "/bin/zsh",
		"ORCHESTRA_DOWNLOAD_URL":     s.url,
		"ORCHESTRA_TEST_RELEASE_KEY": publicPEM(t, key),
		"ORCHESTRA_TEST_SYSTEM_DIR":  in.system,
	}
	maps.Copy(env, in.env)
	shell := in.shell
	if shell == "" {
		shell = "sh"
	}
	if shell, err = exec.LookPath(shell); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell)
	cmd.Stdin = bytes.NewReader(script)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	in.out = string(out)
	return err
}

// installed is what dir/orchestra --version prints, "" when there is no orchestra in dir. It fails the
// test if dir holds anything else, such as a temporary file left behind.
func installed(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return ""
	}
	if !slices.Equal(names, []string{"orchestra"}) {
		t.Errorf("%s holds %q, want orchestra alone", dir, names)
	}
	out, err := exec.CommandContext(t.Context(), filepath.Join(dir, "orchestra"), "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// newKey is a new ed25519 private key.
func newKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// publicPEM is key's public half as PKIX PEM, as internal/update/release-keys has them.
func publicPEM(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// skipIfShort skips a test that runs the script, which takes over a second: each run starts a binary it
// has just written, which macOS scans first.
func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipped by -short: runs the install script, which starts binaries it has just written")
	}
}

// signatureChecked reports whether the run verified the signature, false where there was no OpenSSL 3.
func signatureChecked(t *testing.T, out string) bool {
	t.Helper()
	switch {
	case strings.Contains(out, "Verified the signature of checksums.txt"):
		return true
	case strings.Contains(out, "The signature of checksums.txt wasn't checked"):
		return false
	}
	t.Errorf("the output says nothing of the signature:\n%s", out)
	return false
}

// The script's release keys are internal/update/release-keys/'s.
func TestInstallScriptHasTheReleaseKeys(t *testing.T) {
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rest := script; ; {
		var block *pem.Block
		if block, rest = pem.Decode(rest); block == nil {
			break
		}
		key, err := update.ParsePublicKey(pem.EncodeToMemory(block))
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, hex.EncodeToString(key))
	}
	keys, err := update.ReleaseKeys()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, key := range keys {
		want = append(want, hex.EncodeToString(key))
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("install.sh's keys %q, want internal/update/release-keys' %q", got, want)
	}
}

// Piped into sh, dash or bash, the script installs the latest release where /usr/local/bin would be,
// having checked its SHA-256 and, with OpenSSL 3, its signature, and says how to put the folder on the PATH.
func TestInstallScriptInstallsTheLatestRelease(t *testing.T) {
	skipIfShort(t)
	key := newKey(t)
	s := serve(t, newRelease(t, key, "v0.5.0"), newRelease(t, key, "v0.6.0"))
	for _, shell := range []string{"sh", "dash", "bash"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skipf("no %s here", shell)
			}
			in := &install{shell: shell}
			if err := in.run(t, s, key); err != nil {
				t.Fatalf("%v:\n%s", err, in.out)
			}
			if got := installed(t, in.system); got != "orchestra v0.6.0" {
				t.Errorf("installed %q, want orchestra v0.6.0:\n%s", got, in.out)
			}
			signatureChecked(t, in.out)
			for _, want := range []string{
				"Installed orchestra v0.6.0 in " + filepath.Join(in.system, "orchestra"),
				in.system + " isn't on your PATH",
				fmt.Sprintf(`echo 'export PATH="%s:$PATH"' >> ~/.zshrc`, in.system),
				"orchestra init",
				"orchestra update",
			} {
				if !strings.Contains(in.out, want) {
					t.Errorf("the output lacks %q:\n%s", want, in.out)
				}
			}
		})
	}
}

// An archive whose SHA-256 isn't checksums.txt's stops the install, which installs nothing.
func TestInstallScriptStopsOnAChecksumMismatch(t *testing.T) {
	skipIfShort(t)
	key := newKey(t)
	r := newRelease(t, key, "v0.6.0")
	var sums strings.Builder
	for line := range strings.Lines(string(r.files[update.Checksums])) {
		_, name, _ := strings.Cut(line, "  ")
		fmt.Fprintf(&sums, "%s  %s", strings.Repeat("0", 64), name)
	}
	r.sign(key, sums.String()) // signed, so that the sum is what fails
	in := &install{}
	if err := in.run(t, serve(t, r), key); err == nil {
		t.Fatalf("the install succeeded:\n%s", in.out)
	}
	if !strings.Contains(in.out, "SHA-256 is") || !strings.Contains(in.out, "Nothing was installed") {
		t.Errorf("the output doesn't say the sum differs:\n%s", in.out)
	}
	if got := installed(t, in.system); got != "" {
		t.Errorf("installed %q", got)
	}
}

// A signature that doesn't verify stops the install before the archive is downloaded, where OpenSSL 3 can
// check it.
func TestInstallScriptStopsOnABadSignature(t *testing.T) {
	skipIfShort(t)
	key := newKey(t)
	r := newRelease(t, newKey(t), "v0.6.0") // signed with another key
	s := serve(t, r)
	in := &install{}
	err := in.run(t, s, key)
	if err == nil && !signatureChecked(t, in.out) {
		t.Skip("no OpenSSL 3 here to check the signature")
	}
	if err == nil {
		t.Fatalf("the install succeeded:\n%s", in.out)
	}
	if !strings.Contains(in.out, "checksums.txt.sig doesn't verify checksums.txt") {
		t.Errorf("the output doesn't say the signature is bad:\n%s", in.out)
	}
	if got := installed(t, in.system); got != "" {
		t.Errorf("installed %q", got)
	}
	if name := update.ArchiveName("v0.6.0", "linux", "amd64"); s.downloaded(name) ||
		s.downloaded(update.ArchiveName("v0.6.0", "darwin", "arm64")) {
		t.Error("downloaded the archive")
	}
}

// On a platform without a release binary the script says which there are, and that go install builds one.
func TestInstallScriptRefusesAnUnsupportedPlatform(t *testing.T) {
	skipIfShort(t)
	key := newKey(t)
	s := serve(t, newRelease(t, key, "v0.6.0"))
	for _, c := range []struct{ kernel, machine string }{{"FreeBSD", "amd64"}, {"Linux", "i686"}} {
		t.Run(c.kernel+"-"+c.machine, func(t *testing.T) {
			shims := t.TempDir()
			faketool.Write(t, shims, "uname", fmt.Sprintf(`case "$1" in -s) echo %s ;; -m) echo %s ;; esac`+"\n",
				c.kernel, c.machine))
			in := &install{path: []string{shims}}
			if err := in.run(t, s, key); err == nil {
				t.Fatalf("the install succeeded:\n%s", in.out)
			}
			for _, want := range []string{
				fmt.Sprintf("there is no release binary for %s %s", c.kernel, c.machine),
				"darwin and linux, amd64 and arm64",
				"go install github.com/noesis-sol/orchestra/cmd/orchestra@latest",
			} {
				if !strings.Contains(in.out, want) {
					t.Errorf("the output lacks %q:\n%s", want, in.out)
				}
			}
			if got := installed(t, in.system); got != "" {
				t.Errorf("installed %q", got)
			}
		})
	}
}

// ORCHESTRA_VERSION installs that release rather than the latest, with its v or without.
func TestInstallScriptInstallsTheVersionAskedFor(t *testing.T) {
	skipIfShort(t)
	key := newKey(t)
	s := serve(t, newRelease(t, key, "v0.5.0"), newRelease(t, key, "v0.6.0"))
	for _, version := range []string{"v0.5.0", "0.5.0"} {
		in := &install{env: map[string]string{"ORCHESTRA_VERSION": version}}
		if err := in.run(t, s, key); err != nil {
			t.Fatalf("%s: %v:\n%s", version, err, in.out)
		}
		if got := installed(t, in.system); got != "orchestra v0.5.0" {
			t.Errorf("%s: installed %q, want orchestra v0.5.0:\n%s", version, got, in.out)
		}
	}
	if s.downloaded("latest") {
		t.Error("asked for the latest release")
	}
}

// Where /usr/local/bin isn't writable the script installs in ~/.local/bin, which it makes, and says how to
// install in /usr/local/bin instead, with sudo it doesn't run itself.
func TestInstallScriptFallsBackToTheHomeFolder(t *testing.T) {
	skipIfShort(t)
	if os.Geteuid() == 0 {
		t.Skip("root writes in any folder")
	}
	key := newKey(t)
	s := serve(t, newRelease(t, key, "v0.6.0"))
	// A folder of its own in place of the system one, read-only.
	system := t.TempDir()
	if err := os.Chmod(system, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(system, 0o755) }) // for t.TempDir's removal
	in := &install{env: map[string]string{"ORCHESTRA_TEST_SYSTEM_DIR": system}}
	if err := in.run(t, s, key); err != nil {
		t.Fatalf("%v:\n%s", err, in.out)
	}
	local := filepath.Join(in.home, ".local", "bin")
	if got := installed(t, local); got != "orchestra v0.6.0" {
		t.Errorf("installed %q in ~/.local/bin, want orchestra v0.6.0:\n%s", got, in.out)
	}
	if got := installed(t, system); got != "" {
		t.Errorf("installed %q in the system folder", got)
	}
	for _, want := range []string{
		"Installing in " + local + ", as " + system + " isn't writable without sudo",
		"| sudo ORCHESTRA_INSTALL_DIR=" + system + " sh",
	} {
		if !strings.Contains(in.out, want) {
			t.Errorf("the output lacks %q:\n%s", want, in.out)
		}
	}
}

// ORCHESTRA_INSTALL_DIR installs there, making the folder, and replaces an orchestra already in it; one
// earlier on the PATH is named as still coming first.
func TestInstallScriptInstallsInTheFolderAskedFor(t *testing.T) {
	skipIfShort(t)
	key := newKey(t)
	s := serve(t, newRelease(t, key, "v0.6.0"))
	shadow := t.TempDir()
	faketool.Write(t, shadow, "orchestra", "echo 'orchestra v0.4.0'\n")
	dir := filepath.Join(t.TempDir(), "tools", "bin")
	for _, run := range []string{"new folder", "replacing"} {
		in := &install{env: map[string]string{"ORCHESTRA_INSTALL_DIR": dir}, path: []string{shadow, dir}}
		if err := in.run(t, s, key); err != nil {
			t.Fatalf("%s: %v:\n%s", run, err, in.out)
		}
		if got := installed(t, dir); got != "orchestra v0.6.0" {
			t.Errorf("%s: installed %q, want orchestra v0.6.0:\n%s", run, got, in.out)
		}
		if got := installed(t, in.system); got != "" {
			t.Errorf("%s: installed %q in the system folder", run, got)
		}
		if want := filepath.Join(shadow, "orchestra") + " comes before " + dir; !strings.Contains(in.out, want) {
			t.Errorf("%s: the output lacks %q:\n%s", run, want, in.out)
		}
		if strings.Contains(in.out, "isn't on your PATH") {
			t.Errorf("%s: the output says the folder isn't on the PATH:\n%s", run, in.out)
		}
	}
}

// Without curl the script downloads with wget.
func TestInstallScriptDownloadsWithWget(t *testing.T) {
	skipIfShort(t)
	if _, err := exec.LookPath("wget"); err != nil {
		t.Skip("no wget here")
	}
	// A PATH of the tools the script runs, but curl.
	bin := t.TempDir()
	for _, tool := range []string{"sh", "uname", "mktemp", "rm", "wget", "awk", "sed", "tail", "tr", "openssl",
		"sha256sum", "shasum", "perl", "tar", "gzip", "mkdir", "chmod", "cp", "mv", "cat", "env"} {
		path, err := exec.LookPath(tool)
		if errors.Is(err, exec.ErrNotFound) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	key := newKey(t)
	s := serve(t, newRelease(t, key, "v0.5.0"), newRelease(t, key, "v0.6.0"))
	in := &install{only: []string{bin}}
	if err := in.run(t, s, key); err != nil {
		t.Fatalf("%v:\n%s", err, in.out)
	}
	if got := installed(t, in.system); got != "orchestra v0.6.0" {
		t.Errorf("installed %q, want orchestra v0.6.0:\n%s", got, in.out)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, agent := range s.agents {
		if !strings.HasPrefix(agent, "Wget/") {
			t.Errorf("%s was requested by %q, not wget", s.requested[i], agent)
		}
	}
}
