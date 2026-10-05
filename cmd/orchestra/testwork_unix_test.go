//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/project"
)

// trackerBd puts first on the PATH a bd that keeps the tickets it files: create gives each the next
// ID (t-1, t-2…), writes its arguments one a line to create.<ID> and prints the ID; list prints
// those filed, all open, whatever its filters. With failCreate, create fails. It returns its folder.
func trackerBd(t *testing.T, failCreate bool) string {
	t.Helper()
	dir := t.TempDir()
	fail := ""
	if failCreate {
		fail = "[ \"$1\" = create ] && { echo 'database is locked' >&2; exit 1; }\n"
	}
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
`+fail+`case "$1" in
create)
	n=$(( $(cat "$d/n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$d/n"
	printf '%s\n' "$@" > "$d/create.t-$n"
	for a; do case "$a" in --title=*) title="${a#--title=}";; esac; done
	printf '{"id":"t-%s","title":"%s","status":"open"}\n' "$n" "$title" >> "$d/db"
	printf '{"id":"t-%s"}' "$n";;
list)
	printf '['; [ -f "$d/db" ] && paste -sd, "$d/db" | tr -d '\n'; printf ']';;
esac
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

// created is the arguments bd create ran with for the ticket, one a line, or "" if it filed none.
func created(t *testing.T, dir, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "create."+id))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// details is the steps' kinds and details, one a line.
func details(steps []project.Step) string {
	var b strings.Builder
	for _, s := range steps {
		b.WriteString(map[project.StepKind]string{project.StepDone: "✓", project.StepKept: "•",
			project.StepCaution: "!", project.StepMissing: "✗"}[s.Kind] + " " + s.Detail + "\n")
	}
	return b.String()
}

// hasArgs fails the test unless the call has each argument, as a line of its own.
func hasArgs(t *testing.T, id, call string, args ...string) {
	t.Helper()
	for _, a := range args {
		if !strings.Contains("\n"+call, "\n"+a+"\n") {
			t.Errorf("%s was filed without %s:\n%s", id, a, call)
		}
	}
}

func branchOf(t *testing.T, repo string) string {
	t.Helper()
	b, err := git.Git{}.CurrentBranch(context.Background(), repo)
	if err != nil || b == "" {
		t.Fatalf("branch %q, %v", b, err)
	}
	return b
}

func TestInitFilesNoTestWorkForAManualCheck(t *testing.T) {
	dir := trackerBd(t, false)
	repo, _ := gitRepo(t)
	if steps := fileTestWork(context.Background(), repo, project.Choice{Tests: project.TestsManual}); steps != nil {
		t.Errorf("steps for a manual check:\n%s", details(steps))
	}
	if _, err := os.Stat(filepath.Join(dir, "n")); err == nil {
		t.Error("bd create ran for a manual check")
	}
}

func TestInitFilesTheTestsFromScratchOnce(t *testing.T) {
	dir := trackerBd(t, false)
	repo, _ := gitRepo(t)
	c := project.Choice{Tests: project.TestsScratch, Agent: "codex"}
	steps := fileTestWork(context.Background(), repo, c)
	want := "✓ filed t-1 (epic): The project's tests\n" +
		"✓ filed t-2 (P1, solo): Set up the test harness and a first suite\n"
	if got := details(steps); got != want {
		t.Fatalf("steps:\n%s\nwant\n%s", got, want)
	}
	hasArgs(t, "t-1", created(t, dir, "t-1"), "--type=epic", "--labels="+project.TestWorkLabel)
	setUp := created(t, dir, "t-2")
	hasArgs(t, "t-2", setUp, "--type=task", "--priority=1", "--parent=t-1",
		"--labels="+project.TestWorkLabel+","+dispatch.SoloLabel)
	for _, want := range []string{".agents/skills/create-check-suite/SKILL.md", "smoke tests", "FEATURES.md",
		"one P3 ticket per untested area", "passing on " + branchOf(t, repo)} {
		if !strings.Contains(setUp, want) {
			t.Errorf("t-2 doesn't say %q:\n%s", want, setUp)
		}
	}

	steps = fileTestWork(context.Background(), repo, c)
	want = "• t-1 is open: The project's tests; not filed again\n" +
		"• t-2 is open: Set up the test harness and a first suite; not filed again\n"
	if got := details(steps); got != want {
		t.Errorf("run again:\n%s\nwant\n%s", got, want)
	}
	if call := created(t, dir, "t-3"); call != "" {
		t.Errorf("run again, init filed:\n%s", call)
	}
}

