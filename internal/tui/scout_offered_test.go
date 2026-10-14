package tui

import (
	"reflect"
	"testing"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// The checklists offer the scout's suites without those that run a runner, and, when the project has
// scripts/check.sh, with its suite first and fast, added when the scout left it out.
func TestOfferedSuites(t *testing.T) {
	vet := organ.Suite{Name: "vet", Kind: organ.SuiteLint, Command: "go vet ./...", Tier: organ.TierFast}
	e2e := organ.Suite{Name: "e2e", Kind: organ.SuiteE2E, Command: "npm run e2e", Tier: organ.TierFull}
	check := organ.Suite{Name: "check", Kind: organ.SuiteOther, Command: "scripts/check.sh && make lint",
		FoundIn: "Makefile:2", Tier: organ.TierFull, ParallelSafe: true}
	fastCheck := check
	fastCheck.Tier = organ.TierFast
	added := organ.Suite{Name: "check", Kind: organ.SuiteOther, Command: "scripts/check.sh",
		FoundIn: "scripts/check.sh:1", Tier: organ.TierFast, Needs: []string{}}
	fastRunner := organ.Suite{Name: "check-fast", Kind: organ.SuiteOther, Command: "scripts/check-fast.sh"}
	fullRunner := organ.Suite{Name: "check-full", Kind: organ.SuiteOther, Command: "bash $PWD/scripts/check-full.sh"}
	for _, c := range []struct {
		name        string
		suites      []organ.Suite
		checkScript bool
		want        []organ.Suite
	}{
		{"none", nil, false, nil},
		{"none, with check.sh", nil, true, []organ.Suite{added}},
		{"as found", []organ.Suite{vet, e2e}, false, []organ.Suite{vet, e2e}},
		{"check.sh moved first", []organ.Suite{vet, e2e, check}, true, []organ.Suite{fastCheck, vet, e2e}},
		{"check.sh added", []organ.Suite{vet}, true, []organ.Suite{added, vet}},
		{"check.sh's suite without the script", []organ.Suite{vet, check}, false, []organ.Suite{vet, check}},
		{"runners left out", []organ.Suite{fastRunner, vet, fullRunner}, true, []organ.Suite{added, vet}},
	} {
		in := append([]organ.Suite(nil), c.suites...)
		got := offered(c.suites, c.checkScript)
		if len(got) != len(c.want) || len(got) > 0 && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
		if !reflect.DeepEqual(in, c.suites) {
			t.Errorf("%s: the scout's suites changed to %+v", c.name, c.suites)
		}
	}
}
