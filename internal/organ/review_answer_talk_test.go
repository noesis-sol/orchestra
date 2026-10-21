package organ

import (
	"strings"
	"testing"
)

// The report's Needs you points a question at the answer talk first, the dashboard's key or orchestra answer, and
// at bd human respond only as the command-line way.
func TestReviewNeedsYouPointsAtTheAnswerTalk(t *testing.T) {
	want := `is answered in the answer talk, which the dashboard's a key opens, or with: orchestra answer <question id>; ` +
		`on the command line alone, with: bd human respond <question id> --response "…"`
	if !strings.Contains(review.system, want) {
		t.Errorf("the reviewer's prompt doesn't say %q", want)
	}
}
