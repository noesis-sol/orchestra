package hookterm

import (
	"errors"
	"net"
	"os"
	"syscall"
	"time"
)

// Socket is what Probe found on a terminal's unix socket.
type Socket int

const (
	// SocketListening is a socket that took the connection: the terminal runs.
	SocketListening Socket = iota
	// SocketGone is no socket, or one that refuses the connection, as a terminal that exited leaves it.
	SocketGone
	// SocketUnsure is a connection that failed otherwise, such as one that timed out: the terminal may
	// still run.
	SocketUnsure
)

// Probe connects to the unix socket at path, for up to a second, to see whether something listens on
// it, and closes the connection. The terminal's own messages aren't read.
func Probe(path string) Socket {
	c, err := net.DialTimeout("unix", path, time.Second)
	if err == nil {
		_ = c.Close() // only connected to see that it can be
		return SocketListening
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
		return SocketGone
	}
	return SocketUnsure
}
