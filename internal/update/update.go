// Package update replaces the orchestra binary with the latest GitHub Release's (orchestra update): it
// reads the latest release, downloads its archive for this platform, checksums.txt and its signature,
// verifies the signature with the release keys embedded in the binary, checks the archive's SHA-256
// against checksums.txt, and puts the binary in it in place of the old one once it runs and names the
// release's version. The archive and checksum names are those .goreleaser.yaml publishes.
package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// DefaultAPI is the GitHub API's address for orchestra's repository, which is public: its requests
// need no token.
const DefaultAPI = "https://api.github.com/repos/noesis-sol/orchestra"

// Time limits on the requests, as internal/command has for commands: a request still going at its
// limit is stopped and fails.
const (
	RequestLimit  = 30 * time.Second // the latest release's details, checksums.txt and its signature
	DownloadLimit = 5 * time.Minute  // the archive
)

// Size limits on what the requests read: far above what a release has, below what would fill memory.
const (
	maxDetails = 4 << 20   // the release's JSON, checksums.txt and its signature
	maxArchive = 256 << 20 // the archive, and the binary in it
)

// Checksums is the file of a release's SHA-256 sums.
const Checksums = "checksums.txt"

// Release is a GitHub Release of orchestra.
type Release struct {
	Tag    string            // its version, v0.6.0
	URL    string            // its page
	Assets map[string]string // the download address of each file attached, by name
}

// Updater reads and installs releases. Its zero value uses GitHub, the running platform and the new
// binary's own --version.
type Updater struct {
	API    string       // the repository's API address; "" for DefaultAPI
	Client *http.Client // nil for http.DefaultClient
	GOOS   string       // "" for the running one
	GOARCH string       // "" for the running one
	// Keys are the public keys checksums.txt.sig may verify with; nil for ReleaseKeys.
	Keys []ed25519.PublicKey
	// Version runs the binary at bin with --version and returns what it prints; nil runs it.
	Version func(ctx context.Context, bin string) (string, error)
}

// HTTPError is a request GitHub answered with a status other than 200 OK.
type HTTPError struct {
	URL     string
	Status  int
	Message string // GitHub's message, when it gave one
}

func (e *HTTPError) Error() string {
	s := fmt.Sprintf("GET %s: %d %s", e.URL, e.Status, http.StatusText(e.Status))
	if e.Message != "" {
		s += ": " + e.Message
	}
	return s
}

// MissingAssetError is a release without a file orchestra update needs: the archive for the
// platform (Platform set), checksums.txt or its signature.
type MissingAssetError struct {
	Release  string // its tag
	Name     string // the file's name
	Platform string // GOOS/GOARCH, for an archive
}

func (e *MissingAssetError) Error() string {
	if e.Platform != "" {
		return fmt.Sprintf("release %s has no archive for %s (%s)", e.Release, e.Platform, e.Name)
	}
	return fmt.Sprintf("release %s has no %s", e.Release, e.Name)
}

// ChecksumError is an archive whose SHA-256 isn't the one checksums.txt gives.
type ChecksumError struct {
	Name string // the archive
	Want string // from checksums.txt
	Got  string // of what was downloaded
}

func (e *ChecksumError) Error() string {
	return fmt.Sprintf("%s has SHA-256 %s, but %s says %s", e.Name, e.Got, Checksums, e.Want)
}

// NotWritableError is a binary whose folder orchestra update may not write in, such as root's
// /usr/local/bin.
type NotWritableError struct {
	Dir string
	Err error
}

func (e *NotWritableError) Error() string { return fmt.Sprintf("cannot write in %s: %v", e.Dir, e.Err) }

func (e *NotWritableError) Unwrap() error { return e.Err }

