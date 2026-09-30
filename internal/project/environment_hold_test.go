package project

import (
	"encoding/json"
	"testing"
	"time"
)

func TestResolveEnvironmentHold(t *testing.T) {
	cases := []struct {
		json   string
		count  int
		window time.Duration
		fails  bool
	}{
		{`{}`, 2, 2 * time.Minute, false},
		{`{"environment_hold": {}}`, 2, 2 * time.Minute, false},
		{`{"environment_hold": {"count": 3, "window": "90s"}}`, 3, 90 * time.Second, false},
		{`{"environment_hold": {"window": "5m"}}`, 2, 5 * time.Minute, false},
		{`{"environment_hold": {"count": 0}}`, 0, 2 * time.Minute, false}, // off
		{`{"environment_hold": {"count": -1}}`, 0, 0, true},
		{`{"environment_hold": {"window": "0"}}`, 0, 0, true},
		{`{"environment_hold": {"window": "two minutes"}}`, 0, 0, true},
	}
	for _, c := range cases {
		var s Settings
		if err := json.Unmarshal([]byte(c.json), &s); err != nil {
			t.Fatal(err)
		}
		count, window, err := ResolveEnvironmentHold(s)
		if (err != nil) != c.fails || (!c.fails && (count != c.count || window != c.window)) {
			t.Errorf("ResolveEnvironmentHold(%s) = %d, %s, %v", c.json, count, window, err)
		}
	}
}

func TestResolveEnvironmentProbe(t *testing.T) {
	cases := []struct {
		json  string
		probe time.Duration
		fails bool
	}{
		{`{}`, 10 * time.Minute, false},
		{`{"environment_hold": {"count": 3}}`, 10 * time.Minute, false},
		{`{"environment_hold": {"probe": "30m"}}`, 30 * time.Minute, false},
		{`{"environment_hold": {"probe": "0"}}`, 0, false}, // no probe
		{`{"environment_hold": {"probe": "0s"}}`, 0, false},
		{`{"environment_hold": {"probe": "-1m"}}`, 0, true},
		{`{"environment_hold": {"probe": "soon"}}`, 0, true},
	}
	for _, c := range cases {
		var s Settings
		if err := json.Unmarshal([]byte(c.json), &s); err != nil {
			t.Fatal(err)
		}
		probe, err := ResolveEnvironmentProbe(s)
		if (err != nil) != c.fails || (!c.fails && probe != c.probe) {
			t.Errorf("ResolveEnvironmentProbe(%s) = %s, %v", c.json, probe, err)
		}
	}
}
