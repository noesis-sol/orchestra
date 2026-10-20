package project

import (
	"strings"
	"testing"
)

// suiteLinesChecks are the suites of a project whose scout said how to run them on part of it.
func suiteLinesChecks() PromptChecks {
	return PromptChecks{Check: FastRunner,
		Fast: &[]Suite{{Name: "unit", Command: "go test ./...", Targeted: "go test ./<package>/..."}},
		Full: &[]Suite{{Name: "e2e", Command: "npm run e2e"}}}
}

// Rewriting the opening, init --update keeps the prompt's lines on running each suite on part of the
// project, a line of the project's own under them included.
func TestUpdateKeepsThePromptsSuiteLines(t *testing.T) {
	repo := t.TempDir()
	prompt := fillTemplate(promptTemplate, suiteLinesChecks())
	bullets := suiteBullets(suiteLinesChecks().Fast, suiteLinesChecks().Full)
	if bullets == "" || !strings.Contains(prompt, suitesIntro+bullets) {
		t.Fatalf("the prompt lacks the suite lines %q:\n%s", bullets, prompt)
	}
	own := bullets + "    (the e2e suite needs Docker running)\n"
	prompt = strings.Replace(prompt, bullets, own, 1)
	// The opening as a project worded it before the template's change: it lacks that change's marker.
	const marker = "Put throwaway files in a new folder of your scratchpad"
	prompt = strings.Replace(prompt, marker, "Put throwaway files in your scratchpad", 1)
	writeRepoFile(t, repo, Dir+"/"+promptName, prompt)
	writeRepoFile(t, repo, Dir+"/"+SettingsName, `{"check_fast": "scripts/check-fast.sh"}`+"\n")

	_, updates, err := UpdateTemplates(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 1 || !strings.Contains(strings.Join(updates[0].Sections, ","), "the opening") {
		t.Fatalf("updates %+v, want the worker prompt's opening", updates)
	}
	got := readRepoFile(t, repo, Dir+"/"+promptName)
	if !strings.Contains(got, marker) {
		t.Errorf("the opening wasn't rewritten:\n%s", got)
	}
	if !strings.Contains(got, suitesIntro+own+"- Leave the whole suite to orchestra") {
		t.Errorf("the updated prompt lost its suite lines %q:\n%s", own, got)
	}
	if strings.Contains(got, "The suites are the commands") {
		t.Errorf("the updated prompt has the lines for suites init doesn't know:\n%s", got)
	}
}

// A prompt without the suite lines gets those init writes where it doesn't know the suites.
func TestCurrentPromptWithoutSuiteLines(t *testing.T) {
	got := currentPrompt(fillTemplate(promptTemplate, PromptChecks{Check: FastRunner}), "")
	if !strings.Contains(got, suiteLines(PromptChecks{Check: FastRunner})) || strings.Contains(got, suitesIntro) {
		t.Errorf("the prompt's suite lines:\n%s", got)
	}
}

// Rewriting Its checks, init --update keeps the verifier's bullets on running each suite on what a change
// touches; a verifier without them gets the line for suites init doesn't know.
func TestUpdateKeepsTheVerifiersParts(t *testing.T) {
	repo := t.TempDir()
	c := suiteLinesChecks()
	bullets := suiteBullets(c.Fast, c.Full)
	text := VerifierText([]string{"Go"}, nil, nil, c.Fast, c.Full)
	if !strings.Contains(text, partsIntro+bullets) {
		t.Fatalf("the verifier lacks its parts %q after %q:\n%s", bullets, partsIntro, text)
	}
	itsChecks := []TemplateChange{{Section: "Its checks"}}
	current, err := currentVerifier(repo, text)
	if err != nil {
		t.Fatal(err)
	}
	if got := updated(text, current, itsChecks); got != text {
		t.Errorf("Its checks rewritten:\n%s\nwant it as it was:\n%s", got, text)
	}

	bare := VerifierText([]string{"Go"}, nil, nil, nil, nil)
	if current, err = currentVerifier(repo, bare); err != nil {
		t.Fatal(err)
	}
	if got := updated(bare, current, itsChecks); got != bare || strings.Contains(got, partsIntro) {
		t.Errorf("Its checks of a verifier without parts rewritten:\n%s\nwant:\n%s", got, bare)
	}
}

func TestKeptBullets(t *testing.T) {
	for _, c := range []struct{ text, want string }{
		{"a\nintro\n  - x\n    more\n  - y\n- next\n", "  - x\n    more\n  - y\n"},
		{"intro\n  - x\n\n  - not\n", "  - x\n"},
		{"intro\n  - x", ""},
		{"a\n  intro\n  - x\n", ""},
		{"no intro\n  - x\n", ""},
	} {
		if got := keptBullets(c.text, "intro\n"); got != c.want {
			t.Errorf("keptBullets(%q) = %q, want %q", c.text, got, c.want)
		}
	}
}
