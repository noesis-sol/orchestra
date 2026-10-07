package organ

import (
	"strings"
	"testing"
)

// The screen organ, like the others, is given the evidence first and orchestra's request last.
func TestScreenInputEndsWithTheRequest(t *testing.T) {
	in := screenInput(Request{Text: "Add a flag", Repo: "r", README: "# r"})
	if !strings.HasPrefix(in, "## Feature request\n") ||
		!strings.HasSuffix(in, "</evidence id=\""+evidenceIDs(t, in)[0]+"\">\n\n"+
			"Screen the feature request above for the repository r.\n") {
		t.Errorf("the request should come last, after the evidence:\n%s", in)
	}
}
