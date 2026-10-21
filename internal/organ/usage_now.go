package organ

import (
	"context"
	"time"
)

// usageProbe is the smallest call claude answers, made as an organ's is, to learn whether the account
// is at its usage limit now: not an organ, as it advises on nothing, but kept as small as one, a
// short system prompt and one turn, so that it costs next to nothing.
var usageProbe = spec{name: "usage probe", limit: time.Minute, effort: "low",
	system: "Answer with the single word ok."}

// UsageNow asks claude whether the account is at its usage limit now, as an overnight run's pre-flight
// does: limited, with what Claude Code said of it ("You've hit your session limit · resets 7pm
// (Europe/Athens)"), where the account's limit refused the call; err where the call failed otherwise.
// It is a real call on the account, at the claude CLI's default model, the workers': a fraction of a
// cent.
func (g Client) UsageNow(ctx context.Context) (said string, limited bool, err error) {
	_, err = g.ask(ctx, usageProbe, "ok?")
	if said, limited = UsageLimit(err); limited {
		return said, true, nil
	}
	return "", false, err
}
