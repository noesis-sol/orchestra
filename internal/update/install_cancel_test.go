package update

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
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

// An update stopped during its download, as orchestra update's Ctrl+C stops it, leaves the old binary
// as it was and no temporary file beside it. The new binary is synced before the rename makes it the
// binary (stage: extract, Sync, Close, then the rename in Install), which no test can observe: the
// data reaches the disk in either order unless the machine fails in between.
func TestInstallStoppedDuringTheDownload(t *testing.T) {
	r := newRelease(t, "v0.6.0", "orchestra v0.6.0\n")
	u := r.serve(t)
	rel := latest(t, u)
	started := make(chan struct{})
	u.Client = &http.Client{Transport: stallArchive{started: started}}
	exe, _ := installed(t)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-started
		cancel()
	}()
	_, err := u.Install(ctx, rel, exe)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Install: %v, want it cancelled", err)
	}
	assertUntouched(t, exe)
}
