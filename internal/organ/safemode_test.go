package organ

import (
	"context"
	"testing"
	"time"
)

// An organ runs claude in safe mode, so the user's CLAUDE.md, hooks, skills and auto-memory stay out,
// even when orchestra's own environment turns safe mode off.
func TestAskRunsClaudeInSafeMode(t *testing.T) {
	t.Setenv("CLAUDE_CODE_SAFE_MODE", "0")
	bin, _ := fakeScript(t, `cat > /dev/null
echo "{\"type\":\"result\",\"is_error\":false,\"result\":\"$CLAUDE_CODE_SAFE_MODE\"}"
`)
	r, err := Client{Bin: bin}.Ask(context.Background(), "test", time.Minute, "low", "s", "i", "")
	if err != nil || r.Result != "1" {
		t.Errorf("got %+v, %v; want CLAUDE_CODE_SAFE_MODE=1", r, err)
	}
}
