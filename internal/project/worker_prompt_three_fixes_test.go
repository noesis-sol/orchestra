package project

import (
	"os"
	"strings"
	"testing"
)

// threeFixesRule is the worker prompt's rule on a problem that resists three fixes, its lines joined.
const threeFixesRule = "When the same problem has resisted three different fixes, stop: note on the ticket what you " +
	"tried and what each attempt showed (`bd update TICKET_ID --append-notes \"...\"`), defer it " +
	"(`bd defer TICKET_ID`), and stop. Three different approaches count, not three runs of the same " +
	"command; rerunning a check that failed for reasons outside your change isn't an attempt."

// Both worker prompts, the template init writes and this repository's own, have under Close that a
// worker stuck on one problem after three different fixes notes what it tried, defers its ticket and stops.
func TestWorkerPromptDefersAfterThreeFailedFixes(t *testing.T) {
	own, err := os.ReadFile("../../.orchestra/worker-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{"template": promptTemplate, "repository's": string(own)} {
		all := sections(prompt)
		i := indexOf(all, "Close")
		if i < 0 {
			t.Errorf("the %s prompt has no Close:\n%s", name, prompt)
			continue
		}
		if closing := strings.Join(strings.Fields(all[i].text), " "); !strings.Contains(closing, threeFixesRule) {
			t.Errorf("the %s prompt's Close lacks %q:\n%s", name, threeFixesRule, all[i].text)
		}
	}
}

// A project whose prompt came from version 10 is told at a run's start that Close lacks the rule, and
// 'orchestra init --update' adds it.
func TestAVersion10PromptIsToldOfThreeFixes(t *testing.T) {
	repo, _ := gitRepo(t)
	current := currentPrompt("", FastRunner)
	before, rest, ok := strings.Cut(current, "- When the same problem")
	_, after, found := strings.Cut(rest, "- If you cannot finish")
	if !ok || !found {
		t.Fatalf("the template lacks the rule, or the line after it:\n%s", current)
	}
	old := before + "- If you cannot finish" + after
	if strings.Contains(old, "resisted three") || old == current {
		t.Fatalf("the version-10 prompt still has the rule:\n%s", old)
	}
	writeRepoFile(t, repo, Dir+"/"+promptName, old)
	writeRepoFile(t, repo, Dir+"/"+SettingsName,
		`{"check_fast": "scripts/check-fast.sh", "templates": {"worker_prompt": 10}}`+"\n")

	notes := TemplateNotes(repo)
	if len(notes) != 1 || !strings.Contains(notes[0], "which has since changed Close (a problem that has "+
		"resisted three different fixes is noted on the ticket, with what each attempt showed, and the ticket "+
		"deferred): orchestra init --update") {
		t.Fatalf("notes %q, want one naming Close's three fixes alone", notes)
	}
	if _, updates, err := UpdateTemplates(repo); err != nil || len(updates) != 1 {
		t.Fatalf("update: %d files, %v", len(updates), err)
	}
	got, err := os.ReadFile(repo + "/" + Dir + "/" + promptName)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(string(got)), " "), threeFixesRule) {
		t.Errorf("the updated prompt lacks the rule:\n%s", got)
	}
	if notes := TemplateNotes(repo); len(notes) != 0 {
		t.Errorf("notes %q after the update", notes)
	}
}
