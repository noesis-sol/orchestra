package git

import (
	"maps"
	"testing"
)

func TestParseStatusRenames(t *testing.T) {
	for _, tc := range []struct {
		name, out string
		want      map[string]string
	}{
		{"renamed in the index", "R  new.go\x00old.go\x00 M a.go\x00",
			map[string]string{"new.go": "R ", "a.go": " M"}},
		{"copied in the index", "C  copy.go\x00orig.go\x00?? b.txt\x00",
			map[string]string{"copy.go": "C ", "b.txt": "??"}},
		{"renamed in the work tree", " R new.go\x00old.go\x00 M a.go\x00",
			map[string]string{"new.go": " R", "a.go": " M"}},
		{"copied in the work tree", " C copy.go\x00orig.go\x00?? b.txt\x00",
			map[string]string{"copy.go": " C", "b.txt": "??"}},
		{"renamed in both", "RR newer.go\x00old.go\x00",
			map[string]string{"newer.go": "RR"}},
		{"modified after a rename", "RM new.go\x00old.go\x00",
			map[string]string{"new.go": "RM"}},
	} {
		got := map[string]string{}
		for _, c := range parseStatus(tc.out) {
			got[c.path] = c.code
		}
		if !maps.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
