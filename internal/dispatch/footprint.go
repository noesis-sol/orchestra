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
	"unicode/utf8"

	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/text/unicode/norm"
)

// Footprint is where a ticket works: the files and functions its text names, its area labels and
// the files its metadata lists. Two tickets whose footprints overlap are likely to conflict when
// they run at the same time, so the loop doesn't start one beside the other. The files the
// project's check commands name, specs under .orchestra/specs/ and the spec a ticket's own metadata
// names (SpecKey), count only when the metadata lists them: nearly every ticket names the check,
// asking for it to pass, and every ticket broken down from a spec cites it, without changing them.
// The files git merges by union (merge=union, as init offers for the changelog) never count: two
// tickets' lines in one can't conflict, git keeps both. A file two footprints share keeps them apart
// only when one of them is to change it (changes): two tickets that only cite a file can't conflict
// over it.
type Footprint struct {
	// Files are paths in the repository, sorted. A folder the files metadata lists is kept as one,
	// ending in /, beside the tracked files under it: it holds the files under it, those another ticket
	// adds included, and is all there is of a new one (or of any, the files unknown).
	Files []string
	// Cited are the files among Files the ticket names only to read, sorted: those its text names when
	// its files metadata lists others (the files it changes), and, without files metadata, a document
	// (docExtensions) its text names in no sentence asking to change it (asksChange), as tickets cite
	// the project's guides and spikes for conventions and findings.
	Cited []string
	// Listed says the files metadata lists the files the ticket changes: a function it names changes
	// none of the others (changes).
	Listed bool
	// Globs are the globs the files metadata lists, sorted, cleaned and in NFC: one that matches none of
	// the repository's files (any, the files unknown) as it is (web/src/*.ts, which a ticket of a project
	// built from a spec may list before there is any), one that matches some anchored at each folder it
	// matched in (dispatch/*.go as internal/dispatch/*.go), beside those files in Files. Each holds the
	// files and folders other footprints name that it would match from the repository's root
	// (globHolds): those another ticket adds there. They are kept apart from Files, where a name may hold
	// a [ of its own.
	Globs []string
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
func (f Footprint) Empty() bool { return len(f.Files)+len(f.Globs)+len(f.Funcs)+len(f.Areas) == 0 }

// String lists the footprint for the log: functions, files, globs, then areas.
func (f Footprint) String() string {
	if f.Empty() {
		return "nothing named"
	}
	s := strings.Join(slices.Concat(f.Funcs, f.Files, f.Globs, f.Areas), ", ")
	if f.Predicted {
		s += " (predicted)"
	}
	return s
}

// pathRest is the rest of a run of a path token outside brackets: letters, digits, marks, _ . / -,
// and a + starting a name after a /.
const pathRest = `(?:/\+[\p{L}\p{M}\p{N}_]|[\p{L}\p{M}\p{N}_./-])*`

var (
	// pathToken is anything that could be a path: loop.go, internal/dispatch/loop.go:661 (the line
	// number is left off), docs/café.md, Loop.merge (sorted out later). Letters and digits of any
	// script count, and combining marks: macOS often writes é as e and U+0301. So do brackets, single
	// or double, around a name inside a word, as in the routes of a Next.js or SvelteKit project
	// (pages/[id].tsx, app/[[...slug]]/page.tsx, src/routes/[page=fruit]/), but not one starting it:
	// [README.md](/README.md), a Markdown link, names README.md. So does a + starting a name at the
	// word's start or after a /, as in a SvelteKit route's files (src/routes/[slug]/+page.svelte,
	// +layout.ts), but no other: C++, 1.26+ and the + of a+b, which eachPathToken drops, are no part
	// of a path.
	pathToken = regexp.MustCompile(`(?:\+[\p{L}\p{M}\p{N}_]|[\p{L}\p{M}\p{N}_./-])` + pathRest +
		`(?:(?:\[[\p{L}\p{M}\p{N}_.=-]+\]|\[\[[\p{L}\p{M}\p{N}_.=-]+\]\])` + pathRest + `)*`)
	// plainPathToken is a part of a path token without brackets or a +, which a word with them may be
	// made of instead: an index expression (files[i], Footprint.Files[0]), or a + marking an added line.
	plainPathToken = regexp.MustCompile(`[\p{L}\p{M}\p{N}_./-]+`)
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
	// sentenceBreak ends a sentence of a ticket's text: a line's end, or a . ! or ? and the spaces after
	// it (a closing quote or bracket between them), unless a lower-case letter follows (e.g. docs/x.md).
	sentenceBreak = regexp.MustCompile(`[.!?]["'’”)\]]*\s+|\n`)
	// changeWord is a word asking for a change to a document a sentence names near it (asksChange), before
	// or after it: "Update docs/running.md", "Add the result to docs/spikes/07-compile.md", "docs/workers.md
	// updated".
	changeWord = regexp.MustCompile(`(?i)\b(?:add|added|adding|update|updated|updating|change|changed|changing|` +
		`edit|edited|editing|write|written|writing|rewrite|rewritten|reword|reworded|document|fix|fixed|correct|` +
		`corrected|mention|say|describe|explain|note|record|remove|removed|delete|deleted|drop|replace|rename|` +
		`move|put|pin|keep|extend|create|split|trim|shorten|expand)\b`)
	// changesWord is a word saying, after a document it follows, what the document is to do or have:
	// "docs/development.md says how to run them". Before a document, its subject is something else ("the
	// help says it disables CLAUDE.md").
	changesWord = regexp.MustCompile(`(?i)\b(?:adds|updates|changes|edits|writes|rewrites|rewords|fixes|` +
		`mentions|says|describes|explains|notes|records|removes|deletes|drops|replaces|renames|moves|extends|` +
		`creates|lists|names|gives|shows|covers)\b`)
)

// docExtensions are the extensions of documents, files of prose that tickets cite for conventions and
// findings more often than they change them.
var docExtensions = setOf(`md markdown mdx txt rst adoc`)

// document reports whether p, a file or a folder, is a document (docExtensions).
func document(p string) bool {
	return !strings.HasSuffix(p, "/") && docExtensions[strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))]
}

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
// project's check commands name. Names are compared in one Unicode form, NFC: a name with é written
// as e and U+0301 (NFD, as Finder gives it) means the file git lists with é, and the reverse.
type repoFiles struct {
	files  map[string][]string // the files as git lists them, by their NFC form; nil when git couldn't list them
	byBase map[string][]string // the files' NFC forms, by base name
	dirs   map[string]bool     // the folders' NFC forms
	check  map[string]bool     // the files the check commands name (scripts/check.sh)
	// read gives a repository file's contents, to find the functions it mentions; nil when they
	// can't be read.
	read func(file string) ([]byte, error)
	// union reports whether git merges a repository file by union; nil when it isn't asked.
	union func(file string) bool
	mu    sync.Mutex
	words map[string]map[string]bool // the identifiers in each file read, nil for one that can't be
}

