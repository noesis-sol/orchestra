package claude

import (
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
	"github.com/noesis-sol/orchestra/internal/claudescreen"
)

// usageLimit is the usage limit a turn that ended on an API error of the kind (StopFailure's error)
// with the message (its last_assistant_message) ended on, the turn having ended at at; false when the
// error is another, as a server that limits requests is. claudescreen's comment above limitWords says
// how Claude Code ends such a turn.
func usageLimit(kind, message string, at time.Time) (agentio.UsageLimit, bool) {
	if kind != "rate_limit" || !claudescreen.SaysLimit(message) {
		return agentio.UsageLimit{}, false
	}
	return agentio.UsageLimit{At: at, Reset: agentio.LimitReset(message, at), Message: message}, true
}
