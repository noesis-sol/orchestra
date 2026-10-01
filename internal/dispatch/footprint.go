package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Footprint is where a ticket works: the files and functions its text names, its area labels and
// the files its metadata lists. Two tickets whose footprints overlap are likely to conflict when
// they run at the same time, so the loop doesn't start one beside the other.
type Footprint struct {
	Files []string // paths in the repository, sorted
	Funcs []string // functions and types, as named: Loop.merge, refreshBranch
	Areas []string // area:<name> labels
	// Predicted says Files are the predictor's guess for a ticket naming nothing (PredictedKey).
	Predicted bool
}

// AreaPrefix starts a label naming the part of the project a ticket works on: tickets carrying
// the same one overlap.
const AreaPrefix = "area:"

// FilesKey is the metadata key listing the files a ticket works on, as a list or a string of
// paths separated by commas or spaces (bd update <id> --set-metadata files=a.go,b.go).
const FilesKey = "files"

// PredictedKey is the metadata key caching the files the predictor organ expects a ticket naming
// nothing to change, kept apart from the maintainer's FilesKey.
const PredictedKey = "predicted_files"

// Empty reports whether the footprint names nothing.
func (f Footprint) Empty() bool { return len(f.Files)+len(f.Funcs)+len(f.Areas) == 0 }

// String lists the footprint for the log: functions, files, then areas.
func (f Footprint) String() string {
	if f.Empty() {
		return "nothing named"
	}
	s := strings.Join(slices.Concat(f.Funcs, f.Files, f.Areas), ", ")
	if f.Predicted {
		s += " (predicted)"
	}
	return s
}

var (
	// pathToken is anything that could be a path: loop.go, internal/dispatch/loop.go:661 (the line
	// number is left off), Loop.merge (sorted out later).
	pathToken = regexp.MustCompile(`[\w./-]+`)
	extension = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]*$`)
	// typeMethod is Type.method, which a path token without a known extension may be.
	typeMethod = regexp.MustCompile(`^[A-Z]\w*\.[A-Za-z_]\w*$`)
	// called is a function named with its parentheses: refreshBranch(), Loop.merge().
	called = regexp.MustCompile(`\b((?:[A-Za-z_]\w*\.)?[A-Za-z_]\w*)\(\)`)
	// codeSpan is Markdown's `code`, where a function is often named without parentheses.
	codeSpan = regexp.MustCompile("`([^`\n]+)`")
	// callIn is a call in a code span, with or without arguments: o.agentName(id).
	callIn = regexp.MustCompile(`\b((?:[A-Za-z_]\w*\.)?[A-Za-z_]\w*)\(`)
	// camelCase is an identifier with a hump (waitSettled, LastToolUse), which in a code span is
	// code, where a plain word (solo, bd) may not be.
	camelCase = regexp.MustCompile(`^[A-Za-z_]\w*[a-z0-9][A-Z]\w*$`)
)

// sourceExtensions are the file extensions a bare name (loop.go) is taken for a file by when the
// repository's files aren't known.
var sourceExtensions = setOf(`go md json yaml yml toml swift py js ts tsx jsx mjs rs rb java kt kts
	c h cc cpp hpp m mm sh bash zsh txt html css scss sql mod sum lock xml plist gradle cs php ex exs
	erl hs ml scala dart vue svelte proto tf ini cfg conf`)

// setOf is the set of the space-separated words in s.
func setOf(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.Fields(s) {
		set[w] = true
	}
	return set
}

// repoFiles are the repository's files, to tell a path named in a ticket from other words and to
// find the file a bare name (run.go) or a partial path (dispatch/run.go) means.
type repoFiles struct {
	set    map[string]bool
	byBase map[string][]string
	dirs   map[string]bool
}

// newRepoFiles indexes the files git tracks; nil when git couldn't list them.
func newRepoFiles(tracked []string) *repoFiles {
	if tracked == nil {
		return nil
	}
	r := &repoFiles{set: map[string]bool{}, byBase: map[string][]string{}, dirs: map[string]bool{".": true}}
	for _, f := range tracked {
		r.set[f] = true
		r.byBase[path.Base(f)] = append(r.byBase[path.Base(f)], f)
		for d := path.Dir(f); d != "." && !r.dirs[d]; d = path.Dir(d) {
			r.dirs[d] = true
		}
	}
	return r
}

// resolve returns the repository files name means: itself, the files it ends, or a new file in an
// existing folder. explicit keeps a name that means none of these (a files metadata entry says it
// is one). Without the repository's files, a name with a folder or a known extension is kept.
func (r *repoFiles) resolve(name string, explicit bool) []string {
	name = strings.TrimPrefix(name, "./")
	if r == nil {
		if explicit || strings.Contains(name, "/") || sourceExtensions[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))] {
			return []string{name}
		}
		return nil
	}
	if r.set[name] {
		return []string{name}
	}
	var ends []string
	for _, f := range r.byBase[path.Base(name)] {
		if f == name || strings.HasSuffix(f, "/"+name) {
			ends = append(ends, f)
		}
	}
	switch {
	case len(ends) > 0:
		return ends
	case explicit || strings.Contains(name, "/") && r.dirs[path.Dir(name)]:
		return []string{name}
	}
	return nil
}

