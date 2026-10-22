package dispatch

import (
	"errors"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// A ticket that names a section of a Markdown document (docs/running.md § "Solo tickets", or
// docs/running.md#solo-tickets) works on that section alone, as one naming a function works on it
// alone: git merges edits to different sections of a document cleanly. Two tickets on one document
// overlap only when the sections they name meet (sectionsMeet), or one names the document without a
// section; a running ticket's worker overlaps a ticket on a section when it has edited that section
// (changedSections). A section is kept as its path: the anchors of its heading and of the headings it
// is under, outermost first, joined by / (running-orchestra/solo-tickets). An anchor holds no /.

// markdownExtensions are the extensions of the documents whose sections a ticket can name: Markdown's,
// whose headings are anchors.
var markdownExtensions = setOf(`md markdown mdx`)

// markdown reports whether p, a file or a folder, is a Markdown document.
func markdown(p string) bool {
	return !strings.HasSuffix(p, "/") && markdownExtensions[strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))]
}

var (
	// sectionRef is what follows a document's name to name one of its sections: an anchor
	// (#solo-tickets), or a § and the heading, quoted ("Solo tickets", “Solo tickets”, 'Solo tickets',
	// `Solo tickets`) or not (§ Solo tickets, up to the next punctuation), after the quote or bracket
	// closing the name and a bracket opening.
	sectionRef = regexp.MustCompile(`^(?:#([\p{L}\p{M}\p{N}_-]+)|` + "[`*\"'’”]*" + `\s*\(?§\s*(?:` +
		`"([^"\n]+)"|“([^”\n]+)”|'([^'\n]+)'|‘([^’\n]+)’|` + "`([^`\n]+)`" +
		`|([^\s,;:()\[\]"“”.!?§][^,;:()\[\]"“”.!?§\n]*)))`)
	// lineAnchor is the anchor of a line or lines of a file as GitHub shows it (#L120, #L120-L140): no
	// section.
	lineAnchor = regexp.MustCompile(`^[Ll]\d+(?:-[Ll]\d+)?$`)
	// atxHeading is a Markdown heading line (## Solo tickets ##): its #s and its text.
	atxHeading = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	// linkTarget is the target of a Markdown link, which its heading's anchor leaves out.
	linkTarget = regexp.MustCompile(`\]\([^)]*\)`)
)

// sectionName is a section of a document as a ticket names it: its heading or its anchor, exact
// (quoted, or an anchor) or not, when the heading ends where the ticket's words do (§ Solo tickets
// and the rest).
type sectionName struct {
	text  string
	exact bool
}

// sectionAfter returns the section the text after a document's name names, as sectionRef reads it,
// and false when it names none.
func sectionAfter(s string) (sectionName, bool) {
	m := sectionRef.FindStringSubmatch(s)
	if m == nil {
		return sectionName{}, false
	}
	if m[1] != "" {
		if lineAnchor.MatchString(m[1]) {
			return sectionName{}, false
		}
		return sectionName{m[1], true}, true
	}
	for _, q := range m[2:7] {
		if q = strings.TrimSpace(q); q != "" {
			return sectionName{q, true}, true
		}
	}
	return sectionName{strings.TrimSpace(m[7]), false}, true
}

// splitSection splits a files metadata entry into the document it names and the section, nil for
// none: docs/running.md#solo-tickets, docs/running.md § Solo tickets.
func splitSection(entry string) (string, *sectionName) {
	i := strings.IndexAny(entry, "#§")
	if i <= 0 {
		return entry, nil
	}
	name := strings.TrimSpace(entry[:i])
	if !markdown(name) {
		return entry, nil
	}
	sec, ok := sectionAfter(entry[i:])
	if !ok {
		return name, nil
	}
	return name, &sec
}

// anchor is a heading's anchor as Markdown renders it (GitHub's): its text in lower case without
// link targets, its letters, digits, marks, - and _ kept, each space made a -, the rest dropped, in NFC.
func anchor(heading string) string {
	heading = linkTarget.ReplaceAllString(heading, "]")
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(heading)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r):
			b.WriteRune(r)
		}
	}
	return norm.NFC.String(b.String())
}

// docSection is a section of a Markdown document, from its heading to the next of any level: its
// path, and its text. What comes before the first heading has the path "".
type docSection struct{ path, text string }

