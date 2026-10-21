package organ

import (
	"strings"
	"testing"
)

// UsageNow tells an account at its usage limit, with Claude Code's words, from one that answers, and
// from a call that fails otherwise.
func TestUsageNow(t *testing.T) {
	const words = "You've hit your session limit · resets 7pm (Europe/Athens)"
	for _, tc := range []struct {
		name, out string
		code      int
		limited   bool
		failed    string
	}{
		{"answering", `{"type":"result","subtype":"success","result":"ok","total_cost_usd":0.0004,"num_turns":1}`, 0,
			false, ""},
		{"at its limit", `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,` +
			`"api_error":"usage_limit_reached","result":"` + words + `","total_cost_usd":0,"num_turns":1}`, 1, true, ""},
		{"overloaded", `{"type":"result","subtype":"success","is_error":true,"api_error_status":529,` +
			`"api_error":"overloaded_error","result":"API Error: overloaded"}`, 1, false, "API Error: overloaded"},
		{"not logged in", `Invalid API key · Please run /login`, 1, false, "exit status 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			said, limited, err := Client{Bin: answers(t, tc.out, tc.code)}.UsageNow(t.Context())
			switch {
			case limited != tc.limited:
				t.Errorf("limited %v, want %v (said %q, err %v)", limited, tc.limited, said, err)
			case tc.limited && (said != words || err != nil):
				t.Errorf("said %q, err %v", said, err)
			case tc.failed == "" && err != nil:
				t.Errorf("err %v", err)
			case tc.failed != "" && (err == nil || !strings.Contains(err.Error(), tc.failed)):
				t.Errorf("err %v, want one saying %q", err, tc.failed)
			}
		})
	}
}
