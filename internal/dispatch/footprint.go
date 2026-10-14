package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"golang.org/x/text/unicode/norm"
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
func TicketFiles(t Ticket) []string { return metadataList(t, FilesKey) }

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
	// called is a function named with its parentheses: refreshBranch(), Loop.merge(), o.log.Raw().
	called = regexp.MustCompile(`\b((?:[A-Za-z_]\w*\.)*[A-Za-z_]\w*)\(\)`)
	// codeSpan is Markdown's `code`, where a function is often named without parentheses.
	codeSpan = regexp.MustCompile("`([^`\n]+)`")
	// callIn is a call in a code span, with or without arguments: o.agentName(id).
	callIn = regexp.MustCompile(`\b((?:[A-Za-z_]\w*\.)*[A-Za-z_]\w*)\(`)
	// camelCase is an identifier with a hump (waitSettled, LastToolUse), which in a code span is
	// code, where a plain word (solo, bd) may not be.
	camelCase = regexp.MustCompile(`^[A-Za-z_]\w*[a-z0-9][A-Z]\w*$`)
	// identifier is a word of source code, for the functions a file mentions.
	identifier = regexp.MustCompile(`[A-Za-z_]\w*`)
)

// camelWords are names written with a hump that are no code of the project's: GitHub, macOS.
var camelWords = setOf(`GitHub GitLab BitBucket JavaScript TypeScript CoffeeScript PostgreSQL MySQL SQLite MongoDB
	MariaDB GraphQL WebSocket WebSockets YouTube LinkedIn OpenAI PowerShell WordPress DevOps DockerHub NuGet PyPI
	NumPy SciPy PyTorch TensorFlow FastAPI LaTeX OpenAPI IntelliJ JetBrains VSCode iOS iPadOS macOS tvOS watchOS
	visionOS iPhone iPad iCloud iTerm iTerm2 eBPF`)

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
// project's check command names. Names are compared in one Unicode form, NFC: a name with é written
// as e and U+0301 (NFD, as Finder gives it) means the file git lists with é, and the reverse.
type repoFiles struct {
	files  map[string][]string // the files as git lists them, by their NFC form; nil when git couldn't list them
	byBase map[string][]string // the files' NFC forms, by base name
	dirs   map[string]bool     // the folders' NFC forms
	check  map[string]bool     // the files the check command names (scripts/check.sh)
	// read gives a repository file's contents, to find the functions it mentions; nil when they
	// can't be read.
	read  func(file string) ([]byte, error)
	mu    sync.Mutex
	words map[string]map[string]bool // the identifiers in each file read, nil for one that can't be
}

