package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"unicode/utf8"

	"github.com/noesis-sol/orchestra/internal/git"
)

// A spec run (orchestra --spec <file>) builds what a spec describes: a Markdown or plain-text file,
// broken down into tickets that workers then work on. Workers see only what is committed, so the
// spec is committed before it is broken down: a file outside the repository as a copy in
// .orchestra/specs/, one inside it where it is. Its path in the repository is what names it from
// one run to the next.

// SpecsDir is the folder under Dir that a spec from outside the repository is copied to.
const SpecsDir = "specs"

// The metadata keys a spec's root epic carries besides the spec's path (dispatch.SpecKey), which
// every issue its breakdown files carries: the committed content's SHA-256 and the commit that added
// or last changed it, as Spec has them.
const (
	SpecSHA256Key = "spec_sha256"
	SpecCommitKey = "spec_commit"
)

// A spec is broken down in a Claude Code session in the main checkout, its instructions
// (spec-prompt.md) appended to Claude Code's system prompt from .orchestra/run/: headless, with
// nobody there, or refined with the user first (--refine), in a pane beside orchestra's or on its
// own terminal. A feature, its description given as text, is broken down as a spec refined with the
// user, in the instructions' feature mode, its interview method adapted from mattpocock/skills under
// the MIT License, whose notice they carry. The instructions are a text/template, which gives each
// its sections.
//
//go:embed spec-prompt.md
var specPromptText string

// specPrompt is spec-prompt.md, parsed.
var specPrompt = template.Must(template.New(specPromptName).Option("missingkey=error").Parse(specPromptText))

// The breakdown's files in .orchestra/run/ of the main checkout.
const (
	specPromptName = "spec-prompt.md" // its instructions
	// SpecName is where a refinement hands its root epic back to orchestra, {"epic":"<id>"}, once it
	// has filed the spec's tree.
	SpecName = "spec.json"
	// SpecUpdateName is what a breakdown in update mode reads besides the spec: how the spec changed
	// since its tree was filed, and that tree (see WriteSpecUpdate).
	SpecUpdateName = "spec-update.md"
)

// RefineMode is what a breakdown with the user breaks down, and so which sections its instructions
// have and which file it hands its root epic back in.
type RefineMode int

// The modes of a breakdown with the user.
const (
	RefineSpec    RefineMode = iota // a spec whose tree isn't filed yet; it hands back in spec.json
	RefineUpdate                    // a spec changed since its tree was filed (see WriteSpecPrompt); spec.json
	RefineFeature                   // a feature talked through, its description given as text; feature.json
)

// handBack is the file in .orchestra/run/ that a breakdown in mode m hands its root epic back in.
func (m RefineMode) handBack() string {
	if m == RefineFeature {
		return FeatureName
	}
	return SpecName
}

// specPromptData picks spec-prompt.md's sections: Refine the user's over the headless ones,
// OnTerminal what a session on orchestra's own terminal adds, Update the sections that update a tree
// filed for an earlier version of the spec, and Feature, with Refine alone, those of a feature.
type specPromptData struct{ Refine, OnTerminal, Update, Feature bool }

// TreeInAnswer tells whether the breakdown files the root epic and the environment ticket alone, and
// gives the rest of the tree in its answer for orchestra to file: headless, for a spec broken down for
// the first time.
func (d specPromptData) TreeInAnswer() bool { return !d.Refine && !d.Update }

// refined is the data of a breakdown with the user in mode m, on orchestra's own terminal with
// onTerminal.
func (m RefineMode) refined(onTerminal bool) specPromptData {
	return specPromptData{Refine: true, OnTerminal: onTerminal, Update: m == RefineUpdate, Feature: m == RefineFeature}
}

// WriteSpecPrompt writes the instructions of a spec's headless breakdown to .orchestra/run/ in the
// main checkout repo, over an earlier breakdown's, and returns their path. With update, the spec
// changed since its tree was filed: the breakdown updates that tree for the change, from what
// WriteSpecUpdate wrote.
func WriteSpecPrompt(repo string, update bool) (string, error) {
	return writeSpecPrompt(repo, specPromptData{Update: update})
}

