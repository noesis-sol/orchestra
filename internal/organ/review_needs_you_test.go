package organ

import (
	"strings"
	"testing"
)

// The report's Needs you lists only the kinds the run flags, so a step a worker's close reason says is still
// pending, such as CI, stays out of it and the report agrees with the dashboard's count.
func TestReviewNeedsYouOnlyWhatTheRunFlagged(t *testing.T) {
	for _, want := range []string{
		"## Needs you: only what the run itself flagged for the maintainer, most urgent first, of these kinds alone: ",
		"Nothing else goes under Needs you. A step that a worker's close reason or notes say is still pending " +
			"(CI, a review, a deploy, a check by hand) is not a Needs you entry: at most the ticket's Finished " +
			"bullet mentions it in a few words.",
		`With none of the kinds above, Needs you is "Nothing."`,
	} {
		if !strings.Contains(review.system, want) {
			t.Errorf("the reviewer's prompt doesn't say %q", want)
		}
	}
}
