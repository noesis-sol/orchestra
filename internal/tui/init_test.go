package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
)

func TestPrerequisitesLeadWithACrossWhenOneIsMissing(t *testing.T) {
	for _, tc := range []struct {
		pre  []project.Step
		lead string
	}{
		{[]project.Step{{Kind: project.StepDone, Label: "bd"}}, "✓"},
		{[]project.Step{{Kind: project.StepDone, Label: "bd"}, {Kind: project.StepMissing, Label: "herdr", Detail: "install Herdr"}}, "✗"},
	} {
		var b strings.Builder
		InitScreen{out: &b, width: 80}.Prerequisites(tc.pre)
		if got := strings.TrimSpace(ansi.Strip(b.String())); !strings.HasPrefix(got, tc.lead+" needs") {
			t.Errorf("want %s first: %q", tc.lead, got)
		}
	}
}

func TestConcurrencyOptionsOfferOnlyValidSettings(t *testing.T) {
	has := func(opts []string, n string) bool {
		for _, o := range opts {
			if strings.HasPrefix(o, n+" ") {
				return true
			}
		}
		return false
	}
	keys := func(current int) []string {
		var ks []string
		for _, o := range concurrencyOptions(current) {
			ks = append(ks, o.Key)
		}
		return ks
	}
	if !has(keys(12), "12") {
		t.Error("a valid current setting is offered")
	}
	if has(keys(project.MaxConcurrency+4), "20") {
		t.Error("an out-of-range setting is offered")
	}
}