// newRepoFiles indexes the files git tracks (nil when git couldn't list them) and finds the files
// the check commands (check) name among them.
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

// specsDir is the folder a spec from outside the repository is committed to. Every ticket broken
// down from a spec cites it, and may name its file, without changing it.
const specsDir = project.Dir + "/specs/"

// inPassing reports whether f is a file a ticket names without working on it: one the check command
// names, which nearly every ticket asks to pass, a spec under .orchestra/specs/, or spec, the spec
// the ticket comes from (specFile; "" for none).
func (r *repoFiles) inPassing(f, spec string) bool {
	c := path.Clean(f)
	return r != nil && r.check[f] || strings.HasPrefix(c, specsDir) || spec != "" && norm.NFC.String(c) == spec
}

// unioned reports whether git merges f, a file, by union (merge=union in .gitattributes): two
// tickets' lines in it can't conflict, as git keeps both, so it is no reason to keep them apart.
func (r *repoFiles) unioned(f string) bool {
	return r != nil && r.union != nil && !strings.HasSuffix(f, "/") && r.union(f)
}

// specFile is the path in the repository of the spec ticket t comes from (SpecKey), in NFC as
// resolve gives a new file, or "" when its metadata names none or none that can be a path in the
// repository. A spec run keeps a spec already in the repository where it is (docs/shop.md), and
// every ticket broken down from it cites it without changing it.
func specFile(t Ticket) string {
	var spec string
	if json.Unmarshal(t.metadataField(SpecKey), &spec) != nil {
		return ""
	}
	spec = norm.NFC.String(strings.TrimPrefix(strings.TrimSpace(spec), "./"))
	if !repoPath(spec) {
		return ""
	}
	return path.Clean(spec)
}

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
// in an existing folder. explicit, for a files metadata entry other than a glob (listed), also takes a
// folder (internal/dispatch/, or dispatch/) for the files under it and the folder itself, kept with a
// trailing slash in NFC, which holds the files under it other footprints name (takesIn): those another
// ticket adds. It keeps a name that means none of these as a file the ticket adds, even in a folder
// that doesn't exist yet (a project built from a spec), but never one that can't be a path in the
// repository (repoPath). Such a name written as a folder (web/src/, web/src/.) is kept as a folder,
// with its trailing slash. Without the repository's files, a name with a folder or a known extension
// is kept, but not the extension alone (.tsx). A name kept is cleaned (path.Clean) whether the files
// are known or not, and given in NFC when it isn't tracked.
func (r *repoFiles) resolve(name string, explicit bool) []string {
	name, folder := cleanName(name)
	if name == "" {
		return nil
	}
	folder = explicit && folder
	if r == nil || r.files == nil {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		// An extension alone (.tsx, as in "the .tsx files") is no file's name.
		named := sourceExtensions[ext] && !strings.EqualFold(strings.TrimLeft(name, "."), ext)
		switch {
		case folder:
			return []string{name + "/"}
		case explicit || strings.Contains(name, "/") || named:
			return []string{name}
		}
		return nil
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
		for _, d := range slices.Sorted(maps.Keys(r.dirs)) {
			if d == name || strings.HasSuffix(d, "/"+name) {
				found = append(found, d+"/")
			}
		}
	}
	switch {
	case len(found) > 0:
		return found
	case folder:
		return []string{name + "/"}
	case explicit || strings.Contains(name, "/") && r.dirs[path.Dir(name)]:
		return []string{name}
	}
	return nil
}

