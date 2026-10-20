package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// init writes the worker prompt and the verifier from orchestra's templates, and from then on they are
// the project's: init keeps them as they are. So that a project still hears of what orchestra changes
// in its templates later, settings.json records the version of each template its copy came from
// (Templates), and the changes below say, section by section, what each later version changed. A run
// says at its start which sections a project's copy lacks (TemplateNotes), and 'orchestra init
// --update' rewrites those sections from the templates, keeping the rest of the file (UpdateTemplates).
//
// A change to worker-prompt.md, verifierSection or verifier.md is recorded here, as a new version of
// its template: the test that pins the templates fails until it is.

// TemplateChange is one change to a template, made by one of its versions.
type TemplateChange struct {
	Version int    // the template's version that made it, counting from 1
	Section string // the heading of the section it changed (its "## " line's text), "" for the opening
	What    string // what changed, as the notice says it
	// Marker is words only the changed text has: a project's copy that has them has the change, though
	// it records an older version (copied by hand) or none (written before versions were recorded).
	Marker string
}

// promptChanges are the changes to the worker prompt's template, worker-prompt.md with verifierSection.
var promptChanges = []TemplateChange{
	{1, "", "a follow-up that builds on the ticket waits for it to merge (--deps blocked-by)",
		"--deps blocked-by:TICKET_ID"},
	{1, "Close", "a ticket that needs another's work waits for it to merge", "Waiting on <other> to merge"},
	{1, "Working unattended", "a wait for another ticket to merge is among the stops",
		"a wait for another ticket to merge"},
	{2, "", "throwaway files stay in the scratchpad, never removed with an rm of a variable or a glob",
		"Put throwaway files in a new folder of your scratchpad"},
	{3, "", "a worker checks its own change and leaves the whole suite to the merge check",
		"Leave the whole suite to orchestra"},
	{3, "Close", "a ticket closes once the worker's own checks pass", "Close the ticket only when your checks pass"},
	{4, verifyHeading, "the worker gives the verifier the commit at which its checks passed and the commands " +
		"it ran there, so that it needn't run them again", "commit at which your checks passed"},
}

// verifierChanges are the changes to the verifier's template, verifier.md.
var verifierChanges = []TemplateChange{
	{1, "Rules", "throwaway files stay in the scratchpad, never removed with an rm of a variable or a glob",
		"Put throwaway files in a new folder of your scratchpad"},
	{2, "", "its description asks for the commit at which the worker's checks passed and the commands it ran",
		"the commands you ran there"},
	{2, "Its checks", "how to run each suite on what a change touches", "on what a change touches"},
	{2, "How to verify", "it quotes the worker's passing checks at the same commit rather than run them again, " +
		"and runs the suites only on what the change touches", "Leave the whole suite to orchestra"},
	{3, "Rules", "a check gets Bash's longest timeout, and one that takes longer runs in the background, waited " +
		"on with short waits", "Give a check its time"},
}

// verifyHeading is the heading of the worker prompt's section that runs the verifier.
const verifyHeading = "Verify"

// Templates are the versions of orchestra's templates a project's worker prompt and verifier came from,
// as settings.json records them; 0 for one written before versions were recorded.
type Templates struct {
	WorkerPrompt int `json:"worker_prompt,omitempty"`
	Verifier     int `json:"verifier,omitempty"`
}

// CurrentTemplates are the versions of orchestra's templates now.
func CurrentTemplates() Templates {
	return Templates{WorkerPrompt: latestVersion(promptChanges), Verifier: latestVersion(verifierChanges)}
}

// latestVersion is the version of the last of changes.
func latestVersion(changes []TemplateChange) int {
	v := 0
	for _, c := range changes {
		v = max(v, c.Version)
	}
	return v
}

// section is a part of a template or of a project's copy of it: the opening, before the first "## "
// heading (title ""), or a heading and what follows it up to the next.
type section struct{ title, text string }

// sections splits text at its "## " headings.
func sections(text string) []section {
	all := []section{{}}
	for line := range strings.SplitAfterSeq(text, "\n") {
		if title, ok := strings.CutPrefix(line, "## "); ok {
			all = append(all, section{title: strings.TrimSpace(title)})
		}
		all[len(all)-1].text += line
	}
	return all
}

