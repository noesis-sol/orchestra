package organ

import (
	"context"
	"errors"
	"testing"
	"time"
)

// claude -p refused for the account's usage limit exits 1, with is_error and api_error
// usage_limit_reached (Claude Code 2.1.301): the call's error is one UsageLimit recognises, with
// Claude Code's words. Any other error is not.
func TestUsageLimitRefusalIsRecognised(t *testing.T) {
	const words = "You've hit your session limit · resets 7pm (Europe/Athens)"
	ask := func(out string) error {
		_, err := Client{Bin: answers(t, out, 1)}.ask(context.Background(),
			spec{name: "triage", limit: time.Minute, effort: "low", system: "s"}, "i")
		return err
	}
	err := ask(`{"type":"result","subtype":"success","is_error":true,"api_error_status":429,` +
		`"api_error":"usage_limit_reached","result":"` + words + `","total_cost_usd":0,"num_turns":1}`)
	if said, limited := UsageLimit(err); !limited || said != words {
		t.Errorf("UsageLimit(%v) = %q, %v", err, said, limited)
	}
	for _, err := range []error{
		ask(`{"type":"result","subtype":"success","is_error":true,"api_error_status":529,` +
			`"api_error":"overloaded_error","result":"API Error: overloaded"}`),
		ask(`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"boom"}`),
		errors.New("claude: exit status 1"),
		nil,
	} {
		if said, limited := UsageLimit(err); limited {
			t.Errorf("UsageLimit(%v) = %q, true", err, said)
		}
	}
}