// docSections splits a Markdown document at its headings (ATX: ## Solo tickets), not counting the
// lines of a fenced code block.
func docSections(doc string) []docSection {
	type heading struct {
		level  int
		anchor string
	}
	var (
		out   []docSection
		under []heading
		cur   docSection
		start int
		fence string
	)
	at := 0
	for line := range strings.SplitAfterSeq(doc, "\n") {
		text := strings.TrimRight(line, "\r\n")
		if f := fenceOf(text); f != "" && (fence == "" || f[0] == fence[0] && len(f) >= len(fence)) {
			if fence == "" {
				fence = f
			} else {
				fence = ""
			}
		} else if m := atxHeading.FindStringSubmatch(text); m != nil && fence == "" {
			cur.text = doc[start:at]
			out = append(out, cur)
			for len(under) > 0 && under[len(under)-1].level >= len(m[1]) {
				under = under[:len(under)-1]
			}
			under = append(under, heading{len(m[1]), anchor(m[2])})
			anchors := make([]string, len(under))
			for i, h := range under {
				anchors[i] = h.anchor
			}
			cur, start = docSection{path: strings.Join(anchors, "/")}, at
		}
		at += len(line)
	}
	cur.text = doc[start:]
	return append(out, cur)
}

// fenceOf returns the fence a line of Markdown opens or closes a code block with (``` or ~~~, or
// longer), or "".
func fenceOf(line string) string {
	t := strings.TrimLeft(line, " ")
	if len(line)-len(t) > 3 || len(t) < 3 || t[0] != '`' && t[0] != '~' {
		return ""
	}
	n := len(t) - len(strings.TrimLeft(t, t[:1]))
	if n < 3 {
		return ""
	}
	return t[:n]
}

// headingPaths returns the paths of a Markdown document's sections, by the anchor of their heading.
func headingPaths(doc string) map[string][]string {
	paths := map[string][]string{}
	for _, s := range docSections(doc)[1:] {
		a := s.path[strings.LastIndex(s.path, "/")+1:]
		if !slices.Contains(paths[a], s.path) {
			paths[a] = append(paths[a], s.path)
		}
	}
	return paths
}

// headings returns the paths of repository document file's sections by their anchor (headingPaths),
// nil when it can't be read.
func (r *repoFiles) headings(file string) map[string][]string {
	if r == nil || r.read == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	h, ok := r.heads[file]
	if !ok {
		if b, err := r.read(file); err == nil {
			h = headingPaths(string(b))
		}
		if r.heads == nil {
			r.heads = map[string]map[string][]string{}
		}
		r.heads[file] = h
	}
	return h
}

// sectionPaths returns the paths of the sections of document doc that n names, and false when n
// names it whole: an exact name the paths of the headings with its anchor, or, for none (a section the
// ticket adds, or the document unread), the anchor alone; a name that isn't exact the paths of the
// headings with the anchor of its longest run of first words, and with none, the whole document.
func (r *repoFiles) sectionPaths(doc string, n sectionName) ([]string, bool) {
	heads := r.headings(doc)
	if n.exact {
		a := anchor(n.text)
		if ps := heads[a]; len(ps) > 0 {
			return ps, true
		}
		return []string{a}, a != ""
	}
	words := strings.Fields(n.text)
	for k := len(words); k > 0; k-- {
		if ps := heads[anchor(strings.Join(words[:k], " "))]; len(ps) > 0 {
			return ps, true
		}
	}
	return nil, false
}

// docRefs are the mentions of each Markdown document a ticket names, each with the section it names,
// or nil where it names the document whole.
type docRefs map[string][]*sectionName

