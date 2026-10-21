package project

import (
	"encoding/json"
	"testing"
)

func TestResolveOvernightFreeDisk(t *testing.T) {
	t.Parallel()
	cases := []struct {
		json  string
		gb    int
		fails bool
	}{
		{`{}`, DefaultOvernightFreeDisk, false},
		{`{"overnight_free_disk_gb": 50}`, 50, false},
		{`{"overnight_free_disk_gb": 0}`, 0, false}, // no check
		{`{"overnight_free_disk_gb": -1}`, 0, true},
	}
	for _, c := range cases {
		var s Settings
		if err := json.Unmarshal([]byte(c.json), &s); err != nil {
			t.Fatal(err)
		}
		gb, err := ResolveOvernightFreeDisk(s)
		if (err != nil) != c.fails || (!c.fails && gb != c.gb) {
			t.Errorf("ResolveOvernightFreeDisk(%s) = %d, %v", c.json, gb, err)
		}
	}
}