// stdPackages are standard packages whose functions tickets name in passing (os.WriteFile,
// strings.HasPrefix): they say nothing about where a ticket works.
var stdPackages = setOf(`bufio bytes context errors exec filepath fmt http io json os path reflect
	regexp slices sort strconv strings sync syscall time`)

// funcName is a function as the footprint keeps it: a lower-case qualifier is a package or a
// variable (o.merge), not a type, and is left off. "" for a standard package's function.
func funcName(s string) string {
	q, name, ok := strings.Cut(s, ".")
	switch {
	case !ok || q[0] >= 'A' && q[0] <= 'Z':
		return s
	case stdPackages[q]:
		return ""
	}
	return name
}

// TicketFootprint is where ticket t works: the files and functions named in its title and text,
// its area labels and the files its metadata lists; with none of these, the files predicted for it
// (PredictedKey). Paths are checked against the repository's files (git ls-files), nil when they
// aren't known. With nothing named or predicted it is empty, and the ticket runs beside anything.
func TicketFootprint(t Ticket, tracked []string) Footprint {
	return ticketFootprint(t, newRepoFiles(tracked))
}

func ticketFootprint(t Ticket, repo *repoFiles) Footprint {
	files, funcs := map[string]bool{}, map[string]bool{}
	text := strings.Join([]string{t.Title, t.Description, t.Design, t.AcceptanceCriteria, t.Notes}, "\n")
	for _, tok := range pathToken.FindAllString(text, -1) {
		tok = strings.TrimRight(tok, "./-")
		if tok == "" || strings.HasPrefix(tok, "/") || !extension.MatchString(tok) {
			continue // not a file, or an absolute path or a URL, which isn't in the repository
		}
		if found := repo.resolve(tok, false); len(found) > 0 {
			for _, f := range found {
				files[f] = true
			}
		} else if typeMethod.MatchString(tok) && !sourceExtensions[strings.ToLower(strings.TrimPrefix(path.Ext(tok), "."))] {
			funcs[tok] = true
		}
	}
	for _, m := range called.FindAllStringSubmatch(text, -1) {
		if f := funcName(m[1]); f != "" {
			funcs[f] = true
		}
	}
	for _, m := range codeSpan.FindAllStringSubmatch(text, -1) {
		code := strings.TrimSpace(m[1])
		if camelCase.MatchString(code) {
			funcs[code] = true
		}
		for _, c := range callIn.FindAllStringSubmatch(code, -1) {
			if f := funcName(c[1]); f != "" {
				funcs[f] = true
			}
		}
	}
	for _, f := range metadataList(t.Metadata, FilesKey) {
		for _, p := range repo.resolve(f, true) {
			files[p] = true
		}
	}
	var fp Footprint
	for _, l := range t.Labels {
		if strings.HasPrefix(l, AreaPrefix) && len(l) > len(AreaPrefix) {
			fp.Areas = append(fp.Areas, l)
		}
	}
	fp.Files, fp.Funcs = sortedKeys(files), sortedKeys(funcs)
	sort.Strings(fp.Areas)
	if fp.Empty() {
		return predictedFootprint(metadataList(t.Metadata, PredictedKey), repo)
	}
	return fp
}

// predictedFootprint is the footprint of a ticket naming nothing, from the files predicted for it.
func predictedFootprint(predicted []string, repo *repoFiles) Footprint {
	files := map[string]bool{}
	for _, f := range predicted {
		for _, p := range repo.resolve(f, false) {
			files[p] = true
		}
	}
	return Footprint{Files: sortedKeys(files), Predicted: len(files) > 0}
}

func sortedKeys(m map[string]bool) []string {
	var l []string
	for k := range m {
		l = append(l, k)
	}
	sort.Strings(l)
	return l
}

// metadataList reads a list of paths from a ticket's metadata (FilesKey, PredictedKey): a list, or
// one string of them separated by commas or spaces. bd gives the metadata as an object or as one
// encoded in a string.
func metadataList(raw json.RawMessage, key string) []string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s)
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(raw, &meta) != nil {
		return nil
	}
	var list []string
	if json.Unmarshal(meta[key], &list) == nil {
		return list
	}
	if json.Unmarshal(meta[key], &s) == nil {
		return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	}
	return nil
}

// funcsMeet reports whether two function names can mean the same function: the same name, and the
// same type when both give one (Loop.merge and merge meet, Loop.merge and Git.merge don't).
func funcsMeet(a, b string) bool {
	ta, na, qa := strings.Cut(a, ".")
	tb, nb, qb := strings.Cut(b, ".")
	if !qa {
		ta, na = "", a
	}
	if !qb {
		tb, nb = "", b
	}
	return na == nb && (ta == "" || tb == "" || ta == tb)
}