// WriteRefinePrompt readies the main checkout repo for a breakdown with the user in mode m: a spec's
// refinement (--refine), new or an update, or a feature's. It removes the file an earlier breakdown
// in that mode handed back in (spec.json, or feature.json for a feature), so that only this one's
// counts, and writes the instructions for a session in a pane beside orchestra's to .orchestra/run/.
// It returns their path.
func WriteRefinePrompt(repo string, m RefineMode) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	err = RemoveRun(root, repo, RunPath(m.handBack()))
	_ = root.Close() // nothing written
	if err != nil {
		return "", err
	}
	return writeSpecPrompt(repo, m.refined(false))
}

// WriteTerminalRefine writes the instructions for a breakdown with the user on orchestra's own
// terminal, in mode m, over those WriteRefinePrompt wrote to the main checkout repo, and returns
// their path, the same. orchestra closes a session in a pane beside its own once it has handed the
// root epic back, but can only wait for one on its terminal to end: these instructions add that
// claude tells the user to type /exit then.
func WriteTerminalRefine(repo string, m RefineMode) (string, error) {
	return writeSpecPrompt(repo, m.refined(true))
}

// writeSpecPrompt writes the breakdown's instructions, with the sections d picks, to .orchestra/run/
// in the main checkout repo, over an earlier breakdown's, and returns their path.
func writeSpecPrompt(repo string, d specPromptData) (string, error) {
	var text bytes.Buffer
	if err := specPrompt.Execute(&text, d); err != nil {
		return "", fmt.Errorf("cannot make the breakdown's instructions: %w", err)
	}
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := RunPath(specPromptName)
	if err := WriteRun(root, repo, rel, text.Bytes(), 0o644); err != nil {
		return "", err
	}
	return filepath.Join(repo, rel), nil
}

