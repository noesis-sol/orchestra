package dispatch

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// trackedWithOwnSpec is trackedHere with a spec a spec run kept where it was in the repository.
var trackedWithOwnSpec = append(slices.Clone(trackedHere), "docs/shop-spec.md")

// fromSpec is a ticket broken down from the spec at spec, as its metadata names it (SpecKey), with
// more metadata (extra, "" for none) and text.
func fromSpec(spec, extra, title, text string) Ticket {
	meta := map[string]any{SpecKey: spec}
	if extra != "" {
		var more map[string]any
		_ = json.Unmarshal([]byte(extra), &more) // the tests' own metadata, always an object
		maps.Copy(meta, more)
	}
	b, _ := json.Marshal(meta) // strings and lists of them always encode
	return Ticket{Title: title, Description: text, Metadata: b}
}

// Tickets broken down from a spec in the repository cite it, by its path, with a line number, a
// partial path or its bare name: the spec their metadata names is left out of their footprints, as
// specs under .orchestra/specs/ are, so they run side by side. The metadata may name it with ./ or
// spaces, or hold it encoded in a string, as bd may give it.
func TestTicketsFromASpecInTheRepositoryRunSideBySide(t *testing.T) {
	for _, spec := range []string{"docs/shop-spec.md", "./docs/shop-spec.md", " docs//shop-spec.md\n"} {
		for _, cite := range []string{"docs/shop-spec.md", "./docs/shop-spec.md", "docs/shop-spec.md:40", "shop-spec.md"} {
			for _, tracked := range [][]string{trackedWithOwnSpec, nil} {
				if cite == "shop-spec.md" && tracked == nil {
					continue // a bare name finds its folder only among the repository's files
				}
				a := fromSpec(spec, "", "Add the cart", "Build the cart, as "+cite+" says under Cart.")
				b := fromSpec(spec, "", "Add checkout", "See "+cite+", Checkout. Edit internal/tui/run.go.")
				fa, fb := TicketFootprint(a, tracked, ""), TicketFootprint(b, tracked, "")
				if !fa.Empty() {
					t.Errorf("spec %q, citing %s (files known: %t): footprint %+v, want nothing named",
						spec, cite, tracked != nil, fa)
				}
				if want := []string{"internal/tui/run.go"}; !slices.Equal(fb.Files, want) {
					t.Errorf("spec %q, citing %s (files known: %t): files %q, want %q", spec, cite, tracked != nil, fb.Files, want)
				}
				if what := shared(fb, fa, nil, nil) + shared(fa, fb, nil, nil); what != "" {
					t.Errorf("spec %q, citing %s (files known: %t): the tickets share %q", spec, cite, tracked != nil, what)
				}
			}
		}
	}

	inString := Ticket{Title: "Add the cart", Description: "Build the cart, as docs/shop-spec.md says.",
		Metadata: []byte(`"{\"spec\":\"docs/shop-spec.md\"}"`)}
	if fp := TicketFootprint(inString, trackedWithOwnSpec, ""); !fp.Empty() {
		t.Errorf("the metadata encoded in a string: footprint %+v, want nothing named", fp)
	}

	// Only the spec itself is left out: a file of the same name elsewhere still counts.
	elsewhere := append(slices.Clone(trackedWithOwnSpec), "web/shop-spec.md")
	fp := TicketFootprint(fromSpec("docs/shop-spec.md", "", "Add the cart", "Rewrite shop-spec.md."), elsewhere, "")
	if want := []string{"web/shop-spec.md"}; !slices.Equal(fp.Files, want) {
		t.Errorf("a bare name meaning the spec and web/shop-spec.md: files %q, want %q", fp.Files, want)
	}
}

// A ticket listing its spec in its files metadata works on it: it overlaps another listing it, and
// one naming it without coming from it.
func TestATicketListingItsSpecCountsIt(t *testing.T) {
	for _, tracked := range [][]string{trackedWithOwnSpec, nil} {
		listed := fromSpec("docs/shop-spec.md", `{"files": ["docs/shop-spec.md"]}`,
			"Revise the spec", "Reword docs/shop-spec.md's Cart.")
		fp := TicketFootprint(listed, tracked, "")
		if want := []string{"docs/shop-spec.md"}; !slices.Equal(fp.Files, want) {
			t.Errorf("listing the spec (files known: %t): files %q, want %q", tracked != nil, fp.Files, want)
		}
		if what := shared(fp, fp, nil, nil); what != "docs/shop-spec.md" {
			t.Errorf("listing the spec (files known: %t): two such tickets share %q", tracked != nil, what)
		}
		other := TicketFootprint(Ticket{Title: "Fix a typo", Description: "Fix a typo in docs/shop-spec.md."}, tracked, "")
		if what := shared(other, fp, nil, nil); what != "docs/shop-spec.md" {
			t.Errorf("listing the spec (files known: %t): a ticket naming it shares %q with one listing it",
				tracked != nil, what)
		}
	}
}

