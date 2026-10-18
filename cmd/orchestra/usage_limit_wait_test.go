package main

import (
	"strings"
	"testing"
	"time"
)

// A run waits for the usage limit to reset as long as settings.json's usage_limit_wait says, 6h
// without it; a value it can't read is a setup problem naming it.
func TestUsageLimitWaitSetting(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		settings string
		want     time.Duration
	}{{"", 6 * time.Hour}, {`{"usage_limit_wait": "90m"}`, 90 * time.Minute}, {`{"usage_limit_wait": "0"}`, 0}} {
		if o, _, problems := settle(t, nil, nil, c.settings); len(problems) > 0 || o.UsageLimitWait != c.want {
			t.Errorf("settings %q: wait %s, problems %q; want %s", c.settings, o.UsageLimitWait, problems, c.want)
		}
	}
	_, _, problems := settle(t, nil, nil, `{"usage_limit_wait": "soon"}`)
	if len(problems) != 1 || !strings.Contains(problems[0], "usage_limit_wait") {
		t.Errorf("problems %q", problems)
	}
}