// WriteSpecUpdate writes text, what a breakdown in update mode reads besides the spec (the change to
// the spec since its tree was filed, and that tree), to .orchestra/run/ in the main checkout repo,
// over an earlier update's, and returns its path, relative to repo and slash-separated, as the
// breakdown's first message names it.
func WriteSpecUpdate(repo string, text []byte) (string, error) {
	root, err := OpenRun(repo)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	rel := RunPath(SpecUpdateName)
	if err := WriteRun(root, repo, rel, text, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// ErrSpecEmpty is ReadSpec's error for a spec with nothing but whitespace in it.
var ErrSpecEmpty = errors.New("the spec is empty")

// ErrSpecNotText is ReadSpec's error for a file that isn't text in UTF-8: one with a NUL byte, or a
// byte sequence UTF-8 doesn't have.
var ErrSpecNotText = errors.New("the spec isn't text in UTF-8")

// ErrSpecInGitDir is CommitSpec's error for a spec inside the repository's git directory, which git
// keeps no file of.
var ErrSpecInGitDir = errors.New("the spec is inside the repository's .git folder")

// Spec is a spec as committed for a run.
type Spec struct {
	Path   string // its path in the repository, slash-separated: what names it from run to run
	SHA256 string // its content's SHA-256, in hex
	Commit string // the commit, a full hash, that added it or last changed it
}

// Name is the spec's file name, as commit messages give it.
func (s Spec) Name() string { return path.Base(s.Path) }

// ShortSHA256 is the start of the spec's SHA-256, as the log gives it.
func (s Spec) ShortSHA256() string { return s.SHA256[:min(12, len(s.SHA256))] }

// ReadSpec reads the spec file: text in UTF-8, with something besides whitespace in it. A file that
// isn't is ErrSpecEmpty or ErrSpecNotText; one that can't be read is the os package's error (a
// folder too, as reading one fails).
func ReadSpec(file string) ([]byte, error) {
	b, err := os.ReadFile(file)
	switch {
	case err != nil:
		return nil, err
	case len(bytes.TrimSpace(b)) == 0:
		return nil, ErrSpecEmpty
	case bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b):
		return nil, ErrSpecNotText
	}
	return b, nil
}

// SpecPath returns where the spec file is kept in the repository whose main checkout is repo,
// slash-separated: its own path, relative to repo, when it is inside it (inside), else its file
// name in SpecsDir under Dir. Symlinks are followed: a link into the repository is the file it
// leads to, and one inside it leading out is a file outside.
func SpecPath(repo, file string) (rel string, inside bool) {
	top, full := resolved(repo), resolved(file)
	if r, err := filepath.Rel(top, full); err == nil && filepath.IsLocal(r) {
		return filepath.ToSlash(r), true
	}
	// macOS's filesystems usually ignore case: the repository's folder may be typed in another.
	if runtime.GOOS == "darwin" && len(full) > len(top)+1 && strings.EqualFold(full[:len(top)], top) &&
		full[len(top)] == filepath.Separator {
		return filepath.ToSlash(full[len(top)+1:]), true
	}
	return path.Join(Dir, SpecsDir, filepath.Base(file)), false
}

// resolved is p, which exists, absolute and with its symlinks resolved; as far as that can be done.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// InGitDir reports whether rel, a path in the repository as SpecPath gives it, is inside its .git
// folder.
func InGitDir(rel string) bool {
	first, _, _ := strings.Cut(rel, "/")
	return strings.EqualFold(first, ".git")
}

// CommitSpec keeps the spec file in the repository whose main checkout is repo, at SpecPath, and
// commits that path alone on the branch checked out when it is new, untracked or differs from what
// is committed; committed says whether it made that commit. A file outside the repository is copied
// there first, over the copy an earlier run made of it, and the commit records where from; one that
// would take the copy of a spec from elsewhere is *SpecTakenError (see CheckSpecSource). The spec is
// read again (see ReadSpec), and its SHA-256 is that content's.
//
// It is called before a run's loop starts, with the run lock held: nothing else of orchestra's
// writes to the repository meanwhile.
func CommitSpec(ctx context.Context, repo, file string) (s Spec, committed bool, err error) {
	text, err := ReadSpec(file)
	if err != nil {
		return Spec{}, false, err
	}
	rel, inside := SpecPath(repo, file)
	if inside && InGitDir(rel) {
		return Spec{}, false, ErrSpecInGitDir
	}
	sum := sha256.Sum256(text)
	s = Spec{Path: rel, SHA256: hex.EncodeToString(sum[:])}
	source := ""
	if !inside {
		if err := copyOutside(ctx, repo, file, rel, text); err != nil {
			return Spec{}, false, err
		}
		source = sourceTrailer(file)
	}
	g := git.Git{}
	changed, err := g.PathChanged(ctx, repo, rel)
	if err != nil {
		return Spec{}, false, fmt.Errorf("cannot tell whether %s is committed: %w", rel, err)
	}
	if !changed {
		if s.Commit, err = g.LastCommit(ctx, repo, rel); err != nil {
			return Spec{}, false, fmt.Errorf("cannot find the commit that added %s: %w", rel, err)
		}
		if s.Commit == "" {
			return Spec{}, false, fmt.Errorf("cannot find the commit that added %s", rel)
		}
		return s, false, nil
	}
	verb := "add"
	if blob, _ := g.Head(ctx, repo, "HEAD:"+rel); blob != "" { // the message's verb only: "add" on failure
		verb = "update"
	}
	msg := fmt.Sprintf("orchestra: %s spec %s\n\nThe spec orchestra --spec builds, at %s, kept where the workers on "+
		"its tickets can read it.\n\nsha256 %s\n%s", verb, s.Name(), rel, s.SHA256, source)
	if s.Commit, err = g.CommitPath(ctx, repo, rel, msg); err != nil {
		return Spec{}, false, fmt.Errorf("cannot commit %s: %w", rel, err)
	}
	return s, true, nil
}

// copyOutside copies text, the spec file from outside the repository repo, to rel there, once
// CheckSpecSource has found that no other spec's copy is kept at rel.
func copyOutside(ctx context.Context, repo, file, rel string, text []byte) error {
	if err := CheckSpecSource(ctx, repo, file); err != nil {
		return err
	}
	return copySpec(repo, rel, text)
}

// copySpec writes text, a spec from outside the repository repo, to rel there, in SpecsDir. It is
// written through an os.Root at the checkout, so a symlink can't lead it out of the repository.
func copySpec(repo, rel string, text []byte) error {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return fmt.Errorf("cannot copy the spec into %s: %w", repo, err)
	}
	defer func() { _ = root.Close() }() // the copy is written and renamed, or not, by then
	dir := filepath.FromSlash(path.Dir(rel))
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cannot copy the spec to %s: %w", rel, err)
	}
	sub, err := root.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("cannot copy the spec to %s: %w", rel, err)
	}
	defer func() { _ = sub.Close() }()
	if err := writeRoot(sub, path.Base(rel), text, 0o644); err != nil {
		return fmt.Errorf("cannot copy the spec to %s: %w", rel, err)
	}
	return nil
}
