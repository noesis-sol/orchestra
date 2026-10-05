package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Footprint is where a ticket works: the files and functions its text names, its area labels and
// the files its metadata lists. Two tickets whose footprints overlap are likely to conflict when
// they run at the same time, so the loop doesn't start one beside the other. The files the
// project's check command names count only when the metadata lists them: nearly every ticket
// names them, asking for the check to pass, without changing them.
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

// FilesKey is the metadata key listing the files a ticket works on, as a list, a list in a string
// or a string of paths separated by commas or spaces (bd update <id> --set-metadata files=a.go,b.go).
const FilesKey = "files"

// TicketFiles returns the files the ticket's metadata lists under FilesKey, as written, in any of
// those forms.
func TicketFiles(t Ticket) []string { return metadataList(t.Metadata, FilesKey) }

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
	// number is left off), docs/café.md, Loop.merge (sorted out later). Letters and digits of any
	// script count, and combining marks: macOS often writes é as e and U+0301.
	pathToken = regexp.MustCompile(`[\p{L}\p{M}\p{N}_./-]+`)
	// asciiPathToken is a path token's ASCII part, which may be a path run into words of a script
	// written without spaces: 修改loop.go, loop.goを直す.
	asciiPathToken = regexp.MustCompile(`[\w./-]+`)
	extension      = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]*$`)
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
	for w := range strings.FieldsSeq(s) {
		set[w] = true
	}
	return set
}

// repoFiles are the repository's files, to tell a path named in a ticket from other words and to
// find the file a bare name (run.go) or a partial path (dispatch/run.go) means, and the files the
// project's check command names.
type repoFiles struct {
	set    map[string]bool // nil when git couldn't list the files
	byBase map[string][]string
	dirs   map[string]bool
	check  map[string]bool // the files the check command names (scripts/check.sh)
}

// newRepoFiles indexes the files git tracks (nil when git couldn't list them) and finds the files
// the check command names among them.
func newRepoFiles(tracked []string, check string) *repoFiles {
	r := &repoFiles{check: map[string]bool{}}
	if tracked != nil {
		r.set, r.byBase, r.dirs = map[string]bool{}, map[string][]string{}, map[string]bool{".": true}
	}
	for _, f := range tracked {
		r.set[f] = true
		r.byBase[path.Base(f)] = append(r.byBase[path.Base(f)], f)
		for d := path.Dir(f); d != "." && !r.dirs[d]; d = path.Dir(d) {
			r.dirs[d] = true
		}
	}
	eachPathToken(check, func(tok string) bool {
		found := r.resolve(tok, false)
		for _, f := range found {
			r.check[f] = true
		}
		return len(found) > 0
	})
	return r
}

// checks reports whether f is a file the check command names.
func (r *repoFiles) checks(f string) bool { return r != nil && r.check[f] }

// resolve returns the repository files name means: itself, the files it ends, or a new file in an
// existing folder. explicit keeps a name that means none of these (a files metadata entry says it
// is one). Without the repository's files, a name with a folder or a known extension is kept.
func (r *repoFiles) resolve(name string, explicit bool) []string {
	name = strings.TrimPrefix(name, "./")
	if r == nil || r.set == nil {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		if explicit || strings.Contains(name, "/") || sourceExtensions[ext] {
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
// strings.HasPrefix, synctest.Wait): they say nothing about where a ticket works.
var stdPackages = setOf(`atomic base64 binary bits bufio bytes cmp context debug errors exec filepath flag
	fmt gzip hash hex hmac http httptest io ioutil iter json log maps math md5 netip os path pprof rand
	reflect regexp runtime sha1 sha256 signal slices slog sort strconv strings sync synctest syscall
	tabwriter testing time unicode unsafe url utf16 utf8`)

// goWords are Go's keywords and builtins, which a ticket calls bare (func(), len(x), string(b)) and
// which are no function of the project's.
var goWords = setOf(`break case chan const continue default defer else fallthrough for func go goto if
	import interface map package range return select struct switch type var
	append cap clear close complex copy delete imag len make max min new panic print println real recover
	any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32 int64 rune
	string uint uint8 uint16 uint32 uint64 uintptr`)

// funcName is a function as the footprint keeps it: a lower-case qualifier is a package or a
// variable (o.merge), not a type, and is left off. "" for a standard package's function and for a
// Go keyword or builtin called bare; a method of the same name (o.close) is kept.
func funcName(s string) string {
	q, name, ok := strings.Cut(s, ".")
	switch {
	case !ok && goWords[s] || ok && stdPackages[q]:
		return ""
	case !ok || q[0] >= 'A' && q[0] <= 'Z':
		return s
	}
	return name
}

// TicketFootprint is where ticket t works: the files and functions named in its title and text,
// its area labels and the files its metadata lists; with none of these, the files predicted for it
// (PredictedKey). Paths are checked against the repository's files (git ls-files), nil when they
// aren't known. The files the project's check command (check) names count only when its metadata
// lists them. With nothing named or predicted it is empty, and the ticket runs beside anything.
func TicketFootprint(t Ticket, tracked []string, check string) Footprint {
	return ticketFootprint(t, newRepoFiles(tracked, check))
}

// eachPathToken calls take with each word of text that may be a path: with an extension, and
// neither an absolute path nor a URL, which aren't in the repository. take reports whether the word
// names something. A word with characters outside ASCII that names nothing is tried in its ASCII
// parts instead: a path run into words of a script written without spaces (修改loop.go, loop.goを直す)
// is one of them.
func eachPathToken(text string, take func(tok string) bool) {
	nonASCII := func(r rune) bool { return r > unicode.MaxASCII }
	for _, word := range pathToken.FindAllString(text, -1) {
		word = strings.TrimRight(word, "./-")
		if strings.HasPrefix(word, "/") { // an absolute path or a URL, no part of which is in the repository
			continue
		}
		if extension.MatchString(word) && take(word) || !strings.ContainsFunc(word, nonASCII) {
			continue
		}
		for _, part := range asciiPathToken.FindAllString(word, -1) {
			if part = strings.TrimRight(part, "./-"); !strings.HasPrefix(part, "/") && extension.MatchString(part) {
				take(part)
			}
		}
	}
}

func ticketFootprint(t Ticket, repo *repoFiles) Footprint {
	files, funcs := map[string]bool{}, map[string]bool{}
	text := strings.Join([]string{t.Title, t.Description, t.Design, t.AcceptanceCriteria, t.Notes}, "\n")
	eachPathToken(text, func(tok string) bool {
		if found := repo.resolve(tok, false); len(found) > 0 {
			for _, f := range found {
				if !repo.checks(f) { // "scripts/check.sh passes" says nothing about where a ticket works
					files[f] = true
				}
			}
			return true
		}
		if typeMethod.MatchString(tok) && !sourceExtensions[strings.ToLower(strings.TrimPrefix(path.Ext(tok), "."))] {
			funcs[tok] = true
			return true
		}
		return false
	})
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
	fp.Files, fp.Funcs = slices.Sorted(maps.Keys(files)), slices.Sorted(maps.Keys(funcs))
	sort.Strings(fp.Areas)
	if fp.Empty() {
		return predictedFootprint(metadataList(t.Metadata, PredictedKey), repo)
	}
	return fp
}

// predictedFootprint is the footprint of a ticket naming nothing, from the files predicted for it
// but those the check command names.
func predictedFootprint(predicted []string, repo *repoFiles) Footprint {
	files := map[string]bool{}
	for _, f := range predicted {
		for _, p := range repo.resolve(f, false) {
			if !repo.checks(p) {
				files[p] = true
			}
		}
	}
	return Footprint{Files: slices.Sorted(maps.Keys(files)), Predicted: len(files) > 0}
}

// metadataList reads a list of paths from a ticket's metadata (FilesKey, PredictedKey): a list, a
// list encoded in a string (bd update <id> --set-metadata 'files=["a.go","b.go"]' stores one), or
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
	if json.Unmarshal(meta[key], &s) != nil {
		return nil
	}
	var inString []string
	if strings.HasPrefix(strings.TrimSpace(s), "[") && json.Unmarshal([]byte(s), &inString) == nil {
		return inString
	}
	// A list that isn't quite JSON (["a.go", 'b.go'), with a missing bracket) leaves its quotes and
	// brackets on the words it splits into: they aren't part of any path.
	var words []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if w = strings.Trim(w, "\"'`[]"); w != "" {
			words = append(words, w)
		}
	}
	return words
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
	o.files = newRepoFiles(o.checkout.TrackedFiles(ctx, o.cfg.Repo), o.cfg.Check)
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

	ids := slices.Sorted(maps.Keys(edited))
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
		for _, id := range slices.Sorted(maps.Keys(runningIDs)) {
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
