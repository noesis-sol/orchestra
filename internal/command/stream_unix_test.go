//go:build unix

package command

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A streamed command's stdout reaches the writer while the command still runs, and all of it has by
// the time Stream returns; the command reads its input on stdin.
func TestStreamWritesOutputAsItComes(t *testing.T) {
	dir := t.TempDir()
	r, w := io.Pipe()
	first := make(chan string, 1)
	go func() {
		b := make([]byte, 64)
		n, _ := r.Read(b)
		first <- string(b[:n])
		_, _ = io.Copy(io.Discard, r)
	}()
	done := make(chan error, 1)
	go func() {
		// The command waits for the go file, which the test writes once it has read the first line.
		done <- Stream(context.Background(), ReadLimit, dir, nil, "the spec\n", w, "sh", "-c",
			"read line; echo \"got $line\"; while [ ! -e go ]; do sleep 0.01; done; echo second")
		_ = w.Close()
	}()
	select {
	case got := <-first:
		if got != "got the spec\n" {
			t.Errorf("first output %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no output while the command ran")
	}
	if err := os.WriteFile(filepath.Join(dir, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("Stream: %v", err)
	}
}

// A streamed command that fails is an *Error carrying its stderr, after the writer has had its stdout.
func TestStreamFailureCarriesStderr(t *testing.T) {
	var out strings.Builder
	err := Stream(context.Background(), ReadLimit, "", []string{"WHO=claude"}, "", &out, "sh", "-c",
		"echo partial; echo \"$WHO: not logged in\" >&2; exit 3")
	e, ok := errors.AsType[*Error](err)
	if !ok || e.Stderr != "claude: not logged in" || e.Stopped || out.String() != "partial\n" {
		t.Errorf("err %v, stdout %q", err, out.String())
	}
}