// joinSections is the text of all, in order.
func joinSections(all []section) string {
	var b strings.Builder
	for _, s := range all {
		b.WriteString(s.text)
	}
	return b.String()
}

// indexOf is the place of the section titled title in all, or -1.
func indexOf(all []section, title string) int {
	return slices.IndexFunc(all, func(s section) bool { return s.title == title })
}

// templateFile is a file of the project's that orchestra wrote from one of its templates.
type templateFile struct {
	path     string // from the repository's top
	template string // the template's name, as the notice says it: the worker prompt's, the verifier's
	changes  []TemplateChange
	// optional reports whether a project's copy, text, may go without the section titled title: the
	// worker prompt's Verify where it doesn't run the verifier. A change to such a section isn't the
	// copy's to take.
	optional func(text, title string) bool
}

var (
	promptFile = templateFile{path: Dir + "/" + promptName, template: "worker prompt", changes: promptChanges,
		optional: func(text, title string) bool { return title == verifyHeading && !usesVerifier(text) }}
	verifierFile = templateFile{path: VerifierPath, template: "verifier", changes: verifierChanges,
		optional: func(string, string) bool { return false }}
)

// missing are the changes of f's template that text, the project's copy, which came from version
// recorded of it, lacks: those since that version, unless the copy has their marker.
func (f templateFile) missing(text string, recorded int) []TemplateChange {
	have := sections(text)
	var left []TemplateChange
	for _, c := range f.changes {
		switch {
		case c.Version <= recorded, strings.Contains(text, c.Marker):
		case indexOf(have, c.Section) < 0 && f.optional(text, c.Section):
		default:
			left = append(left, c)
		}
	}
	return left
}

// sectionName is how the notice names the section titled title.
func sectionName(title string) string {
	if title == "" {
		return "the opening"
	}
	return title
}

// changedSections names the sections changes changed, each with what changed in it, in the order they
// first come: "the opening (a; b) and Close (c)".
func changedSections(changes []TemplateChange) string {
	var titles []string
	what := map[string][]string{}
	for _, c := range changes {
		if !slices.Contains(titles, c.Section) {
			titles = append(titles, c.Section)
		}
		what[c.Section] = append(what[c.Section], c.What)
	}
	var parts []string
	for _, t := range titles {
		parts = append(parts, sectionName(t)+" ("+strings.Join(what[t], "; ")+")")
	}
	return joinAnd(parts)
}

// sectionTitles names the sections changes changed, in the order they first come.
func sectionTitles(changes []TemplateChange) []string {
	var names []string
	for _, c := range changes {
		if n := sectionName(c.Section); !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return names
}

// OutdatedTemplate is a file of the project's that lacks changes orchestra made to its template since
// the project's copy was written.
type OutdatedTemplate struct {
	Path    string // from the repository's top
	file    templateFile
	text    string // the project's copy
	Missing []TemplateChange
}

// Note says what the file lacks and how to take it, for a run's start and init's summary.
func (o OutdatedTemplate) Note() string {
	return fmt.Sprintf("%s is older than orchestra's %s template, which has since changed %s: orchestra init "+
		"--update rewrites those sections from the template, keeping the rest of the file, and shows the diff "+
		"to commit", o.Path, o.file.template, changedSections(o.Missing))
}

// OutdatedTemplates are the project's worker prompt and verifier, those it has, that lack changes
// orchestra made to their templates since they were written, as settings.json records it. A file
// orchestra can't read is left out: the run and init each say so where they read it.
func OutdatedTemplates(repo string) []OutdatedTemplate {
	s, _, err := LoadSettings(repo)
	if err != nil {
		return nil // the run and init stop on settings they can't read
	}
	recorded := Templates{}
	if s.Templates != nil {
		recorded = *s.Templates
	}
	var out []OutdatedTemplate
	for _, f := range []struct {
		file    templateFile
		version int
	}{{promptFile, recorded.WorkerPrompt}, {verifierFile, recorded.Verifier}} {
		b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(f.file.path)))
		if err != nil {
			continue
		}
		if missing := f.file.missing(string(b), f.version); len(missing) > 0 {
			out = append(out, OutdatedTemplate{Path: f.file.path, file: f.file, text: string(b), Missing: missing})
		}
	}
	return out
}