// cleanName is name as resolve compares it: in NFC, without a ./ before it and cleaned (path.Clean),
// and whether it is written as a folder (web/src/, web/src/.); "" for a name that can't be a path in
// the repository (repoPath).
func cleanName(name string) (clean string, folder bool) {
	name = norm.NFC.String(strings.TrimPrefix(name, "./"))
	if !repoPath(name) {
		return "", false
	}
	last := name[strings.LastIndex(name, "/")+1:]
	return path.Clean(name), last == "" || last == "." || last == ".."
}

// isGlob reports whether files metadata entry name is a glob: it has a *, a ? or a [.
func isGlob(name string) bool { return strings.ContainsAny(name, "*?[") }

// listed returns what files metadata entry name means: the files resolve gives it, or, for a glob
// (*.go, internal/**/*_test.go), the files it matches (glob) and the globs that hold the files other
// footprints name that it would match (Footprint.Globs): itself anchored at each folder it matched in,
// or, matching none of the repository's files (any, the files unknown), itself. A glob is cleaned and
// in NFC (web/src/*/ as web/src/*, whose last part holds a folder's files as glob's does).
func (r *repoFiles) listed(name string) (files, globs []string) {
	g, _ := cleanName(name)
	if !isGlob(g) {
		return r.resolve(name, true), nil
	}
	if r != nil {
		files, globs = r.glob(g)
	}
	if len(files) == 0 {
		return nil, []string{g}
	}
	return files, globs
}