// cannotWrite reports whether err is a folder orchestra update may not write in: one it lacks the
// permission for, or one on a read-only filesystem.
func cannotWrite(err error) bool {
	return errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// Platform is u's GOOS/GOARCH.
func (u Updater) Platform() string { return u.goos() + "/" + u.goarch() }

func (u Updater) goos() string {
	if u.GOOS != "" {
		return u.GOOS
	}
	return runtime.GOOS
}

func (u Updater) goarch() string {
	if u.GOARCH != "" {
		return u.GOARCH
	}
	return runtime.GOARCH
}

// ArchiveName is the name of the release tag's archive for goos and goarch, as .goreleaser.yaml
// names it: orchestra_0.6.0_darwin_arm64.tar.gz for v0.6.0.
func ArchiveName(tag, goos, goarch string) string {
	return fmt.Sprintf("orchestra_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)
}

// Latest reads the repository's latest release, which GitHub never makes a draft or a pre-release.
func (u Updater) Latest(ctx context.Context) (Release, error) {
	api := u.API
	if api == "" {
		api = DefaultAPI
	}
	body, err := u.get(ctx, RequestLimit, strings.TrimSuffix(api, "/")+"/releases/latest",
		"application/vnd.github+json", maxDetails)
	if err != nil {
		return Release{}, fmt.Errorf("cannot read the latest release: %w", err)
	}
	var r struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return Release{}, fmt.Errorf("cannot read the latest release: %w", err)
	}
	if _, ok := parse(r.TagName); !ok {
		return Release{}, fmt.Errorf("the latest release's tag %q is not a version", r.TagName)
	}
	rel := Release{Tag: r.TagName, URL: r.HTMLURL, Assets: map[string]string{}}
	for _, a := range r.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// Install replaces the binary at exe, through any symbolic links, with the release's for u's
// platform: it downloads checksums.txt and its signature, verifies the signature with u's keys, downloads
// the archive and checks its SHA-256 against checksums.txt, writes the
// binary in it to a temporary file beside exe, checks that it runs and prints the release's version,
// and renames it over exe. On every failure the old binary is left as it was and the temporary file
// removed. It returns the path it replaced.
func (u Updater) Install(ctx context.Context, rel Release, exe string) (_ string, err error) {
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("cannot find the binary to replace: %w", err)
	}
	a, err := u.assetsOf(rel)
	if err != nil {
		return exe, err
	}
	keys := u.Keys
	if keys == nil {
		if keys, err = ReleaseKeys(); err != nil {
			return exe, err
		}
	}

	// Made first, so that a folder it can't write in fails before the download.
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".orchestra-update-*")
	if cannotWrite(err) {
		return exe, &NotWritableError{Dir: dir, Err: err}
	}
	if err != nil {
		return exe, err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()           // already closed once written; nothing more to lose
			_ = os.Remove(tmp.Name()) // the old binary stays; a leftover temporary file breaks nothing
		}
	}()

	archive, err := u.fetchVerified(ctx, a, keys)
	if err != nil {
		return exe, err
	}
	if err := u.stage(ctx, tmp, archive, a.archive, rel.Tag); err != nil {
		return exe, err
	}
	if err := os.Rename(tmp.Name(), exe); cannotWrite(err) {
		return exe, &NotWritableError{Dir: dir, Err: err}
	} else if err != nil {
		return exe, err
	}
	return exe, nil
}

// releaseAssets are the files of a release that Install downloads: the archive for its platform,
// by name, and the addresses of each.
type releaseAssets struct {
	archive                     string // its name
	archiveURL, sumsURL, sigURL string
}

// assetsOf finds in rel the files Install needs for u's platform, or a MissingAssetError for the
// first it lacks.
func (u Updater) assetsOf(rel Release) (releaseAssets, error) {
	a := releaseAssets{archive: ArchiveName(rel.Tag, u.goos(), u.goarch())}
	var ok bool
	if a.archiveURL, ok = rel.Assets[a.archive]; !ok {
		return a, &MissingAssetError{Release: rel.Tag, Name: a.archive, Platform: u.Platform()}
	}
	if a.sumsURL, ok = rel.Assets[Checksums]; !ok {
		return a, &MissingAssetError{Release: rel.Tag, Name: Checksums}
	}
	if a.sigURL, ok = rel.Assets[Signature]; !ok {
		return a, &MissingAssetError{Release: rel.Tag, Name: Signature}
	}
	return a, nil
}

