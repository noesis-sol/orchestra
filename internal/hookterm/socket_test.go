package hookterm

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A socket something listens on is SocketListening; none, or one whose listener has gone, is
// SocketGone; a connection that fails otherwise, here for a path too long for a socket, is SocketUnsure.
func TestProbe(t *testing.T) {
	dir, err := os.MkdirTemp("", "hk") // short: a socket's path has a limit of about 100 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) }) // a leftover is no failure
	open := filepath.Join(dir, "open")
	l, err := net.Listen("unix", open)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() }) // only listened to be probed
	gone := filepath.Join(dir, "gone")
	g, err := net.ListenUnix("unix", &net.UnixAddr{Name: gone, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	g.SetUnlinkOnClose(false) // the socket stays, refusing connections, as a server that exited leaves it
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, path string
		want       Socket
	}{
		{"listening", open, SocketListening},
		{"listener gone", gone, SocketGone},
		{"no socket", filepath.Join(dir, "none"), SocketGone},
		{"path too long", filepath.Join(dir, strings.Repeat("x", 200)), SocketUnsure},
	} {
		if got := Probe(c.path); got != c.want {
			t.Errorf("%s: Probe = %d, want %d", c.name, got, c.want)
		}
	}
}
