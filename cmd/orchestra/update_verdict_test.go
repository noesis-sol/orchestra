package main

import "testing"

// A build is the latest release, newer, behind, or a build from a clone, which can be any of the
// last two as well; dev, with no version, is behind.
func TestVerdictOf(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		current string
		want    verdict
	}{
		{"v0.6.0", verdict{latest: true}},
		{"v0.7.0", verdict{newer: true}},
		{"v0.5.1", verdict{behind: true}},
		{"v0.6.0-rc.1", verdict{behind: true}},
		{"dev", verdict{behind: true, clone: true}},
		{"v0.5.2-0.20261005062236-d70b642867ad", verdict{behind: true, clone: true}},
		{"v0.6.1-0.20261005062236-d70b642867ad", verdict{newer: true, clone: true}},
		{"v0.6.0+dirty", verdict{clone: true}},
	} {
		if got := verdictOf(tc.current, "v0.6.0"); got != tc.want {
			t.Errorf("verdictOf(%q, v0.6.0) = %+v, want %+v", tc.current, got, tc.want)
		}
	}
}
