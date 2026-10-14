package organ

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ---- Reviewer ------------------------------------------------------------------------

// review writes the end-of-run report: what finished, what was set aside and why, and what needs the
// maintainer. It answers in Markdown, with no schema.
var review = spec{name: "review", limit: 5 * time.Minute, effort: "medium",
	system: systemPrompt("review", fromTheRun)}

// Review writes the end-of-run report from the evidence, whose sections Section makes. An empty
// report, or one cut off at the model's token limit, is an error: the maintainer would read nothing,
// or half of it, as though it were all.
func (g Client) Review(ctx context.Context, evidence string) (string, error) {
	return askAs(ctx, g, review, evidence, func(r Result) (string, error) {
		switch {
		case r.StopReason == StopMaxTokens:
			return "", fmt.Errorf("%s cut the report off at its output token limit", g.Bin)
		case strings.TrimSpace(r.Result) == "":
			return "", fmt.Errorf("%s wrote an empty report", g.Bin)
		}
		return r.Result, nil
	})
}
