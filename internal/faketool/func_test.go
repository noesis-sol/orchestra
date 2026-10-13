//go:build unix

package faketool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// echoFunc is a fake tool that prints its arguments, its input and the variable FAKE, and exits 3.
var _ = Func("echo-func", func(args []string) int {
	in, _ := io.ReadAll(os.Stdin)
	fmt.Printf("%s|%s|%s|%s", strings.Join(args, "|"), in, os.Getenv("FAKE"), os.Getenv(funcVar))
	return 3
})

// A fake tool written with WriteFunc runs its function in the test binary, with the tool's
// arguments, input and environment, and exits with its status; what it starts is no fake tool.
func TestAFuncFakeRunsInTheTestBinary(t *testing.T) {
	bin := WriteFunc(t, t.TempDir(), "echo-func", "FAKE=it's set")
	out, err := command.OutputWithInput(context.Background(), command.ReadLimit, t.TempDir(), nil, "input", bin,
		"show", "k 1")
	var exit *exec.ExitError
	if want := "show|k 1|input|it's set|"; out != want || !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("got %q, %v; want %q and exit status 3", out, err, want)
	}
}