// newRepoFiles indexes the files git tracks (nil when git couldn't list them) and finds the files
// the check command names among them.
func newRepoFiles(tracked []string, check string) *repoFiles {
	r := &repoFiles{check: map[string]bool{}}
	if tracked != nil {
		r.files, r.byBase, r.dirs = map[string][]string{}, map[string][]string{}, map[string]bool{".": true}
	}
	for _, f := range tracked {
		n := norm.NFC.String(f)
		if !repoPath(n) { // git lists none of these
			continue
		}
		if r.files[n] == nil {
			r.byBase[path.Base(n)] = append(r.byBase[path.Base(n)], n)
		}
		r.files[n] = append(r.files[n], f) // two files whose names differ only in form are both meant
		for d := path.Dir(n); d != "." && !r.dirs[d]; d = path.Dir(d) {
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

// mentions reports whether repository file file mentions function fn, by its name and its type
// when it gives one (Loop.merge: Loop and merge), as identifiers. A file that can't be read (a new
// one, or the contents unknown) mentions none.
func (r *repoFiles) mentions(file, fn string) bool {
	if r == nil || r.read == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	words, ok := r.words[file]
	if !ok {
		if b, err := r.read(file); err == nil {
			words = map[string]bool{}
			for _, w := range identifier.FindAll(b, -1) {
				words[string(w)] = true
			}
		}
		if r.words == nil {
			r.words = map[string]map[string]bool{}
		}
		r.words[file] = words
	}
	typ, name, ok := strings.Cut(fn, ".")
	if !ok {
		return words[fn]
	}
	return words[typ] && words[name]
}

// checks reports whether f is a file the check command names.
func (r *repoFiles) checks(f string) bool { return r != nil && r.check[f] }

// repoPath reports whether name can be a path in the repository, relative to its root: not empty,
// not the root itself (., a/..), and neither absolute nor leading out of it (/etc/passwd, ../x.go).
func repoPath(name string) bool { return filepath.IsLocal(name) && path.Clean(name) != "." }

// file returns the file name is as git lists it, whichever Unicode form name is in; for a file git
// doesn't track (a new one, or the files unknown), name in NFC, the form resolve gives one in. So
// the files a worker edits compare with those tickets name.
func (r *repoFiles) file(name string) string {
	n := norm.NFC.String(name)
	if r == nil {
		return n
	}
	switch fs := r.files[n]; {
	case slices.Contains(fs, name):
		return name
	case len(fs) > 0:
		return fs[0]
	}
	return n
}

// resolve returns the repository files name means, in whichever Unicode form, as git lists them:
// itself and every file it ends (run.go means ./run.go and internal/dispatch/run.go), or a new file
// in an existing folder. explicit, for a files metadata entry, also takes a folder (internal/dispatch/,
// or dispatch/) for the files under it and a glob (*.go, internal/**/*_test.go) for the files it
// matches, and keeps a name that means none of these as a file the ticket adds, but never one that
// can't be a path in the repository (repoPath). Without the repository's files, a name with a folder
// or a known extension is kept. A name kept but not tracked is given in NFC.
func (r *repoFiles) resolve(name string, explicit bool) []string {
	name = norm.NFC.String(strings.TrimPrefix(name, "./"))
	if !repoPath(name) {
		return nil
	}
	if r == nil || r.files == nil {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		if explicit || strings.Contains(name, "/") || sourceExtensions[ext] {
			return []string{name}
		}
		return nil
	}
	name = path.Clean(name)
	if explicit && strings.ContainsAny(name, "*?[") {
		return r.glob(name)
	}
	found := slices.Clone(r.files[name])
	for _, n := range r.byBase[path.Base(name)] {
		if strings.HasSuffix(n, "/"+name) {
			found = append(found, r.files[n]...)
		}
	}
	if explicit {
		for _, n := range slices.Sorted(maps.Keys(r.files)) {
			if strings.HasPrefix(n, name+"/") || strings.Contains(n, "/"+name+"/") {
				found = append(found, r.files[n]...)
			}
		}
	}
	switch {
	case len(found) > 0:
		return found
	case explicit || strings.Contains(name, "/") && r.dirs[path.Dir(name)]:
		return []string{name}
	}
	return nil
}

// glob returns the files pattern matches, in any folder as a partial path does: its last part a
// file's name or a folder's, holding every file under it. * and ? match within a part of a path, and
// ** any number of folders.
func (r *repoFiles) glob(pattern string) []string {
	pat := append([]string{"**"}, strings.Split(pattern, "/")...)
	var found []string
	for _, n := range slices.Sorted(maps.Keys(r.files)) {
		parts := strings.Split(n, "/")
		for end := 1; end <= len(parts); end++ {
			if globMatch(pat, parts[:end]) {
				found = append(found, r.files[n]...)
				break
			}
		}
	}
	return found
}

// globMatch reports whether the parts of a path match the pattern's, path.Match for each but **,
// which matches any number of them.
func globMatch(pat, parts []string) bool {
	if len(pat) == 0 {
		return len(parts) == 0
	}
	if pat[0] == "**" {
		for i := range len(parts) + 1 {
			if globMatch(pat[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	ok, _ := path.Match(pat[0], parts[0]) // a malformed pattern matches nothing
	return ok && globMatch(pat[1:], parts[1:])
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

// testVars are the names tests give a *testing.T, B or F, and testingMethods those types' methods:
// t.Run() and b.Loop() are no functions of the project's.
var (
	testVars       = setOf(`t b f tb`)
	testingMethods = setOf(`Add Attr Chdir Cleanup Context Deadline Error Errorf Fail FailNow Failed Fatal Fatalf
		Fuzz Helper Log Logf Loop Name Output Parallel ReportAllocs ReportMetric ResetTimer Run RunParallel SetBytes
		Setenv Skip SkipNow Skipf Skipped StartTimer StopTimer TempDir`)
)

// funcName is a function as the footprint keeps it, from a call's name: a lower-case qualifier is a
// package, a variable or a field (o.merge, o.log.Raw), not a type, and is left off. "" for a standard
// package's function or variable's method (os.WriteFile, os.Stdout.Write), a test's method (t.Run)
// and a Go keyword or builtin called bare; a method of the same name (o.close) is kept.
func funcName(s string) string {
	parts := strings.Split(s, ".")
	name := parts[len(parts)-1]
	if len(parts) == 1 {
		if goWords[s] {
			return ""
		}
		return s
	}
	switch q := parts[len(parts)-2]; {
	case stdPackages[parts[0]] || testVars[q] && testingMethods[name]:
		return ""
	case q[0] >= 'A' && q[0] <= 'Z':
		return q + "." + name
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
	text := strings.Join([]string{t.Title, t.Description, t.Design, t.AcceptanceCriteria, t.Notes}, "\n")
	files, funcs := pathsIn(text, repo)
	maps.Copy(funcs, callsIn(text))
	maps.Copy(funcs, codeSpansIn(text))
	for _, f := range metadataList(t, FilesKey) {
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
		return predictedFootprint(metadataList(t, PredictedKey), repo)
	}
	return fp
}

// pathsIn returns the repository files the words of text that may be paths name, but those the
// check command names, and the Type.method words among them that name no file.
func pathsIn(text string, repo *repoFiles) (files, funcs map[string]bool) {
	files, funcs = map[string]bool{}, map[string]bool{}
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
	return files, funcs
}

// callsIn returns the functions text names with their parentheses: refreshBranch(), Loop.merge().
func callsIn(text string) map[string]bool {
	funcs := map[string]bool{}
	for _, m := range called.FindAllStringSubmatch(text, -1) {
		if f := funcName(m[1]); f != "" {
			funcs[f] = true
		}
	}
	return funcs
}

// codeSpansIn returns the functions text names in its code spans: an identifier with a hump
// (`waitSettled`), or a call with or without arguments (`o.agentName(id)`).
func codeSpansIn(text string) map[string]bool {
	funcs := map[string]bool{}
	for _, m := range codeSpan.FindAllStringSubmatch(text, -1) {
		code := strings.TrimSpace(m[1])
		if camelCase.MatchString(code) && !camelWords[code] {
			funcs[code] = true
		}
		for _, c := range callIn.FindAllStringSubmatch(code, -1) {
			if f := funcName(c[1]); f != "" {
				funcs[f] = true
			}
		}
	}
	return funcs
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
// one string of them separated by commas or spaces (see Ticket.metadataField).
func metadataList(t Ticket, key string) []string {
	field := t.metadataField(key)
	var list []string
	if json.Unmarshal(field, &list) == nil {
		return list
	}
	var s string
	if json.Unmarshal(field, &s) != nil {
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
// the files its worker has edited, or "": an area label, a function (sharedFunc), or a file
// (sharedFile).
func shared(f, r Footprint, edited []string, in func(file, fn string) bool) string {
	for _, a := range f.Areas {
		if slices.Contains(r.Areas, a) {
			return a
		}
	}
	if x := sharedFunc(f, r); x != "" {
		return x
	}
	return sharedFile(f, r, edited, in, nil)
}

// sharedFunc returns a function both footprints name, as f names it, or "".
func sharedFunc(f, g Footprint) string {
	for _, x := range f.Funcs {
		for _, y := range g.Funcs {
			if funcsMeet(x, y) {
				return x
			}
		}
	}
	return ""
}

// sharedFile returns a file both footprints work on, for which keep holds (any, when keep is nil),
// or "". A ticket works on a file it names and on one the other ticket names or r's worker edited
// (edited) that mentions a function it names (in reports whether one does), and a file both work on
// is shared unless both name functions it mentions: they are compared by function (sharedFunc).
func sharedFile(f, r Footprint, edited []string, in func(file, fn string) bool, keep func(string) bool) string {
	namesIn := func(fp Footprint, p string) bool {
		return in != nil && slices.ContainsFunc(fp.Funcs, func(fn string) bool { return in(p, fn) })
	}
	seen := map[string]bool{}
	for _, p := range slices.Concat(f.Files, r.Files, edited) {
		if seen[p] {
			continue
		}
		seen[p] = true
		fIn, rIn := namesIn(f, p), namesIn(r, p)
		fAt := fIn || slices.Contains(f.Files, p)
		rAt := rIn || slices.Contains(r.Files, p) || slices.Contains(edited, p)
		if fAt && rAt && !(fIn && rIn) && (keep == nil || keep(p)) {
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

// readFiles lists the repository's files for the footprints of the tickets about to be compared,
// reading those compared by function from the checkout.
func (r *runState) readFiles(ctx context.Context) {
	c := r.o.cfg
	r.files = newRepoFiles(r.o.checkout.TrackedFiles(ctx, c.Repo), c.Check)
	r.files.read = func(file string) ([]byte, error) { return ReadSource(c.Repo, file) }
}

// maxSource is the most of a file read for the functions it mentions.
const maxSource = 4 << 20

// ReadSource reads up to maxSource bytes of file in the repository at repo, never outside it.
func ReadSource(repo, file string) ([]byte, error) {
	root, err := os.OpenRoot(repo)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // opened read-only: nothing to flush
	f, err := root.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // read-only
	return io.ReadAll(io.LimitReader(f, maxSource))
}

// footprintOf is ready ticket t's footprint: TicketFootprint, or, for a ticket naming nothing, the
// prediction made in this run when bd hasn't handed back the one cached on the ticket.
func (r *runState) footprintOf(t Ticket) Footprint {
	fp := ticketFootprint(t, r.files)
	if fp.Empty() {
		if files := r.o.prediction(t.ID); len(files) > 0 {
			fp = predictedFootprint(files, r.files)
		}
	}
	return fp
}

// startFootprint records a dispatched ticket's footprint, and logs it.
func (r *runState) startFootprint(t Ticket) {
	o := r.o
	if !o.footprintOn() {
		return
	}
	fp := r.footprintOf(t)
	o.mu.Lock()
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
func (r *runState) endFootprint(id string) {
	o := r.o
	o.mu.Lock()
	delete(o.footprints, id)
	o.mu.Unlock()
	r.skipSaid.forget(id)
}

// readEdits brings each running ticket's edited files up to date from its worker's reports, and
// warns once when two running workers edit the same file: the second to merge is likely to
// conflict.
func (r *runState) readEdits() {
	o := r.o
	if o.reporter == nil {
		return
	}
	o.mu.Lock()
	wts := map[string]string{}
	for id, f := range o.footprints {
		if f.wt != "" {
			wts[id] = f.wt
		}
	}
	o.mu.Unlock()
	edited := map[string][]string{}
	for id, wt := range wts {
		files := o.reporter.EditedFiles(wt)
		for i, f := range files {
			files[i] = r.files.file(f) // in the form the footprints' files are in
		}
		edited[id] = files
	}
	o.mu.Lock()
	for id, files := range edited {
		if f := o.footprints[id]; f != nil {
			f.edited = files
		}
	}
	o.mu.Unlock()

	for _, e := range sharedEdits(edited) {
		key := e.a + " " + e.b + " " + e.file
		if r.warned[key] {
			continue
		}
		r.warned[key] = true
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf(
			"  LIKELY_CONFLICT: %s and %s both edit %s; the second to merge may conflict", e.a, e.b, e.file)})
	}
}

// edit is a file the workers of two running tickets, a before b, both edited.
type edit struct{ a, b, file string }

// sharedEdits returns the files two running tickets' workers both edited, from the files each
// ticket's worker edited (edited, by ticket): by the pair, in the order of the tickets' IDs, then
// as a's worker lists them.
func sharedEdits(edited map[string][]string) []edit {
	var both []edit
	ids := slices.Sorted(maps.Keys(edited))
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			for _, f := range edited[a] {
				if slices.Contains(edited[b], f) {
					both = append(both, edit{a, b, f})
				}
			}
		}
	}
	return both
}

// overlapsRunning reports whether ready ticket t's footprint overlaps a running ticket's, saying
// why once. A solo ticket waits for every running ticket anyway, so it is never skipped for one.
func (r *runState) overlapsRunning(t Ticket, runningIDs map[string]bool) bool {
	o := r.o
	if HasLabel(t, SoloLabel) {
		return false
	}
	fp := r.footprintOf(t)
	why := ""
	if !fp.Empty() {
		o.mu.Lock()
		for _, id := range slices.Sorted(maps.Keys(runningIDs)) {
			if f := o.footprints[id]; f != nil {
				if what := shared(fp, f.fp, f.edited, r.files.mentions); what != "" {
					why = fmt.Sprintf("touches %s, like running %s", what, id)
					break
				}
			}
		}
		o.mu.Unlock()
	}
	if r.skipSaid.changed(t.ID, why) {
		o.info("  skipping %s: %s", t.ID, why)
	}
	return why != ""
}
