package update

import (
	"regexp"
	"slices"
	"strings"
)

// A version is a semantic version with a leading v, as orchestra's tags and buildVersion give it:
// v0.6.0, v1.2.0-rc.1, a clone's pseudo-version v0.5.2-0.20261005062236-d70b642867ad, maybe with
// +dirty. buildVersion's "dev" isn't one.

// semver is a version's precedence: its major, minor and patch numbers and its pre-release
// identifiers, the numbers as written (without leading zeros, so longer is larger).
type semver struct {
	core [3]string
	pre  []string
}

// parse reads v, ignoring its build metadata (+dirty); false when it isn't a version.
func parse(v string) (semver, bool) {
	v, ok := strings.CutPrefix(v, "v")
	if !ok {
		return semver{}, false
	}
	v, _, _ = strings.Cut(v, "+")
	core, pre, hasPre := strings.Cut(v, "-")
	var s semver
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	for i, p := range parts {
		if !number(p) {
			return semver{}, false
		}
		s.core[i] = p
	}
	if hasPre {
		s.pre = strings.Split(pre, ".")
		if slices.Contains(s.pre, "") {
			return semver{}, false
		}
	}
	return s, true
}

// number reports whether s is a number without leading zeros.
func number(s string) bool {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// compareNumbers compares two numbers without leading zeros.
func compareNumbers(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// Compare compares the versions a and b by semantic-version precedence: -1 when a is older, 0 when
// they rank the same, 1 when a is newer. ok is false when either isn't a version, such as "dev".
func Compare(a, b string) (order int, ok bool) {
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return 0, false
	}
	for i := range x.core {
		if c := compareNumbers(x.core[i], y.core[i]); c != 0 {
			return c, true
		}
	}
	switch {
	case len(x.pre) == 0 && len(y.pre) == 0:
		return 0, true
	case len(x.pre) == 0:
		return 1, true // a release ranks above its pre-releases
	case len(y.pre) == 0:
		return -1, true
	}
	for i := 0; i < len(x.pre) && i < len(y.pre); i++ {
		p, q := x.pre[i], y.pre[i]
		switch pn, qn := number(p), number(q); {
		case pn && qn:
			if c := compareNumbers(p, q); c != 0 {
				return c, true
			}
		case pn:
			return -1, true // numbers rank below words
		case qn:
			return 1, true
		default:
			if c := strings.Compare(p, q); c != 0 {
				return c, true
			}
		}
	}
	switch {
	case len(x.pre) < len(y.pre):
		return -1, true
	case len(x.pre) > len(y.pre):
		return 1, true
	}
	return 0, true
}

// pseudo matches a Go pseudo-version, the version of a build from a commit without a tag of its own
// (golang.org/x/mod/module's pattern).
var pseudo = regexp.MustCompile(`^v[0-9]+\.(0\.0-|\d+\.\d+-([^+]*\.)?0\.)\d{14}-[A-Za-z0-9]+` +
	`(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// IsRelease reports whether v is a release's version: a version that is neither a pseudo-version
// nor marked with build metadata (+dirty), which builds from a clone are.
func IsRelease(v string) bool {
	_, ok := parse(v)
	return ok && !strings.Contains(v, "+") && !pseudo.MatchString(v)
}
