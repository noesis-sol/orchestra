package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/git"
)

// Tickets running side by side each add their changelog entry at the top of the same list, and
// git can't order two sides' added lines, so the second ticket's rebase stops on a conflict.
// CHANGELOG.md merge=union in .gitattributes has git keep both sides' lines instead; init offers it.
const (
	changelogName  = "CHANGELOG.md"
	attributesName = ".gitattributes"
	unionLine      = changelogName + " merge=union"
)

// OffersUnion reports whether init should offer to merge the changelog by union: the project keeps
// a CHANGELOG.md, and .gitattributes doesn't merge it by union yet.
func OffersUnion(ctx context.Context, repo string) bool {
	return fileExists(filepath.Join(repo, changelogName)) && !mergesByUnion(ctx, repo)
}

// mergesByUnion reports whether git merges the repository's CHANGELOG.md by union, however
// .gitattributes says so (CHANGELOG.md merge=union, *.md merge=union, …).
func mergesByUnion(ctx context.Context, repo string) bool {
	merge, err := git.Git{}.Attribute(ctx, repo, "merge", changelogName)
	return err == nil && merge == "union"
}

// ApplyUnion adds CHANGELOG.md merge=union to .gitattributes when the choice says so, and returns
// the step for init's summary; ok is false when the project has no CHANGELOG.md.
func ApplyUnion(ctx context.Context, repo string, c Choice) (s Step, ok bool, err error) {
	if !fileExists(filepath.Join(repo, changelogName)) {
		return Step{}, false, nil
	}
	const label = "changelog"
	const why = "two tickets adding entries at the same spot"
	switch {
	case mergesByUnion(ctx, repo):
		return Step{Kind: StepKept, Label: label,
			Detail: changelogName + " merges by union: " + why + " keep both"}, true, nil
	case c.UnionUnasked:
		return Step{Kind: StepCaution, Label: label, Detail: "not asked (no terminal): with " + why +
			", the second one's rebase conflicts; --changelog-union adds " + unionLine + " to " + attributesName +
			" so git keeps both sides' lines"}, true, nil
	case !c.Union:
		return Step{Kind: StepKept, Label: label,
			Detail: attributesName + " left as it is: " + why + " will conflict"}, true, nil
	}
	p := filepath.Join(repo, attributesName)
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return Step{}, true, err
	}
	text := string(b)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.WriteFile(p, []byte(text+unionLine+"\n"), 0o644); err != nil {
		return Step{}, true, err
	}
	return Step{Kind: StepDone, Label: label, Detail: "added " + unionLine + " to " + attributesName + ": with " + why +
		", git keeps both sides' lines instead of stopping on a conflict"}, true, nil
}
