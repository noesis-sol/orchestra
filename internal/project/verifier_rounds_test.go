package project

import (
	"strings"
	"testing"
)

// Each verifier round started blind: on tutti-nrg.4.15 (2026-10-20) round 1 held a requirement one way,
// citing the spec, and round 3 the other, citing what the code does, and the worker deferred after three
// FAILs. A later round is given the earlier rounds' findings, says where it disagrees with one and why,
// and the worker takes such a contradiction to the maintainer rather than go round again.
func TestALaterVerifierRoundSeesTheEarlierFindings(t *testing.T) {
	for _, want := range []string{
		"- From the second round on, give it the earlier rounds' findings too: each round's verdict and the\n" +
			"  gaps it raised, as it gave them, still nothing of your reasoning,",
		"When it disagrees with an earlier round (a line that starts DISAGREES WITH\n  ROUND), don't go round " +
			"again to settle it: note both rounds' findings on the ticket and ask the\n  maintainer which holds, " +
			"as a question, as under Close.\n",
	} {
		if !strings.Contains(verifierSection, want) {
			t.Errorf("the Verify section lacks %q:\n%s", want, verifierSection)
		}
	}
	// The rule comes before the three rounds' limit, which it settles first.
	if strings.Index(verifierSection, "earlier rounds' findings") > strings.Index(verifierSection, "After three rounds") {
		t.Errorf("the earlier rounds' findings come after the three rounds' limit:\n%s", verifierSection)
	}

	agent := VerifierText(nil, nil, nil, nil, nil)
	verify := sections(agent)[indexOf(sections(agent), "How to verify")].text
	for _, want := range []string{
		"6. From the second round on, the worker gives you the earlier rounds' findings: each round's verdict\n" +
			"   and the gaps it raised. Check each again on its own terms rather than take or drop it unseen;",
		"say so in a line that starts `DISAGREES WITH ROUND <n>:`, then its\n   point, why you judge otherwise " +
			"and your evidence",
		"so that the worker takes the contradiction to the maintainer rather than go round again.\n",
	} {
		if !strings.Contains(verify, want) {
			t.Errorf("the verifier's How to verify lacks %q:\n%s", want, verify)
		}
	}
}

// A project's copy written before the change lacks it, and hears of it at a run's start.
func TestOlderCopiesLackTheEarlierFindings(t *testing.T) {
	prompt := strings.ReplaceAll(currentPrompt(verifierSection, ""), "earlier rounds' findings", "")
	if !hasChange(promptFile.missing(prompt, 4), "earlier rounds' findings") {
		t.Error("a worker prompt of version 4 isn't told of the earlier rounds' findings")
	}
	if hasChange(promptFile.missing(currentPrompt(verifierSection, ""), 4), "earlier rounds' findings") {
		t.Error("a worker prompt with the change is told of it")
	}
	verifier := strings.ReplaceAll(VerifierText(nil, nil, nil, nil, nil), "DISAGREES WITH ROUND", "")
	if !hasChange(verifierFile.missing(verifier, 3), "DISAGREES WITH ROUND") {
		t.Error("a verifier of version 3 isn't told of the disagreement rule")
	}
}

// hasChange reports whether changes have the one whose marker is marker.
func hasChange(changes []TemplateChange, marker string) bool {
	for _, c := range changes {
		if c.Marker == marker {
			return true
		}
	}
	return false
}