// keptNote is what init's step says of the project's copy at path, which it keeps as it is, where the
// copy lacks changes to its template: which sections, and how to take them; "" where it lacks none.
func keptNote(repo, path string) string {
	for _, o := range OutdatedTemplates(repo) {
		if o.Path == path {
			return "; older than orchestra's template, which has since changed " +
				joinAnd(sectionTitles(o.Missing)) + ": orchestra init --update takes those sections"
		}
	}
	return ""
}

// TemplateNotes are what a run says at its start of the project's worker prompt and verifier where
// they are older than orchestra's templates: a line each (see OutdatedTemplate.Note).
func TemplateNotes(repo string) []string {
	var notes []string
	for _, o := range OutdatedTemplates(repo) {
		notes = append(notes, o.Note())
	}
	return notes
}

// updated is text, a project's copy of a template, with the sections changes changed taken from
// current, the template as init would write it for the project now: each replaced where text has it,
// else added after the section before it in current that text has. The other sections, the project's
// own among them, are kept as they are.
func updated(text, current string, changes []TemplateChange) string {
	have, want := sections(text), sections(current)
	for _, title := range uniqueSections(changes) {
		w := indexOf(want, title)
		if w < 0 {
			continue // a section the template no longer has
		}
		if i := indexOf(have, title); i >= 0 {
			have[i].text = want[w].text
			continue
		}
		at := len(have)
		for p := w - 1; p >= 0; p-- {
			if i := indexOf(have, want[p].title); i >= 0 {
				at = i + 1
				break
			}
		}
		if at == len(have) && !strings.HasSuffix(have[at-1].text, "\n") {
			have[at-1].text += "\n"
		}
		have = slices.Insert(have, at, want[w])
	}
	return joinSections(have)
}

// uniqueSections are the titles of the sections changes changed, in the order they first come.
func uniqueSections(changes []TemplateChange) []string {
	var titles []string
	for _, c := range changes {
		if !slices.Contains(titles, c.Section) {
			titles = append(titles, c.Section)
		}
	}
	return titles
}

// TemplateUpdate is one file 'orchestra init --update' rewrote: its text before and after, and the
// sections it took from the template.
type TemplateUpdate struct {
	Path          string // from the repository's top
	Before, After string
	Sections      []string
}

// currentPrompt is the worker prompt init would write for the project in repo now, whose merge check
// is check (FastRunner where settings.json names none): the template, with the Verify section where
// text, the project's prompt, runs the verifier. Init doesn't know the suites here, which the scout
// found: the lines on running each on part of the project say where they are.
func currentPrompt(text, check string) string {
	if check == "" {
		check = FastRunner
	}
	p := fillTemplate(promptTemplate, PromptChecks{Check: check})
	if usesVerifier(text) {
		p += verifierSection
	}
	return p
}

// currentVerifier is the verifier init would write for the project in repo now, its suites read from
// the runners. Its stack, which the scout found, is the project's copy's: the template never changes
// that section.
func currentVerifier(repo string) (string, error) {
	fast, err := runnerSuites(repo, FastRunner)
	if err != nil {
		return "", err
	}
	full, err := runnerSuites(repo, FullRunner)
	if err != nil {
		return "", err
	}
	return VerifierText(nil, fast, full, nil, nil), nil
}

// ErrNotSetUp is UpdateTemplates' error for a repository init hasn't set up: no worker prompt in
// .orchestra/ to update.
var ErrNotSetUp = errors.New("no " + Dir + "/" + promptName + " to update: run orchestra init first")