// glob returns the files pattern matches, in any folder as a partial path does: its last part a
// file's name or a folder's, holding every file under it. * and ? match within a part of a path, ** any
// number of folders, and each other part the name written as it is too (nameMatch). anchored is pattern
// anchored at each folder it matched in, sorted and cleaned (dispatch/*.go as internal/dispatch/*.go),
// which holds the files another ticket adds there that it would match, from the repository's root
// (globHolds): matching in any folder, it would hold some file under every folder. A folder's name is a
// part of the glob as it is, matching itself (nameMatch) and, with a *, ? or [, other names besides.
func (r *repoFiles) glob(pattern string) (files, anchored []string) {
	pat := strings.Split(pattern, "/")
	at := map[string]bool{}
	for _, n := range slices.Sorted(maps.Keys(r.files)) {
		parts := strings.Split(n, "/")
		starts := len(parts)
		if pat[0] == "**" { // it matches from the root wherever it matches deeper
			starts = 1
		}
		matched := false
		for start := range starts {
			for end := start + 1; end <= len(parts); end++ {
				if globMatch(pat, parts[start:end]) {
					at[path.Join(append(slices.Clone(parts[:start]), pattern)...)] = true
					matched = true
					break
				}
			}
		}
		if matched {
			files = append(files, r.files[n]...)
		}
	}
	return files, slices.Sorted(maps.Keys(at))
}

// globMatch reports whether the parts of a path match the pattern's: ** any number of them, each other
// part one name (nameMatch).
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
	return nameMatch(pat[0], parts[0]) && globMatch(pat[1:], parts[1:])
}

// nameMatch reports whether pattern, a part of a glob, matches name, a part of a path: the names
// path.Match takes, and the name written as it is. A route of a Next.js or SvelteKit project is named
// with brackets (pages/[id].tsx, src/routes/[slug]/), which path.Match takes for a class (i.tsx or d.tsx).
func nameMatch(pattern, name string) bool {
	ok, _ := path.Match(pattern, name) // a malformed pattern matches nothing more
	return ok || pattern == name
}

// globHolds reports whether glob g, one of a footprint's Globs, holds p, a file or a folder (ending in
// /) of another's: a file g matches, or a folder holding one. g matches from the repository's root, as
// a name kept for a file a ticket adds does (web/src/*.ts holds web/src/cart.ts, not app/web/src/cart.ts):
// in any folder, as glob matches the repository's files, it would hold some file under every folder.
func globHolds(g, p string) bool { return partsMeet(globParts(g), pathParts(p)) }

// globsMeet reports whether two of footprints' Globs can match the same file.
func globsMeet(g, h string) bool { return g == h || partsMeet(globParts(g), globParts(h)) }

// part is a part of a path as partsMeet takes it: a glob's (glob), matching the names nameMatch does,
// or a file's or a folder's, matching its name alone.
type part struct {
	s    string
	glob bool
}

// anyFolders is **, the part of a glob matching any number of folders, which ends a folder's parts too.
var anyFolders = part{"**", true}

// matches reports whether p matches name n.
func (p part) matches(n string) bool { return p.s == n || p.glob && nameMatch(p.s, n) }

// globParts are glob g's parts as partsMeet takes them, followed by **: its last part a folder's name
// as well as a file's, as for glob.
func globParts(g string) []part {
	var parts []part
	for s := range strings.SplitSeq(g, "/") {
		parts = append(parts, part{s, true})
	}
	return append(parts, anyFolders)
}

// pathParts are the parts of p, a file or a folder (ending in /), in NFC as globs are, as partsMeet
// takes them, a folder's followed by ** for the files under it.
func pathParts(p string) []part {
	folder := strings.HasSuffix(p, "/")
	var parts []part
	for s := range strings.SplitSeq(strings.TrimSuffix(norm.NFC.String(p), "/"), "/") {
		parts = append(parts, part{s, false})
	}
	if folder {
		parts = append(parts, anyFolders)
	}
	return parts
}

// partsMeet reports whether a path can match both a and b, the parts of globs or paths: ** any number
// of folders, the others one name each, which partMeet compares.
func partsMeet(a, b []part) bool {
	// The ways tried that met nothing: each step moves on in a or b, so none comes round again.
	failed := make([]bool, (len(a)+1)*(len(b)+1))
	var meet func(i, j int) bool
	meet = func(i, j int) bool {
		if failed[i*(len(b)+1)+j] {
			return false
		}
		var ok bool
		switch {
		case i < len(a) && a[i] == anyFolders:
			ok = meet(i+1, j) || j < len(b) && meet(i, j+1)
		case j < len(b) && b[j] == anyFolders:
			ok = meet(i, j+1) || i < len(a) && meet(i+1, j)
		case i == len(a) || j == len(b):
			ok = i == len(a) && j == len(b)
		default:
			ok = partMeet(a[i], b[j]) && meet(i+1, j+1)
		}
		failed[i*(len(b)+1)+j] = !ok
		return ok
	}
	return meet(0, 0)
}

