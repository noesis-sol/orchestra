package update

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

// A folder on a read-only filesystem is one orchestra update can't write in, as one it lacks the
// permission for is: both get the advice to run it as the folder's owner, not "update failed".
func TestCannotWriteOnAReadOnlyFilesystem(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&fs.PathError{Op: "open", Path: "/usr/local/bin/.orchestra-update-1", Err: syscall.EROFS}, true},
		{&fs.PathError{Op: "open", Path: "/usr/local/bin/.orchestra-update-1", Err: syscall.EACCES}, true},
		{&fs.PathError{Op: "open", Path: "/usr/local/bin/.orchestra-update-1", Err: syscall.ENOSPC}, false},
		{errors.New("other"), false},
		{nil, false},
	} {
		if got := cannotWrite(c.err); got != c.want {
			t.Errorf("cannotWrite(%v) = %t, want %t", c.err, got, c.want)
		}
	}
}
