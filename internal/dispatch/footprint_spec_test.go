package dispatch

import (
	"slices"
	"testing"
)

// trackedWithSpec is trackedHere with a spec committed from outside the repository.
var trackedWithSpec = append(slices.Clone(trackedHere), ".orchestra/specs/shop.md")

// Tickets broken down from a spec cite it, by its path, a partial path or its bare name: the spec
// is left out of their footprints, as the check command's files are, so they run side by side.
// Listed in a ticket's files metadata, it counts.
func TestTicketsCitingASpecRunSideBySide(t *testing.T) {
	t.Parallel()
	for _, cite := range []string{".orchestra/specs/shop.md", "./.orchestra/specs/shop.md", "specs/shop.md", "shop.md"} {
		for _, tracked := range [][]string{trackedWithSpec, nil} {
			if cite != ".orchestra/specs/shop.md" && cite != "./.orchestra/specs/shop.md" && tracked == nil {
				continue // a partial path or a bare name finds its folder only among the repository's files
			}
			a := TicketFootprint(Ticket{Title: "Add the cart",
				Description: "Build the cart, as " + cite + " says under Cart.", AcceptanceCriteria: "Tests for the cart."},
				tracked, "")
			b := TicketFootprint(Ticket{Title: "Add checkout",
				Description: "See " + cite + ", Checkout. Edit internal/tui/run.go."}, tracked, "")
			if !a.Empty() {
				t.Errorf("citing %s (files known: %t): footprint %+v, want nothing named", cite, tracked != nil, a)
			}
			if want := []string{"internal/tui/run.go"}; !slices.Equal(b.Files, want) {
				t.Errorf("citing %s (files known: %t): files %q, want %q", cite, tracked != nil, b.Files, want)
			}
			if what := shared(b, b, nil, nil); what != "internal/tui/run.go" {
				t.Errorf("citing %s (files known: %t): two such tickets share %q, want the file they edit",
					cite, tracked != nil, what)
			}
		}
	}

	for _, tracked := range [][]string{trackedWithSpec, nil} {
		listed := Ticket{Title: "Revise the spec", Description: "Reword .orchestra/specs/shop.md's Cart.",
			Metadata: []byte(`{"files": [".orchestra/specs/shop.md"]}`)}
		fp := TicketFootprint(listed, tracked, "")
		if want := []string{".orchestra/specs/shop.md"}; !slices.Equal(fp.Files, want) {
			t.Errorf("listing the spec (files known: %t): files %q, want %q", tracked != nil, fp.Files, want)
		}
		if what := shared(fp, fp, nil, nil); what != ".orchestra/specs/shop.md" {
			t.Errorf("listing the spec (files known: %t): two such tickets share %q", tracked != nil, what)
		}
	}

	folder := TicketFootprint(Ticket{Title: "Revise the specs", Metadata: []byte(`{"files": [".orchestra/specs/"]}`)},
		trackedWithSpec, "")
	if want := []string{".orchestra/specs/", ".orchestra/specs/shop.md"}; !slices.Equal(folder.Files, want) {
		t.Errorf("listing the specs' folder: files %q, want %q", folder.Files, want)
	}

	predicted := TicketFootprint(Ticket{Title: "Say hello",
		Metadata: []byte(`{"predicted_files": ".orchestra/specs/shop.md,README.md"}`)}, trackedWithSpec, "")
	if want := []string{"README.md"}; !slices.Equal(predicted.Files, want) || !predicted.Predicted {
		t.Errorf("predicted files: %+v, want the predicted %q", predicted, want)
	}

	// Only the specs' folder is left out: a file of the same name elsewhere still counts.
	elsewhere := append(slices.Clone(trackedWithSpec), "docs/shop.md")
	fp := TicketFootprint(Ticket{Title: "Say hello", Description: "Rewrite shop.md."}, elsewhere, "")
	if want := []string{"docs/shop.md"}; !slices.Equal(fp.Files, want) {
		t.Errorf("a bare name meaning the spec and docs/shop.md: files %q, want %q", fp.Files, want)
	}
}

// orchestra plan reads the same footprints: tickets citing one spec get no link for it.
func TestPlanLinksLeaveOutTheSpec(t *testing.T) {
	t.Parallel()
	open := []Ticket{
		planTicket("a", 1, "", "Build the cart, as .orchestra/specs/shop.md says under Cart."),
		planTicket("b", 2, "", "Build checkout, as .orchestra/specs/shop.md says under Checkout."),
	}
	if got := PlanLinks(open, nil, trackedWithSpec, "", sourceOf(nil), nil, nil); len(got) != 0 {
		t.Errorf("links %q, want none", linkList(got))
	}

	open[0].Metadata = []byte(`{"files": [".orchestra/specs/shop.md"]}`)
	open[1].Metadata = open[0].Metadata
	got := linkList(PlanLinks(open, nil, trackedWithSpec, "", sourceOf(nil), nil, nil))
	if want := []string{"a>b:.orchestra/specs/shop.md"}; !slices.Equal(got, want) {
		t.Errorf("both listing the spec: links %q, want %q", got, want)
	}
}

// A project built from a spec names files in folders that don't exist yet. Listed in a ticket's
// files metadata, such a file counts, whether the repository has files, has none yet or they aren't
// known, so two tickets listing it overlap; named only in a ticket's text, it counts in a folder
// that exists. A path out of the repository never counts.
func TestTicketsListingAFileInANewFolderOverlap(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedHere, {}, nil} {
		for _, c := range []struct{ entry, want string }{
			{"web/src/cart.ts", "web/src/cart.ts"}, {"./web/src/cart.ts", "web/src/cart.ts"}, {"Makefile", "Makefile"},
		} {
			meta := []byte(`{"files": ["` + c.entry + `"]}`)
			a := TicketFootprint(Ticket{Title: "Add the cart", Metadata: meta}, tracked, "")
			b := TicketFootprint(Ticket{Title: "Add checkout", Metadata: meta}, tracked, "")
			if !slices.Equal(a.Files, []string{c.want}) || a.Predicted {
				t.Errorf("listing %s (files %v): %+v, want files [%s]", c.entry, tracked, a, c.want)
			}
			if what := shared(a, b, nil, nil); what != c.want {
				t.Errorf("listing %s (files %v): two tickets share %q, want %q", c.entry, tracked, what, c.want)
			}
		}

		for _, out := range []string{"../web/src/cart.ts", "/srv/web/src/cart.ts", "web/../../src/cart.ts"} {
			meta := []byte(`{"files": ["` + out + `"]}`)
			if fp := TicketFootprint(Ticket{Title: "Add the cart", Metadata: meta}, tracked, ""); !fp.Empty() {
				t.Errorf("listing %s (files %v): %+v, want nothing named", out, tracked, fp)
			}
		}
	}

	text := Ticket{Title: "Add the cart", Description: "Write web/src/cart.ts."}
	if fp := TicketFootprint(text, trackedHere, ""); !fp.Empty() {
		t.Errorf("naming a file in a new folder in text: %+v, want nothing named", fp)
	}
	text.Description = "Write internal/dispatch/cart.go."
	if fp := TicketFootprint(text, trackedHere, ""); !slices.Equal(fp.Files, []string{"internal/dispatch/cart.go"}) {
		t.Errorf("naming a new file in a folder that exists in text: files %q", fp.Files)
	}
}