// partMeet reports whether a name can match both x and y: the name one of them is written as, which the
// other matches, or, both a glob's, one both match as path.Match patterns: the name one stands for alone
// (literal), which the other matches, or any name their literal beginnings and ends allow (*.ts and c*.ts
// meet, *.ts and *.go don't).
func partMeet(x, y part) bool {
	if x.matches(y.s) || y.matches(x.s) {
		return true
	}
	if !x.glob || !y.glob {
		return false
	}
	lx, xName := literal(x.s)
	ly, yName := literal(y.s)
	switch {
	case xName && yName:
		return lx == ly
	case yName:
		ok, _ := path.Match(x.s, ly) // a malformed pattern matches nothing
		return ok
	case xName:
		ok, _ := path.Match(y.s, lx)
		return ok
	}
	// A pattern's literal beginning ends at its first *, ?, [ or \, and its literal end starts after
	// its last one or ].
	px, py := x.s[:strings.IndexAny(x.s, `*?[\`)], y.s[:strings.IndexAny(y.s, `*?[\`)]
	sx, sy := x.s[strings.LastIndexAny(x.s, `*?[]\`)+1:], y.s[strings.LastIndexAny(y.s, `*?[]\`)+1:]
	return (strings.HasPrefix(px, py) || strings.HasPrefix(py, px)) &&
		(strings.HasSuffix(sx, sy) || strings.HasSuffix(sy, sx))
}

// literal returns the one name part s of a path matches (path.Match), its escapes undone, and false
// when it matches others (a *, a ? or a [class]) or none (a \ ending it).
func literal(s string) (string, bool) {
	if !strings.ContainsAny(s, `*?[\`) {
		return s, true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[':
			return "", false
		case '\\':
			if i++; i == len(s) {
				return "", false
			}
		}
		b.WriteByte(s[i])
	}
	return b.String(), true
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
// aren't known. The files the project's check commands (check, one a line) name, specs under .orchestra/specs/
// and the spec its metadata names (SpecKey) count only when its files metadata lists them. With
// nothing named or predicted it is empty, and the ticket runs beside anything.
func TicketFootprint(t Ticket, tracked []string, check string) Footprint {
	return ticketFootprint(t, newRepoFiles(tracked, check))
}

// eachPathToken calls take with each word of text that may be a path: with an extension, and
// neither an absolute path nor a URL, which aren't in the repository. take reports whether the word
// names something. A + starts a word only after a space or punctuation that isn't a closing bracket:
// after a letter, digit, mark, _ . - + or a closing bracket it joins two words of an expression
// (a+b.go, x[0]+y.go, C++), and is left off. A word with brackets or a + that names nothing is tried
// in its parts without them instead, as the words they were before brackets and a + were taken in:
// an index expression (Footprint.Files[0]) and a + marking an added line (+loop.go) are among them.
// A word with characters outside ASCII that names nothing is tried in its ASCII parts: a path run
// into words of a script written without spaces (修改loop.go, loop.goを直す) is one of them.
func eachPathToken(text string, take func(tok string) bool) {
	for _, m := range pathToken.FindAllStringIndex(text, -1) {
		word := text[m[0]:m[1]]
		if before, _ := utf8.DecodeLastRuneInString(text[:m[0]]); m[0] > 0 && joinsWords(before) {
			word = strings.TrimPrefix(word, "+")
		}
		takePath(word, take)
	}
}

// joinsWords reports whether r, the character before a + starting a path token, makes the + join two
// words of an expression rather than start a name: a letter, digit, mark, _ . - + or a closing bracket.
func joinsWords(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) || strings.ContainsRune("_.-+)]}", r)
}

// takePath calls take with word, a path token, or its parts, as eachPathToken says.
func takePath(word string, take func(tok string) bool) {
	word = strings.TrimRight(word, "./-")
	if strings.HasPrefix(word, "/") { // an absolute path or a URL, no part of which is in the repository
		return
	}
	switch {
	case extension.MatchString(word) && take(word):
	case strings.ContainsAny(word, "[]+"):
		for _, part := range plainPathToken.FindAllString(word, -1) {
			takePath(part, take)
		}
	case strings.ContainsFunc(word, func(r rune) bool { return r > unicode.MaxASCII }):
		for _, part := range asciiPathToken.FindAllString(word, -1) {
			if part = strings.TrimRight(part, "./-"); !strings.HasPrefix(part, "/") && extension.MatchString(part) {
				take(part)
			}
		}
	}
}

func ticketFootprint(t Ticket, repo *repoFiles) Footprint {
	text := strings.Join([]string{t.Title, t.Description, t.Design, t.AcceptanceCriteria, t.Notes}, "\n")
	spec := specFile(t)
	files, funcs := pathsIn(text, repo, spec)
	maps.Copy(funcs, callsIn(text))
	maps.Copy(funcs, codeSpansIn(text))
	globs := map[string]bool{}
	var fp Footprint
	listed := metadataList(t, FilesKey)
	if len(listed) > 0 { // the files it changes are listed: those its text names it reads
		for f := range files {
			files[f] = false
		}
	}
	fp.Listed = len(listed) > 0
	for _, f := range listed {
		found, gs := repo.listed(f)
		for _, p := range found {
			files[p] = true
		}
		for _, g := range gs {
			globs[g] = true
		}
	}
	maps.DeleteFunc(files, func(f string, _ bool) bool { return repo.unioned(f) })
	for _, l := range t.Labels {
		if strings.HasPrefix(l, AreaPrefix) && len(l) > len(AreaPrefix) {
			fp.Areas = append(fp.Areas, l)
		}
	}
	fp.Files, fp.Funcs = slices.Sorted(maps.Keys(files)), slices.Sorted(maps.Keys(funcs))
	for _, f := range fp.Files {
		if !files[f] {
			fp.Cited = append(fp.Cited, f)
		}
	}
	fp.Globs = slices.Sorted(maps.Keys(globs))
	sort.Strings(fp.Areas)
	if fp.Empty() {
		return predictedFootprint(metadataList(t, PredictedKey), repo, spec)
	}
	return fp
}

// pathsIn returns the repository files the words of text that may be paths name, but those named in
// passing (inPassing) by a ticket from spec, each with whether text asks to change it (asksChange), and
// the Type.method words among them that name no file.
func pathsIn(text string, repo *repoFiles, spec string) (files, funcs map[string]bool) {
	files, funcs = map[string]bool{}, map[string]bool{}
	for _, s := range sentences(text) {
		// A path token holds no space, so each is in one word, and starts it as it would the text.
		words := strings.Fields(s)
		for i, w := range words {
			eachPathToken(w, func(tok string) bool {
				if found := repo.resolve(tok, false); len(found) > 0 {
					for _, f := range found {
						if !repo.inPassing(f, spec) { // "scripts/check.sh passes" says nothing about where a ticket works
							files[f] = files[f] || !document(f) || asksChange(words, i)
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
		}
	}
	return files, funcs
}

// Around a document a sentence names, the words a changeWord asking to change it is looked for in.
const (
	changeBefore = 8 // "Add the result to docs/spikes/07-compile.md", "pin the rule in a test and in docs/running.md"
	changeAfter  = 3 // "docs/running.md updated", "docs/running.md or docs/workers.md says how"
)

// citeWords are the words that, among the two before a document, cite it: "per docs/development.md", "as
// docs/running.md says", "following docs/guide.md's conventions".
var citeWords = setOf(`per see following as like cf according via`)

// asksChange reports whether a sentence, as its words, asks to change the document its i-th word names:
// the document starts it, after a list's marker ("- docs/running.md: say …", "CLAUDE.md and
// docs/development.md say …"), or, unless one of the two words before it is a citeWord, a changeWord
// is among the changeBefore words before it (none when it opens a parenthesis) or the changeAfter
// words after it, or a changesWord among those after it. A sentence citing it ("Reproduce per
// docs/development.md", "Spike 1 (docs/spikes/01-sdk.md, item 4) found that …") does none of these.
func asksChange(words []string, i int) bool {
	first := slices.IndexFunc(words, func(w string) bool { return strings.ContainsFunc(w, unicode.IsLetter) })
	if i == first {
		return true
	}
	for _, w := range words[max(0, i-2):i] {
		if citeWords[strings.ToLower(strings.TrimFunc(w, func(r rune) bool { return !unicode.IsLetter(r) }))] {
			return false
		}
	}
	before := ""
	if !strings.HasPrefix(words[i], "(") {
		before = strings.Join(words[max(0, i-changeBefore):i], " ")
	}
	after := strings.Join(words[i+1:min(len(words), i+1+changeAfter)], " ")
	return changeWord.MatchString(before) || changeWord.MatchString(after) || changesWord.MatchString(after)
}

// sentences splits text into its sentences (sentenceBreak), each with the break that ends it.
func sentences(text string) []string {
	var out []string
	start := 0
	for _, m := range sentenceBreak.FindAllStringIndex(text, -1) {
		if next, _ := utf8.DecodeRuneInString(text[m[1]:]); text[m[0]] != '\n' && unicode.IsLower(next) {
			continue // e.g. docs/x.md: the sentence goes on
		}
		out = append(out, text[start:m[1]])
		start = m[1]
	}
	return append(out, text[start:])
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

// predictedFootprint is the footprint of a ticket from spec (specFile) naming nothing, from the files
// predicted for it but those named in passing (inPassing) and those git merges by union (unioned).
func predictedFootprint(predicted []string, repo *repoFiles, spec string) Footprint {
	files := map[string]bool{}
	for _, f := range predicted {
		for _, p := range repo.resolve(f, false) {
			if !repo.inPassing(p, spec) && !repo.unioned(p) {
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

// takesIn reports whether footprint fp takes in p, a file or a folder: p itself, a folder among its
// files (one kept with its trailing slash, web/src/) that p is under (web/src/cart.ts, web/src/ui/),
// or one of its globs holding p (globHolds).
func takesIn(fp Footprint, p string) bool {
	return slices.ContainsFunc(fp.Files, func(f string) bool {
		return f == p || strings.HasSuffix(f, "/") && strings.HasPrefix(p, f)
	}) || slices.ContainsFunc(fp.Globs, func(g string) bool { return globHolds(g, p) })
}

// changes reports whether footprint fp is to change p, a file or a folder: it takes p in (takesIn) by a
// file or folder it doesn't only cite (Cited) or by a glob, or, named true, it names a function p
// mentions while its files metadata lists none (Listed) and p is no document, which mentions functions
// without holding them.
func changes(fp Footprint, p string, named bool) bool {
	if named && !fp.Listed && !document(p) {
		return true
	}
	return slices.ContainsFunc(fp.Files, func(f string) bool {
		return (f == p || strings.HasSuffix(f, "/") && strings.HasPrefix(p, f)) && !slices.Contains(fp.Cited, f)
	}) || slices.ContainsFunc(fp.Globs, func(g string) bool { return globHolds(g, p) })
}

// meetsGlob reports whether footprint fp works where glob g does: on a file or folder g holds, or by a
// glob of its own that can match a file g does.
func meetsGlob(fp Footprint, g string) bool {
	return slices.ContainsFunc(fp.Files, func(p string) bool { return globHolds(g, p) }) ||
		slices.ContainsFunc(fp.Globs, func(h string) bool { return globsMeet(g, h) })
}

// holdsNamed reports whether p is a folder (one kept with its trailing slash) holding a file among
// files, not a folder: one that exists, or one a ticket adds in it.
func holdsNamed(p string, files []string) bool {
	return strings.HasSuffix(p, "/") && slices.ContainsFunc(files, func(f string) bool {
		return !strings.HasSuffix(f, "/") && strings.HasPrefix(f, p)
	})
}

// sharedFile returns a file both footprints work on, for which keep holds (any, when keep is nil),
// or "". A ticket works on a file it names or one under a folder it names (takesIn), and on one the
// other ticket names or r's worker edited (edited) that mentions a function it names (in reports
// whether one does), and a file both work on is shared unless both name functions it mentions: they
// are compared by function (sharedFunc). A folder both work on is shared only when neither names a
// file in it (a new folder): the files they name in it, those it holds when it exists, are compared
// one by one instead, by function where both name some. A folder mentions no function. A glob (Globs)
// works on the files it holds (globHolds), and is compared itself, as a new folder is, only when it
// holds no file either names: its files are compared one by one too. A file both work on is shared only
// when one of them is to change it (changes), or r's worker edited it: two tickets that only cite a
// document, or name files their files metadata doesn't list, don't conflict over it.
func sharedFile(f, r Footprint, edited []string, in func(file, fn string) bool, keep func(string) bool) string {
	namesIn := func(fp Footprint, p string) bool {
		folder := strings.HasSuffix(p, "/")
		return in != nil && !folder && slices.ContainsFunc(fp.Funcs, func(fn string) bool { return in(p, fn) })
	}
	seen := map[string]bool{}
	for _, p := range slices.Concat(f.Files, r.Files, edited) {
		if seen[p] || holdsNamed(p, f.Files) || holdsNamed(p, r.Files) {
			continue
		}
		seen[p] = true
		fIn, rIn := namesIn(f, p), namesIn(r, p)
		fAt := fIn || takesIn(f, p)
		rAt := rIn || takesIn(r, p) || slices.Contains(edited, p)
		change := changes(f, p, fIn) || changes(r, p, rIn) || slices.Contains(edited, p)
		if fAt && rAt && !(fIn && rIn) && change && (keep == nil || keep(p)) {
			return p
		}
	}
	named := slices.Concat(f.Files, r.Files)
	for _, g := range slices.Concat(f.Globs, r.Globs) {
		holdsFile := slices.ContainsFunc(named, func(p string) bool { return !strings.HasSuffix(p, "/") && globHolds(g, p) })
		if !holdsFile && meetsGlob(f, g) && meetsGlob(r, g) && (keep == nil || keep(g)) {
			return g
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
	r.files = newRepoFiles(r.o.checkout.TrackedFiles(ctx, c.Repo), checkCommands(c))
	r.files.read = func(file string) ([]byte, error) { return ReadSource(c.Repo, file) }
	r.files.union = r.mergesByUnion
}

// mergesByUnion reports whether git merges repository file f by union, as .gitattributes says
// (git check-attr merge), asking git once a file a run. An answer git couldn't give isn't kept.
func (r *runState) mergesByUnion(f string) bool {
	r.unionMu.Lock()
	defer r.unionMu.Unlock()
	if u, ok := r.unions[f]; ok {
		return u
	}
	merge, err := r.o.checkout.Attribute(r.ctx, r.o.cfg.Repo, "merge", f)
	if err != nil {
		return false
	}
	r.unions[f] = merge == "union"
	return r.unions[f]
}

// checkCommands are the project's check commands, the merge check's, the full check's and a worker's
// own, one a line, whose files footprints leave out: a ticket asks for them to pass, and a merge
// check that calls the full one (scripts/check.sh -short) names only its own script.
func checkCommands(c Config) string { return c.Check + "\n" + c.CheckFull + "\n" + c.CheckOwn }

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
			fp = predictedFootprint(files, r.files, specFile(t))
		}
	}
	return fp
}

// startFootprint records a dispatched ticket's footprint, and logs it. A ticket adopted as the run
// starts comes before the first pick has read the repository's files, so they are read for it:
// its footprint is kept for its whole run, and compared with ready tickets' in the repository's paths.
func (r *runState) startFootprint(t Ticket) {
	o := r.o
	if !o.footprintOn() {
		return
	}
	if r.files == nil {
		r.readFiles(r.ctx)
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
// conflict. The files git merges by union are left out: both workers' lines are kept.
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
		var files []string
		for _, f := range o.reporter.EditedFiles(wt) {
			if f = r.files.file(f); !r.files.unioned(f) { // in the form the footprints' files are in
				files = append(files, f)
			}
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