// A ticket that doesn't come from the spec counts it when it names it: one without spec metadata,
// one from another spec, and one whose spec metadata is no path in the repository.
func TestATicketFromNoSpecCountsTheSpec(t *testing.T) {
	text := "Fix a typo in docs/shop-spec.md."
	for name, tk := range map[string]Ticket{
		"no metadata":             {Title: "Fix a typo", Description: text},
		"another spec":            fromSpec("docs/other-spec.md", "", "Fix a typo", text),
		"another spec, same name": fromSpec("web/shop-spec.md", "", "Fix a typo", text),
		"an empty spec":           fromSpec("", "", "Fix a typo", text),
		"the repository's root":   fromSpec("./", "", "Fix a typo", text),
		"a spec outside":          fromSpec("../docs/shop-spec.md", "", "Fix a typo", text),
		"an absolute spec":        fromSpec("/docs/shop-spec.md", "", "Fix a typo", text),
		"a list":                  {Title: "Fix a typo", Description: text, Metadata: []byte(`{"spec": ["docs/shop-spec.md"]}`)},
		"a number":                {Title: "Fix a typo", Description: text, Metadata: []byte(`{"spec": 3}`)},
	} {
		for _, tracked := range [][]string{trackedWithOwnSpec, nil} {
			fp := TicketFootprint(tk, tracked, "")
			if want := []string{"docs/shop-spec.md"}; !slices.Equal(fp.Files, want) {
				t.Errorf("%s (files known: %t): files %q, want %q", name, tracked != nil, fp.Files, want)
			}
		}
	}
}

// The spec's name may be in either Unicode form, in its metadata, in the text and as git lists it.
func TestTheOwnSpecInEitherUnicodeForm(t *testing.T) {
	nfc, nfd := "docs/café.md", norm.NFD.String("docs/café.md")
	for _, listed := range []string{nfc, nfd} {
		for _, spec := range []string{nfc, nfd} {
			for _, cite := range []string{nfc, nfd} {
				tracked := append(slices.Clone(trackedHere), listed)
				tk := fromSpec(spec, "", "Add the cart", "Build the cart, as "+cite+" says. Edit README.md.")
				if fp := TicketFootprint(tk, tracked, ""); !slices.Equal(fp.Files, []string{"README.md"}) {
					t.Errorf("listed %+q, spec %+q, cited %+q: files %+q, want only README.md", listed, spec, cite, fp.Files)
				}
			}
		}
	}
}

// The files predicted for a ticket from a spec leave the spec out too: those cached on it, those
// predicted in this run for a ready ticket, and those given a running one.
func TestPredictedFilesLeaveOutTheOwnSpec(t *testing.T) {
	cached := fromSpec("docs/shop-spec.md", `{"predicted_files": "docs/shop-spec.md,README.md"}`,
		"Add the cart", "Build the cart, as docs/shop-spec.md says.")
	want := Footprint{Files: []string{"README.md"}, Predicted: true}
	if fp := TicketFootprint(cached, trackedWithOwnSpec, ""); !slices.Equal(fp.Files, want.Files) || !fp.Predicted {
		t.Errorf("cached: %+v, want %+v", fp, want)
	}

	tk := fromSpec("docs/shop-spec.md", "", "Add the cart", "Build the cart, as docs/shop-spec.md says.")
	tk.ID = "A"
	o := &Loop{predictor: newPredictor(), ledger: newLedger()}
	o.footprints["A"] = &runFootprint{}
	o.setPrediction("A", []string{"docs/shop-spec.md", "README.md"})
	r := &runState{o: o, files: newRepoFiles(trackedWithOwnSpec, "")}
	if fp := r.footprintOf(tk); !slices.Equal(fp.Files, want.Files) || !fp.Predicted {
		t.Errorf("predicted in this run: %+v, want %+v", fp, want)
	}
	o.givePrediction(tk, []string{"docs/shop-spec.md", "README.md"}, trackedWithOwnSpec)
	if fp := o.footprints["A"].fp; !slices.Equal(fp.Files, want.Files) || !fp.Predicted {
		t.Errorf("given the running ticket: %+v, want %+v", fp, want)
	}
}

// orchestra plan reads the same footprints: tickets citing their spec in the repository get no link
// for it, and tickets listing it get one.
func TestPlanLinksLeaveOutTheOwnSpec(t *testing.T) {
	open := []Ticket{
		planTicket("a", 1, "", "Build the cart, as docs/shop-spec.md says under Cart."),
		planTicket("b", 2, "", "Build checkout, as docs/shop-spec.md says under Checkout."),
	}
	for i := range open {
		open[i].Metadata = []byte(`{"spec": "docs/shop-spec.md"}`)
	}
	if got := PlanLinks(open, nil, trackedWithOwnSpec, "", sourceOf(nil), nil); len(got) != 0 {
		t.Errorf("links %q, want none", linkList(got))
	}

	for i := range open {
		open[i].Metadata = []byte(`{"spec": "docs/shop-spec.md", "files": ["docs/shop-spec.md"]}`)
	}
	got := linkList(PlanLinks(open, nil, trackedWithOwnSpec, "", sourceOf(nil), nil))
	if want := []string{"a>b:docs/shop-spec.md"}; !slices.Equal(got, want) {
		t.Errorf("both listing the spec: links %q, want %q", got, want)
	}
}