// shared returns what a ready ticket's footprint f shares with a running ticket's footprint r and
// the files its worker has edited, or "". An area label is shared outright. Functions are compared
// when both tickets name some, else files. Files the worker edited outside what its ticket named
// are compared at file level whatever the ticket names.
func shared(f, r Footprint, edited []string) string {
	for _, a := range f.Areas {
		if slices.Contains(r.Areas, a) {
			return a
		}
	}
	byFunc := len(f.Funcs) > 0 && len(r.Funcs) > 0
	if byFunc {
		for _, x := range f.Funcs {
			for _, y := range r.Funcs {
				if funcsMeet(x, y) {
					return x
				}
			}
		}
	}
	for _, p := range f.Files {
		named := slices.Contains(r.Files, p)
		if named && !byFunc || slices.Contains(edited, p) && !(named && byFunc) {
			return p
		}
	}
	return ""
}

// runFootprint is a running ticket's footprint as the scheduler sees it: what its ticket names, and
// what its worker has edited so far.
type runFootprint struct {
	fp     Footprint
	wt     string   // the ticket's worktree, once made
	edited []string // repository files its worker reported editing
}

// footprintOn says whether tickets are scheduled by footprint: with more than one worker, unless
// the project turned it off.
func (o *Loop) footprintOn() bool { return o.cfg.Concurrency > 1 && !o.cfg.NoFootprint }

// readFiles lists the repository's files for the footprints of the tickets about to be compared.
func (o *Loop) readFiles(ctx context.Context) {
	o.files = newRepoFiles(o.checkout.TrackedFiles(ctx, o.cfg.Repo))
}

// footprintOf is ready ticket t's footprint: TicketFootprint, or, for a ticket naming nothing, the
// prediction made in this run when bd hasn't handed back the one cached on the ticket.
func (o *Loop) footprintOf(t Ticket) Footprint {
	fp := ticketFootprint(t, o.files)
	if fp.Empty() {
		if files := o.prediction(t.ID); len(files) > 0 {
			fp = predictedFootprint(files, o.files)
		}
	}
	return fp
}

// startFootprint records a dispatched ticket's footprint, and logs it.
func (o *Loop) startFootprint(t Ticket) {
	if !o.footprintOn() {
		return
	}
	fp := o.footprintOf(t)
	o.mu.Lock()
	if o.footprints == nil {
		o.footprints = map[string]*runFootprint{}
	}
	o.footprints[t.ID] = &runFootprint{fp: fp}
	o.mu.Unlock()
	o.info("  %s footprint: %s", t.ID, fp)
}

// footprintWorktree records where a running ticket's worker edits.
func (o *Loop) footprintWorktree(id, wt string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r := o.footprints[id]; r != nil {
		r.wt = wt
	}
}

// endFootprint forgets a ticket that is no longer running.
func (o *Loop) endFootprint(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.footprints, id)
	delete(o.skipSaid, id)
}

// readEdits brings each running ticket's edited files up to date from its worker's reports, and
// warns once when two running workers edit the same file: the second to merge is likely to
// conflict.
func (o *Loop) readEdits() {
	if o.reporter == nil {
		return
	}
	o.mu.Lock()
	wts := map[string]string{}
	for id, r := range o.footprints {
		if r.wt != "" {
			wts[id] = r.wt
		}
	}
	o.mu.Unlock()
	edited := map[string][]string{}
	for id, wt := range wts {
		edited[id] = o.reporter.EditedFiles(wt)
	}
	o.mu.Lock()
	for id, files := range edited {
		if r := o.footprints[id]; r != nil {
			r.edited = files
		}
	}
	o.mu.Unlock()

	ids := sortedKeys(func() map[string]bool {
		m := map[string]bool{}
		for id := range edited {
			m[id] = true
		}
		return m
	}())
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			for _, f := range edited[a] {
				key := a + " " + b + " " + f
				if !slices.Contains(edited[b], f) || o.warned[key] {
					continue
				}
				if o.warned == nil {
					o.warned = map[string]bool{}
				}
				o.warned[key] = true
				o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf(
					"  LIKELY_CONFLICT: %s and %s both edit %s; the second to merge may conflict", a, b, f)})
			}
		}
	}
}

// overlapsRunning reports whether ready ticket t's footprint overlaps a running ticket's, saying
// why once. A solo ticket waits for every running ticket anyway, so it is never skipped for one.
func (o *Loop) overlapsRunning(t Ticket, runningIDs map[string]bool) bool {
	if HasLabel(t, SoloLabel) {
		return false
	}
	fp := o.footprintOf(t)
	why := ""
	if !fp.Empty() {
		o.mu.Lock()
		for _, id := range sortedKeys(runningIDs) {
			if r := o.footprints[id]; r != nil {
				if what := shared(fp, r.fp, r.edited); what != "" {
					why = fmt.Sprintf("touches %s, like running %s", what, id)
					break
				}
			}
		}
		o.mu.Unlock()
	}
	o.mu.Lock()
	said := o.skipSaid[t.ID]
	if o.skipSaid == nil {
		o.skipSaid = map[string]string{}
	}
	o.skipSaid[t.ID] = why
	o.mu.Unlock()
	if why != "" && why != said {
		o.info("  skipping %s: %s", t.ID, why)
	}
	return why != ""
}
