package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

// The templates as their last recorded versions have them. A change to a template is a new version of
// it: TestTemplateChangesAreRecorded fails until templates.go records it.
const (
	promptTemplateSum   = "c9c0bec876d583a186382c84ccbc472e06d1d34bf21e7048ed36fdd390eac2e3"
	verifierTemplateSum = "925b141110e3006a95942db7d5df3ab6f9106755d379957f99ab8e5674715b65"
)

func sum(text string) string {
	s := sha256.Sum256([]byte(text))
	return hex.EncodeToString(s[:])
}

func TestTemplateChangesAreRecorded(t *testing.T) {
	if got := sum(promptTemplate + verifierSection); got != promptTemplateSum {
		t.Errorf("worker-prompt.md or verifierSection changed. Record the change in templates.go: a "+
			"TemplateChange in promptChanges with the next version (%d), the section it changed, what changed "+
			"and a marker only the new text has; then set promptTemplateSum to %s",
			latestVersion(promptChanges)+1, got)
	}
	if got := sum(verifierTemplate); got != verifierTemplateSum {
		t.Errorf("verifier.md changed. Record the change in templates.go: a TemplateChange in verifierChanges "+
			"with the next version (%d), the section it changed, what changed and a marker only the new text "+
			"has; then set verifierTemplateSum to %s", latestVersion(verifierChanges)+1, got)
	}
}

// Each change's marker is in the template now, in the section it names: else every project would lack it.
func TestTemplateMarkersAreInTheirSections(t *testing.T) {
	prompt := currentPrompt(verifierSection, "")
	parts := []Suite{{Name: "unit", Command: "go test ./...", Targeted: "go test ./<package>/..."}}
	for name, c := range map[string]struct {
		texts   []string
		changes []TemplateChange
	}{
		"worker prompt": {[]string{prompt}, promptChanges},
		"verifier": {[]string{VerifierText(nil, nil, nil, nil, nil),
			VerifierText(nil, nil, nil, &parts, &[]Suite{})}, verifierChanges},
	} {
		for _, text := range c.texts {
			all := sections(text)
			for _, ch := range c.changes {
				i := indexOf(all, ch.Section)
				if i < 0 || !strings.Contains(all[i].text, ch.Marker) {
					t.Errorf("%s: the section %q lacks the marker %q of version %d", name, ch.Section, ch.Marker,
						ch.Version)
				}
			}
		}
	}
}

