package update

import "testing"

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		order int
		ok    bool
	}{
		{"v0.6.0", "v0.6.0", 0, true},
		{"v0.5.1", "v0.6.0", -1, true},
		{"v0.6.1", "v0.6.0", 1, true},
		{"v0.10.0", "v0.9.9", 1, true},
		{"v1.0.0", "v0.99.99", 1, true},
		{"v0.6.0-rc.1", "v0.6.0", -1, true},
		{"v0.6.0-rc.2", "v0.6.0-rc.10", -1, true},
		{"v0.6.0-rc.1", "v0.6.0-beta", 1, true},
		{"v0.6.0-1", "v0.6.0-rc", -1, true},
		{"v0.6.0-rc", "v0.6.0-rc.1", -1, true},
		{"v0.6.0+dirty", "v0.6.0", 0, true},
		// A clone's build after v0.5.1 ranks between it and v0.5.2.
		{"v0.5.2-0.20261005062236-d70b642867ad", "v0.5.2", -1, true},
		{"v0.5.2-0.20261005062236-d70b642867ad", "v0.5.1", 1, true},
		{"dev", "v0.6.0", 0, false},
		{"0.6.0", "v0.6.0", 0, false},
		{"v0.6", "v0.6.0", 0, false},
		{"v0.06.0", "v0.6.0", 0, false},
		{"v0.6.0-", "v0.6.0", 0, false},
	} {
		if order, ok := Compare(c.a, c.b); order != c.order || ok != c.ok {
			t.Errorf("Compare(%q, %q) = %d, %v, want %d, %v", c.a, c.b, order, ok, c.order, c.ok)
		}
	}
}

func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.6.0":                               true,
		"v1.2.0-rc.1":                          true,
		"dev":                                  false,
		"v0.6.0+dirty":                         false,
		"v0.5.2-0.20261005062236-d70b642867ad": false,
		"v0.5.2-0.20261005062236-d70b642867ad+dirty": false,
		"v0.0.0-20261005062236-d70b642867ad":         false,
		"v1.2.0-rc.1.0.20261005062236-d70b642867ad":  false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", v, got, want)
		}
	}
}