func TestInitFilesTheRunnersPassingForTheSuitesFound(t *testing.T) {
	dir := trackerBd(t, false)
	repo, _ := gitRepo(t)
	base := branchOf(t, repo)
	steps := fileTestWork(context.Background(), repo, project.Choice{Tests: project.TestsFound})
	title := "Make scripts/check-fast.sh and scripts/check-full.sh pass on " + base
	if got, want := details(steps), "✓ filed t-1 (P1, solo): "+title+"\n"; got != want {
		t.Fatalf("steps:\n%s\nwant\n%s", got, want)
	}
	pass := created(t, dir, "t-1")
	hasArgs(t, "t-1", pass, "--title="+title, "--type=task", "--priority=1",
		"--labels="+project.TestWorkLabel+","+dispatch.SoloLabel)
	if strings.Contains(pass, "--parent") || strings.Contains(pass, "--deps") {
		t.Errorf("t-1 has a parent or a dependency:\n%s", pass)
	}
	for _, want := range []string{"skip", "bug ticket", "how long each takes"} {
		if !strings.Contains(pass, want) {
			t.Errorf("t-1 doesn't say %q:\n%s", want, pass)
		}
	}

	// With the untested areas, the same ticket goes under an epic, which init files now, and the map
	// waits for it; the ticket already open isn't filed again.
	steps = fileTestWork(context.Background(), repo, project.Choice{Tests: project.TestsFound, FileUntested: true})
	want := "✓ filed t-2 (epic): The project's tests\n" +
		"• t-1 is open: " + title + "; not filed again\n" +
		"✓ filed t-3 (P2): Map the untested areas\n"
	if got := details(steps); got != want {
		t.Fatalf("steps:\n%s\nwant\n%s", got, want)
	}
	mapping := created(t, dir, "t-3")
	hasArgs(t, "t-3", mapping, "--type=task", "--priority=2", "--parent=t-2", "--deps=blocked-by:t-1",
		"--labels="+project.TestWorkLabel)
	for _, want := range []string{".claude/skills/create-check-suite/SKILL.md", "FEATURES.md", "P3 ticket"} {
		if !strings.Contains(mapping, want) {
			t.Errorf("t-3 doesn't say %q:\n%s", want, mapping)
		}
	}
}

func TestInitFilesTheUntestedAreasUnderAnEpic(t *testing.T) {
	dir := trackerBd(t, false)
	repo, _ := gitRepo(t)
	c := project.Choice{Tests: project.TestsFound, FileUntested: true}
	steps := fileTestWork(context.Background(), repo, c)
	if len(steps) != 3 {
		t.Fatalf("steps:\n%s", details(steps))
	}
	hasArgs(t, "t-1", created(t, dir, "t-1"), "--type=epic")
	hasArgs(t, "t-2", created(t, dir, "t-2"), "--priority=1", "--parent=t-1",
		"--labels="+project.TestWorkLabel+","+dispatch.SoloLabel)
	hasArgs(t, "t-3", created(t, dir, "t-3"), "--priority=2", "--parent=t-1", "--deps=blocked-by:t-2")

	if got := details(fileTestWork(context.Background(), repo, c)); strings.Contains(got, "✓") ||
		created(t, dir, "t-4") != "" {
		t.Errorf("run again, init filed:\n%s", got)
	}
}

func TestInitSaysWhichTestTicketsItCouldNotFile(t *testing.T) {
	trackerBd(t, true)
	repo, _ := gitRepo(t)
	steps := fileTestWork(context.Background(), repo, project.Choice{Tests: project.TestsScratch})
	if len(steps) != 1 || steps[0].Kind != project.StepCaution {
		t.Fatalf("steps:\n%s", details(steps))
	}
	for _, want := range []string{"not filed", `"The project's tests", "Set up the test harness and a first suite"`,
		"database is locked", "orchestra init again"} {
		if !strings.Contains(steps[0].Detail, want) {
			t.Errorf("the step doesn't say %q: %s", want, steps[0].Detail)
		}
	}
}
