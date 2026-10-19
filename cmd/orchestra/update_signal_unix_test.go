//go:build unix

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// stallArchive is a transport that sends the requests on to http.DefaultTransport but the archive's,
// which it holds until the request's context ends, saying on started that the download has begun.
type stallArchive struct{ started chan<- struct{} }

func (s stallArchive) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasSuffix(req.URL.Path, ".tar.gz") {
		return http.DefaultTransport.RoundTrip(req)
	}
	close(s.started)
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// Ctrl+C during the download stops orchestra update rather than killing it: the download is
// cancelled, the old binary stays as it was, and the temporary file beside it is removed.
func TestUpdateStoppedDuringTheDownloadLeavesNothing(t *testing.T) {
	// Not parallel: it signals the test's own process, which the runs of tests beside it would catch too.
	u, _ := releaseServer(t, "v0.6.0", false)
	started := make(chan struct{})
	u.Client = &http.Client{Transport: stallArchive{started: started}}
	exe := oldBinary(t)
	go func() {
		<-started // orchestra update catches SIGINT by now: it did before asking for the release
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
	}()

	code, _, errOut := updateIn(t, "v0.5.1", exe, u)
	if code != dispatch.ExitInterrupted || !strings.HasPrefix(errOut, "orchestra update: stopped; ") ||
		!strings.HasSuffix(errOut, "orchestra is unchanged.\n") {
		t.Errorf("exit %d, stderr %q: want it stopped, the binary unchanged", code, errOut)
	}
	if got := read(t, exe); got != "the old one\n" {
		t.Errorf("the binary is %q, want the old one", got)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".orchestra-update-*")); len(left) > 0 {
		t.Errorf("left behind: %q", left)
	}
}
