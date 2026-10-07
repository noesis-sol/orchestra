package organ

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

// The scout says what the project is built with, for init's verifier: each item on one line, blanks
// and repeats dropped; an answer without a stack has none.
func TestScoutSaysWhatTheProjectIsBuiltWith(t *testing.T) {
	bin, record := fakeClaude(t, `{"is_error":false,"structured_output":{"suites":[],`+
		`"stack":[" Go  1.26 ","","Bubble Tea\nTUI","golangci-lint","Go 1.26"],"note":""}}`)
	s, err := Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, false))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Go 1.26", "Bubble Tea TUI", "golangci-lint"}; !slices.Equal(s.Stack, want) {
		t.Errorf("stack %q, want %q", s.Stack, want)
	}
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), `"required":["suites","stack","note"]`) {
		t.Error("the schema doesn't ask for the stack")
	}
	if s, err := parseScouting(Result{Result: `{"suites":[],"note":""}`}); err != nil || len(s.Stack) != 0 {
		t.Errorf("no stack: %+v, %v", s, err)
	}
}
