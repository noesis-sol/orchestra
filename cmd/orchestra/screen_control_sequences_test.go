package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// failingScreen is a screen organ that fails with err.
type failingScreen struct {
	fakeOrganFeature
	err error
}

func (f failingScreen) Screen(context.Context, organ.Request) (organ.Screening, error) {
	return organ.Screening{}, f.err
}

// The screen organ's reason for not planning a request, and its error, are printed without the
// escape sequences a request or a README could make it echo (a clipboard write, a window title).
func TestScreenDropsControlSequences(t *testing.T) {
	osc52, osc0 := "\x1b]52;c;cm0gLXJmIH4K\x07", "\x1b]0;pwned\x07"
	words := "That sends " + osc52 + "passwords " + osc0 + "away.\r"
	for _, tc := range []struct {
		name   string
		organs featureOrgans
		want   string
	}{
		{"reject", fakeOrganFeature{screen: organ.Screening{Verdict: organ.ScreenReject, Reason: words}},
			"orchestra won't plan this request: That sends passwords away."},
		{"unclear", fakeOrganFeature{screen: organ.Screening{Verdict: organ.ScreenUnclear, Reason: words}},
			"orchestra can't plan this request yet: That sends passwords away."},
		{"error", failingScreen{err: errors.New(words)},
			"orchestra couldn't screen the request: That sends passwords away."},
	} {
		f, _, errOut := featureFixture(t, tc.organs, &fakeFeatureTracker{}, true, false, "")
		if epic, code := f.run(context.Background()); epic != "" || code != dispatch.ExitSetup {
			t.Errorf("%s: epic %q, exit %d", tc.name, epic, code)
		}
		for _, r := range errOut.String() {
			if r != '\n' && unicode.IsControl(r) {
				t.Errorf("%s: %q came through:\n%q", tc.name, r, errOut.String())
				break
			}
		}
		if !strings.Contains(errOut.String(), tc.want) {
			t.Errorf("%s: stderr lacks %q:\n%s", tc.name, tc.want, errOut)
		}
	}
}
