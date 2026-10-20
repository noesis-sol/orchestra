package claude

import (
	"time"

	"github.com/noesis-sol/orchestra/internal/claudescreen"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// usageLimit is the usage limit a turn that ended on an API error of the kind (StopFailure's error)
// with the message (its last_assistant_message) ended on, the turn having ended at at; false when the
// error is another, as a server that limits requests is. claudescreen's comment above limitWords says
// how Claude Code ends such a turn.
func usageLimit(kind, message string, at time.Time) (dispatch.UsageLimit, bool) {
	if kind != "rate_limit" || !claudescreen.SaysLimit(message) {
		return dispatch.UsageLimit{}, false
	}
	return dispatch.UsageLimit{At: at, Reset: dispatch.LimitReset(message, at), Message: message}, true
}
