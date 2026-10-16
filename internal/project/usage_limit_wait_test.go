package project

import (
	"encoding/json"
	"testing"
	"time"
)

func TestResolveUsageLimitWait(t *testing.T) {
	cases := []struct {
		json  string
		wait  time.Duration
		fails bool
	}{
		{`{}`, 6 * time.Hour, false},
		{`{"usage_limit_wait": "2h"}`, 2 * time.Hour, false},
		{`{"usage_limit_wait": "90m"}`, 90 * time.Minute, false},
		{`{"usage_limit_wait": "0"}`, 0, false}, // not at all
		{`{"usage_limit_wait": "-1h"}`, 0, true},
		{`{"usage_limit_wait": "all night"}`, 0, true},
	}
	for _, c := range cases {
		var s Settings
		if err := json.Unmarshal([]byte(c.json), &s); err != nil {
			t.Fatal(err)
		}
		wait, err := ResolveUsageLimitWait(s)
		if (err != nil) != c.fails || (!c.fails && wait != c.wait) {
			t.Errorf("ResolveUsageLimitWait(%s) = %s, %v", c.json, wait, err)
		}
	}
}