// UpdateTemplates brings the project's worker prompt and verifier in repo up to date with orchestra's
// templates: in each, the sections the templates changed since the project's copy was written are
// rewritten from them (see updated), and settings.json records the templates' versions now. It returns
// init's steps and the files it rewrote, for their diff. It writes nothing where nothing is out of
// date but the versions settings.json records.
func UpdateTemplates(repo string) ([]Step, []TemplateUpdate, error) {
	if !fileExists(filepath.Join(repo, Dir, promptName)) {
		return nil, nil, ErrNotSetUp
	}
	s, _, err := LoadSettings(repo)
	if err != nil {
		return nil, nil, err
	}
	outdated := map[string]OutdatedTemplate{}
	for _, o := range OutdatedTemplates(repo) {
		outdated[o.Path] = o
	}
	var steps []Step
	var updates []TemplateUpdate
	now := CurrentTemplates()
	recorded := Templates{}
	if s.Templates != nil {
		recorded = *s.Templates
	}
	for _, f := range []templateFile{promptFile, verifierFile} {
		label := f.template
		o, ok := outdated[f.path]
		switch {
		case !ok && !fileExists(filepath.Join(repo, filepath.FromSlash(f.path))): // no verifier
			continue
		case !ok:
			steps = append(steps, Step{Kind: StepKept, Label: label, Detail: f.path + " has every change to its template"})
			continue
		}
		current := currentPrompt(o.text, s.CheckFast)
		if f.path == VerifierPath {
			if current, err = currentVerifier(repo); err != nil {
				return steps, updates, err
			}
		}
		after := updated(o.text, current, o.Missing)
		if err := writeFile(filepath.Join(repo, filepath.FromSlash(f.path)), []byte(after), 0o644); err != nil {
			return steps, updates, err
		}
		names := sectionTitles(o.Missing)
		updates = append(updates, TemplateUpdate{Path: f.path, Before: o.text, After: after, Sections: names})
		steps = append(steps, Step{Kind: StepDone, Label: label, Detail: "rewrote " + joinAnd(names) + " in " + f.path +
			" from the template; the rest is as it was", Commit: []string{f.path}})
	}
	recorded.WorkerPrompt, recorded.Verifier = now.WorkerPrompt, 0
	if fileExists(filepath.Join(repo, filepath.FromSlash(VerifierPath))) {
		recorded.Verifier = now.Verifier
	}
	if s.Templates == nil || *s.Templates != recorded {
		s.Templates = &recorded
		if err := SaveSettings(repo, s); err != nil {
			return steps, updates, err
		}
		versions := fmt.Sprintf("worker prompt %d", recorded.WorkerPrompt)
		if recorded.Verifier != 0 {
			versions += fmt.Sprintf(", verifier %d", recorded.Verifier)
		}
		steps = append(steps, Step{Kind: StepDone, Label: "settings", Detail: "recorded the versions of the " +
			"templates the files now have (" + versions + ")", Commit: []string{Dir + "/" + SettingsName}})
	}
	return steps, updates, nil
}

// UpdateNext is what is left after UpdateTemplates took steps, rewriting the files updates name: to
// read the diff, where it rewrote any, and to commit what changed.
func UpdateNext(steps []Step, updates []TemplateUpdate) []string {
	var commit []string
	for _, s := range steps {
		for _, p := range s.Commit {
			if !slices.Contains(commit, p) {
				commit = append(commit, p)
			}
		}
	}
	var next []string
	if len(updates) > 0 {
		next = append(next, "Read the diff above: a section rewritten from the template loses the project's own "+
			"edits in it, which you may want to carry over.")
	}
	if len(commit) == 0 {
		return append(next, "Nothing to commit: the worker prompt and the verifier have every change to their templates.")
	}
	return append(next, "Commit "+joinAnd(commit)+".")
}

// recordTemplates records in settings.json, for the files Apply wrote from orchestra's templates, the
// templates' versions now: the worker prompt where steps say it was written from the template, the
// verifier where they say it was written.
func recordTemplates(repo string, steps []Step) error {
	prompt, verifier := wroteTemplate(steps), slices.ContainsFunc(steps, func(s Step) bool {
		return slices.Contains(s.Commit, VerifierPath)
	})
	if !prompt && !verifier {
		return nil
	}
	s, _, err := LoadSettings(repo)
	if err != nil {
		return err
	}
	t := Templates{}
	if s.Templates != nil {
		t = *s.Templates
	}
	now := CurrentTemplates()
	if prompt {
		t.WorkerPrompt = now.WorkerPrompt
	}
	if verifier {
		t.Verifier = now.Verifier
	}
	s.Templates = &t
	return SaveSettings(repo, s)
}
