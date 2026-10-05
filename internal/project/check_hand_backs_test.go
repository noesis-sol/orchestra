package project

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveCheckHandBacks(t *testing.T) {
	for _, c := range []struct {
		json string
		want int
		fail string
	}{
		{`{}`, DefaultCheckHandBacks, ""},
		{`{"check_hand_backs": 3}`, 3, ""},
		{`{"check_hand_backs": 0}`, 0, ""}, // never
		{`{"check_hand_backs": -1}`, 0, "check_hand_backs must be 0 (never) or more (got -1)"},
	} {
		var s Settings
		if err := json.Unmarshal([]byte(c.json), &s); err != nil {
			t.Fatal(err)
		}
		n, err := ResolveCheckHandBacks(s)
		if c.fail != "" {
			if err == nil || !strings.Contains(err.Error(), c.fail) {
				t.Errorf("%s: error %v, want %q", c.json, err, c.fail)
			}
			continue
		}
		if err != nil || n != c.want {
			t.Errorf("%s: %d, %v; want %d", c.json, n, err, c.want)
		}
	}
}
