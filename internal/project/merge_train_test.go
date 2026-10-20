package project

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveMergeTrain(t *testing.T) {
	for _, c := range []struct {
		json string
		want int
		fail string
	}{
		{`{}`, DefaultMergeTrain, ""},
		{`{"merge_train": 2}`, 2, ""},
		{`{"merge_train": 0}`, 0, ""}, // off
		{`{"merge_train": -1}`, 0, "merge_train must be 0 (off) or more (got -1)"},
	} {
		var s Settings
		if err := json.Unmarshal([]byte(c.json), &s); err != nil {
			t.Fatal(err)
		}
		n, err := ResolveMergeTrain(s)
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