// templatesRepo is a project set up on 2026-10-16, as tutti was: the worker prompt and the verifier
// from the templates of then, with the project's own edits, and settings.json recording no versions.
func templatesRepo(t *testing.T, verifier bool) string {
	t.Helper()
	repo, _ := gitRepo(t)
	prompt := readTestdata(t, "worker-prompt-2026-10-16.md") + "\n## Project notes\n- Use pnpm, never npm.\n"
	if verifier {
		prompt += readTestdata(t, "verify-section-2026-10-16.md")
		old := template.Must(template.New("v").Parse(readTestdata(t, "verifier-2026-10-16.md.tmpl")))
		var b strings.Builder
		if err := old.Execute(&b, struct {
			Stack, Fast, Full      []string
			FastRunner, FullRunner string
		}{[]string{"TypeScript", "vitest"}, []string{"pnpm lint"}, nil, FastRunner, FullRunner}); err != nil {
			t.Fatal(err)
		}
		writeRepoFile(t, repo, VerifierPath, b.String())
	}
	writeRepoFile(t, repo, Dir+"/"+promptName, prompt)
	writeRepoFile(t, repo, Dir+"/"+SettingsName, `{"check_fast": "scripts/check-fast.sh", "concurrent": 2}`+"\n")
	if _, err := ApplyRunners(context.Background(), repo, Choice{Fast: &[]Suite{{Command: "pnpm lint"}},
		Full: &[]Suite{}}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "templates", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeRepoFile(t *testing.T, repo, rel, text string) {
	t.Helper()
	p := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnOlderWorkerPromptIsNoticed(t *testing.T) {
	repo := templatesRepo(t, false)
	notes := TemplateNotes(repo)
	if len(notes) != 1 {
		t.Fatalf("notes %q, want one, on the worker prompt", notes)
	}
	for _, want := range []string{
		".orchestra/worker-prompt.md is older than orchestra's worker prompt template",
		"the opening (a worker checks its own change and leaves the whole suite to the merge check; a tool, " +
			"service or permission a ticket needs of the machine goes in the requires list",
		"Close (a ticket closes once the worker's own checks pass)",
		"Working unattended (no to-do checklist is asked for",
		"orchestra init --update",
	} {
		if !strings.Contains(notes[0], want) {
			t.Errorf("the note lacks %q:\n%s", want, notes[0])
		}
	}
	// The changes it has, and the Verify section of a prompt that doesn't run the verifier, aren't named.
	for _, not := range []string{"throwaway", "blocked-by", "Verify"} {
		if strings.Contains(notes[0], not) {
			t.Errorf("the note names %q, which the prompt has or needn't:\n%s", not, notes[0])
		}
	}
}

func TestAnOlderVerifierIsNoticed(t *testing.T) {
	repo := templatesRepo(t, true)
	notes := TemplateNotes(repo)
	if len(notes) != 2 {
		t.Fatalf("notes %q, want two, on the worker prompt and the verifier", notes)
	}
	if !strings.Contains(notes[0], "Verify (the worker gives the verifier the commit at which its checks passed") {
		t.Errorf("the worker prompt's note lacks its Verify section:\n%s", notes[0])
	}
	for _, want := range []string{
		VerifierPath + " is older than orchestra's verifier template",
		"the opening (its description asks", "Its checks (", "How to verify (",
		"Rules (a check gets Bash's longest timeout",
	} {
		if !strings.Contains(notes[1], want) {
			t.Errorf("the verifier's note lacks %q:\n%s", want, notes[1])
		}
	}
	if strings.Contains(notes[1], "throwaway") {
		t.Errorf("the verifier's note names the throwaway files, which its Rules have:\n%s", notes[1])
	}
}

func TestUpdateTemplatesKeepsTheProjectsOwnText(t *testing.T) {
	repo := templatesRepo(t, true)
	steps, updates, err := UpdateTemplates(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(updates) != 2 {
		t.Fatalf("updates %+v, want the worker prompt and the verifier", updates)
	}
	prompt := readRepoFile(t, repo, Dir+"/"+promptName)
	for _, want := range []string{
		"- Leave the whole suite to orchestra: its merge check, `scripts/check-fast.sh`, runs",
		"- Close the ticket only when your checks pass after your last change.",
		"commit at which your checks passed",
		"## Project notes\n- Use pnpm, never npm.\n",
		"keep the ticket's notes current, so you can carry on after it.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the updated prompt lacks %q:\n%s", want, prompt)
		}
	}
	for _, gone := range []string{"Check your work with", "and nothing of your reasoning", "<check command>",
		"todo checklist"} {
		if strings.Contains(prompt, gone) {
			t.Errorf("the updated prompt still has %q:\n%s", gone, prompt)
		}
	}
	if !strings.HasSuffix(prompt, verifierSection) || strings.Count(prompt, "## Verify") != 1 {
		t.Errorf("the Verify section isn't the template's, once, at the end:\n%s", prompt)
	}
	verifier := readRepoFile(t, repo, VerifierPath)
	for _, want := range []string{"- TypeScript\n- vitest\n", "  - `pnpm lint`\n", "Leave the whole suite to orchestra",
		"the commands you ran there"} {
		if !strings.Contains(verifier, want) {
			t.Errorf("the updated verifier lacks %q:\n%s", want, verifier)
		}
	}
	s, _, err := LoadSettings(repo)
	if err != nil || s.Templates == nil || *s.Templates != CurrentTemplates() || s.Concurrency != 2 {
		t.Errorf("settings %+v (templates %+v), %v; want the current versions recorded, the rest kept", s, s.Templates, err)
	}
	if notes := TemplateNotes(repo); len(notes) != 0 {
		t.Errorf("after the update, notes %q", notes)
	}
	diff := updates[0].Diff()
	for _, want := range []string{"--- a/.orchestra/worker-prompt.md\n", "\n@@ -", "\n-- Check your work with",
		"\n+- Leave the whole suite to orchestra"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the diff lacks %q:\n%s", want, diff)
		}
	}
	if strings.Contains(diff, "Project notes") {
		t.Errorf("the diff shows the project's own section, unchanged:\n%s", diff)
	}
	next := UpdateNext(steps, updates)
	if last := next[len(next)-1]; last != "Commit .orchestra/worker-prompt.md, "+VerifierPath+" and .orchestra/settings.json." {
		t.Errorf("next %q", next)
	}

	// Again, there is nothing to do.
	steps, updates, err = UpdateTemplates(repo)
	if err != nil || len(updates) != 0 || len(steps) != 2 || steps[0].Kind != StepKept || steps[1].Kind != StepKept {
		t.Errorf("a second update: steps %+v, updates %d, %v", steps, len(updates), err)
	}
}

// A project that records the version now has every change, though it worded a section its own way.
func TestARecordedVersionIsTakenAsIs(t *testing.T) {
	repo := templatesRepo(t, false)
	s, _, _ := LoadSettings(repo)
	s.Templates = &Templates{WorkerPrompt: latestVersion(promptChanges)}
	if err := SaveSettings(repo, s); err != nil {
		t.Fatal(err)
	}
	if notes := TemplateNotes(repo); len(notes) != 0 {
		t.Errorf("notes %q for a prompt recording the version now", notes)
	}
	s.Templates.WorkerPrompt = 2
	if err := SaveSettings(repo, s); err != nil {
		t.Fatal(err)
	}
	if notes := TemplateNotes(repo); len(notes) != 1 || !strings.Contains(notes[0], "Close") {
		t.Errorf("notes %q for a prompt recording version 2", notes)
	}
}

// A section the project's copy lacks, which the template changed, is added after the one before it.
func TestUpdatedAddsAMissingSection(t *testing.T) {
	current := "intro\n\n## A\na\n\n## B\nb new\n\n## C\nc\n"
	got := updated("intro\n\n## A\nmine\n\n## C\nc\n", current, []TemplateChange{{Section: "B"}})
	if want := "intro\n\n## A\nmine\n\n## B\nb new\n\n## C\nc\n"; got != want {
		t.Errorf("updated:\n%s\nwant:\n%s", got, want)
	}
}

func TestInitSaysAKeptPromptIsOlder(t *testing.T) {
	repo := templatesRepo(t, false)
	steps, err := Init(context.Background(), repo, PromptChecks{Check: FastRunner}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(steps[0].Detail, "older than orchestra's template, which has since changed the opening, "+
		"Close and Working unattended: orchestra init --update takes those sections") {
		t.Errorf("the worker prompt's step: %+v", steps[0])
	}
}

func TestApplyRecordsTheTemplatesItWrites(t *testing.T) {
	repo, _ := gitRepo(t)
	fast := []Suite{{Command: "go test ./..."}}
	c := Choice{Fast: &fast, Full: &[]Suite{}, Concurrent: 1, Verifier: true, Agent: "claude"}
	if _, err := Apply(context.Background(), repo, c, false); err != nil {
		t.Fatal(err)
	}
	s, _, err := LoadSettings(repo)
	if err != nil || s.Templates == nil || *s.Templates != CurrentTemplates() {
		t.Errorf("settings' templates %+v, %v; want %+v", s.Templates, err, CurrentTemplates())
	}
	if notes := TemplateNotes(repo); len(notes) != 0 {
		t.Errorf("notes %q for a project just set up", notes)
	}
}

func TestDiffShowsOnlyWhatChanged(t *testing.T) {
	var before, after strings.Builder
	for i := range 20 {
		line := "line " + string(rune('a'+i)) + "\n"
		before.WriteString(line)
		if i == 10 {
			after.WriteString("changed\n")
		} else {
			after.WriteString(line)
		}
	}
	got := TemplateUpdate{Path: "f.md", Before: before.String(), After: after.String()}.Diff()
	want := "--- a/f.md\n+++ b/f.md\n@@ -8,7 +8,7 @@\n line h\n line i\n line j\n-line k\n+changed\n line l\n line m\n line n\n"
	if got != want {
		t.Errorf("diff:\n%s\nwant:\n%s", got, want)
	}
	if d := (TemplateUpdate{Path: "f.md", Before: "a\n", After: "a\n"}).Diff(); d != "" {
		t.Errorf("diff of the same text: %q", d)
	}
}
