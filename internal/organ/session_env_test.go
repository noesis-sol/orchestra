package organ

import (
	"context"
	"os"
	"testing"
	"time"
)

// An organ asked by an orchestra started from a Claude Code session runs as a session of its own, not
// as that one's child: its claude gets none of the session's markers, and orchestra's PATH.
func TestAskLeavesOutAClaudeSessionAndKeepsPath(t *testing.T) {
	for _, v := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION"} {
		t.Setenv(v, "1")
	}
	path := os.Getenv("PATH") + string(os.PathListSeparator) + "/opt/orchestra/bin"
	t.Setenv("PATH", path)
	bin, _ := fakeScript(t, `cat > /dev/null
s="${CLAUDECODE-unset} ${CLAUDE_CODE_ENTRYPOINT-unset} ${CLAUDE_CODE_CHILD_SESSION-unset} $PATH"
echo "{\"type\":\"result\",\"is_error\":false,\"result\":\"$s\"}"
`)
	r, err := Client{Bin: bin}.ask(context.Background(), spec{name: "test", limit: time.Minute, effort: "low", system: "s"}, "i")
	if want := "unset unset unset " + path; err != nil || r.Result != want {
		t.Errorf("got %+v, %v; want %q", r, err, want)
	}
}