// fetchVerified downloads checksums.txt and its signature, verifies the signature with keys, then
// downloads the archive and checks its SHA-256 against checksums.txt, and returns the archive.
func (u Updater) fetchVerified(ctx context.Context, a releaseAssets, keys []ed25519.PublicKey) ([]byte, error) {
	sums, err := u.get(ctx, RequestLimit, a.sumsURL, "application/octet-stream", maxDetails)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %w", Checksums, err)
	}
	// Verified before any checksum in it is trusted, or the archive downloaded.
	sig, err := u.get(ctx, RequestLimit, a.sigURL, "application/octet-stream", maxDetails)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %w", Signature, err)
	}
	if err := Verify(sums, sig, keys); err != nil {
		return nil, err
	}
	want, err := checksumOf(sums, a.archive)
	if err != nil {
		return nil, err
	}
	archive, err := u.get(ctx, DownloadLimit, a.archiveURL, "application/octet-stream", maxArchive)
	if err != nil {
		return nil, fmt.Errorf("cannot download %s: %w", a.archive, err)
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return nil, &ChecksumError{Name: a.archive, Want: want, Got: hex.EncodeToString(sum[:])}
	}
	return archive, nil
}

// stage writes the binary in archive, the file called name, to tmp and closes it, makes it
// executable, and checks that it runs and names the release tag as its version.
func (u Updater) stage(ctx context.Context, tmp *os.File, archive []byte, name, tag string) error {
	if err := extract(archive, tmp); err != nil {
		return fmt.Errorf("cannot extract orchestra from %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	out, err := u.version(ctx, tmp.Name())
	if err != nil {
		return fmt.Errorf("the new binary doesn't run: %w", err)
	}
	if got := strings.TrimSpace(out); got != "orchestra "+tag {
		return fmt.Errorf("the new binary's --version says %q, not orchestra %s", got, tag)
	}
	return nil
}

// version runs bin --version.
func (u Updater) version(ctx context.Context, bin string) (string, error) {
	if u.Version != nil {
		return u.Version(ctx, bin)
	}
	return command.Output(ctx, command.ReadLimit, "", bin, "--version")
}

// get reads url within limit, sending accept, and returns at most size bytes of its body.
func (u Updater) get(ctx context.Context, limit time.Duration, url, accept string, size int64) ([]byte, error) {
	ctx, cancel := context.WithTimeoutCause(ctx, limit,
		fmt.Errorf("timed out after %s: %w", command.ShortDuration(limit), context.DeadlineExceeded))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "orchestra-update")
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, fmt.Errorf("GET %s: %w", url, cause)
		}
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // read in full or given up on
	body, err := io.ReadAll(io.LimitReader(resp.Body, size+1))
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			err = cause
		}
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &m) // a body that isn't GitHub's JSON has no message to give
		return nil, &HTTPError{URL: url, Status: resp.StatusCode, Message: m.Message}
	}
	if int64(len(body)) > size {
		return nil, fmt.Errorf("GET %s: over %d MB", url, size>>20)
	}
	return body, nil
}

// checksumOf returns the SHA-256 that sums, a checksums.txt ("<sha256>  <name>" a line), gives name.
func checksumOf(sums []byte, name string) (string, error) {
	s := bufio.NewScanner(bytes.NewReader(sums))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), nil
		}
	}
	if err := s.Err(); err != nil {
		return "", fmt.Errorf("cannot read %s: %w", Checksums, err)
	}
	return "", fmt.Errorf("%s has no sum for %s", Checksums, name)
}

// extract writes the file called orchestra in the tar.gz archive to w.
func extract(archive []byte, w io.Writer) error {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("no orchestra in it")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != "orchestra" {
			continue
		}
		n, err := io.Copy(w, io.LimitReader(tr, maxArchive+1))
		if err != nil {
			return err
		}
		if n > maxArchive {
			return fmt.Errorf("orchestra is over %d MB", maxArchive>>20)
		}
		return nil
	}
}