// sectionsOf returns the paths of the sections refs names of each document, by document: none for a
// document a mention names whole, or names by a section that stands for the whole (sectionPaths).
func (r *repoFiles) sectionsOf(refs docRefs) map[string][]string {
	var out map[string][]string
	for doc, names := range refs {
		var paths []string
		whole := len(names) == 0
		for _, n := range names {
			if n == nil {
				whole = true
				break
			}
			ps, ok := r.sectionPaths(doc, *n)
			if !ok {
				whole = true
				break
			}
			paths = append(paths, ps...)
		}
		if whole {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		slices.Sort(paths)
		out[doc] = slices.Compact(paths)
	}
	return out
}

// within reports whether the anchors of section path a run, one after another, within path b's: a is
// b, or a section b is under, or has b's last anchors (a section a ticket adds, or names in a document
// unread).
func within(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return b == a || strings.HasPrefix(b, a+"/") || strings.HasSuffix(b, "/"+a) || strings.Contains(b, "/"+a+"/")
}

// sectionsMeet reports whether two tickets' sections of a document meet: one is, or is under, the other.
func sectionsMeet(a, b []string) bool {
	return slices.ContainsFunc(a, func(x string) bool {
		return slices.ContainsFunc(b, func(y string) bool { return within(x, y) || within(y, x) })
	})
}

// editsMeet reports whether a ticket's sections of a document hold a section a worker edited (edits,
// as changedSections gives them): text under a section's heading but before its first subsection's
// is no subsection's.
func editsMeet(named, edits []string) bool {
	return slices.ContainsFunc(named, func(n string) bool {
		return slices.ContainsFunc(edits, func(e string) bool { return within(n, e) })
	})
}

// sectionShared reports whether ready ticket f and running ticket r, both at document p, work on a
// section of it in common: f on the sections it names of it (Sections), or on all of it when it names
// none or names a function p mentions (fIn); r on those it names, when it names p (or all of it, for
// none, or a function p mentions), and those its worker edited (edits), when it edited p (edited), or
// all of it for none. A file that is no Markdown document has no sections.
func sectionShared(f, r Footprint, p string, fIn, rIn, edited bool) bool {
	fs, ok := f.Sections[p]
	if !ok || fIn {
		return true
	}
	if rIn || takesIn(r, p) {
		if rs, ok := r.Sections[p]; !ok || rIn || sectionsMeet(fs, rs) {
			return true
		}
	}
	if edited {
		if es, ok := r.edits[p]; !ok || editsMeet(fs, es) {
			return true
		}
	}
	return false
}

// changedSections returns the paths of the sections of a Markdown document whose text differs between
// before and after, or that only one of them has, sorted. Sections of one path are taken together.
func changedSections(before, after string) []string {
	texts := func(doc string) map[string]string {
		m := map[string]string{}
		for _, s := range docSections(doc) {
			m[s.path] += s.text
		}
		return m
	}
	b, a := texts(before), texts(after)
	changed := map[string]bool{}
	for p, t := range a {
		if old, ok := b[p]; !ok || old != t {
			changed[p] = true
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			changed[p] = true
		}
	}
	return slices.Sorted(maps.Keys(changed))
}

// editsApart reports whether two workers' edits to file, as changedSections gives them by file (a and
// b), are to different sections of it: both known, and none in common.
func editsApart(a, b map[string][]string, file string) bool {
	as, aok := a[file]
	bs, bok := b[file]
	return aok && bok && !slices.ContainsFunc(as, func(p string) bool { return slices.Contains(bs, p) })
}

// editedSections returns, for each Markdown document among files that ticket id's worker edited in its
// worktree wt, the sections it changed (changedSections) since its branch's merge base with Base: of
// one it removed, every section. A document whose text, or the merge base, can't be read is left out,
// to count whole.
func (r *runState) editedSections(id, wt string, files []string) map[string][]string {
	var out map[string][]string
	for _, f := range files {
		if !markdown(f) {
			continue
		}
		after, err := ReadSource(wt, f)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			after = nil // removed: every section of it
		case err != nil:
			continue // unreadable: the whole document
		}
		base := r.docBase(id)
		if base == "" {
			return out
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[f] = changedSections(r.baseDoc(base, f), string(after))
	}
	return out
}

// docBase is the merge base of ticket id's branch with Base, which its worker's edits to documents are
// read from; "" when git can't say. It is asked once a ticket while it runs (endFootprint forgets it).
func (r *runState) docBase(id string) string {
	r.docMu.Lock()
	base, ok := r.docBases[id]
	r.docMu.Unlock()
	if ok {
		return base
	}
	o, c := r.o, r.o.cfg
	base, err := o.merger.MergeBase(r.ctx, c.Repo, c.Base, branchOf(id))
	if err != nil {
		o.log.Raw("", err)
		return ""
	}
	r.docMu.Lock()
	r.docBases[id] = base
	r.docMu.Unlock()
	return base
}

// baseDoc is document file's text at commit base, "" when it has none (a document the worker adds),
// asking git once a run.
func (r *runState) baseDoc(base, file string) string {
	key := base + "\x00" + file
	r.docMu.Lock()
	text, ok := r.baseDocs[key]
	r.docMu.Unlock()
	if ok {
		return text
	}
	text, err := r.o.checkout.FileAt(r.ctx, r.o.cfg.Repo, base, file)
	if err != nil {
		text = "" // git has it at no such commit: every section is the worker's
	}
	r.docMu.Lock()
	r.baseDocs[key] = text
	r.docMu.Unlock()
	return text
}
