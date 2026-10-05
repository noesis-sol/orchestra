package dispatch

import (
	"slices"
	"testing"
)

// A files metadata entry that can't be a path in the repository (empty, its root, absolute or
// outside it) is dropped, whether the repository's files are known or not: the ticket names
// nothing by it, overlaps no other ticket listing it, and falls back on its predicted files.
func TestTicketFootprintDropsNamesOutsideTheRepository(t *testing.T) {
	for _, entry := range []string{`""`, `"./"`, `"."`, `"./."`, `"a/.."`, `"/etc/passwd"`, `"../x.go"`,
		`"internal/../../x.go"`, `".//x.go"`} {
		for _, tracked := range [][]string{trackedHere, nil} {
			meta := `{"files": [` + entry + `]}`
			fp := TicketFootprint(Ticket{Title: "Say hello", Metadata: []byte(meta)}, tracked, "")
			if !fp.Empty() {
				t.Errorf("%s (files known: %t): %+v, want nothing named", meta, tracked != nil, fp)
			}
			if what := shared(fp, fp, nil); what != "" {
				t.Errorf("%s (files known: %t): two such tickets share %q", meta, tracked != nil, what)
			}

			meta = `{"files": [` + entry + `, "internal/tui/run.go"]}`
			fp = TicketFootprint(Ticket{Title: "Say hello", Metadata: []byte(meta)}, tracked, "")
			if want := []string{"internal/tui/run.go"}; !slices.Equal(fp.Files, want) {
				t.Errorf("%s (files known: %t): files %q, want %q", meta, tracked != nil, fp.Files, want)
			}

			meta = `{"files": [` + entry + `], "predicted_files": [` + entry + `, "internal/dispatch/run.go"]}`
			fp = TicketFootprint(Ticket{Title: "Say hello", Metadata: []byte(meta)}, tracked, "")
			if want := []string{"internal/dispatch/run.go"}; !slices.Equal(fp.Files, want) || !fp.Predicted {
				t.Errorf("%s (files known: %t): %+v, want the predicted %q", meta, tracked != nil, fp, want)
			}
		}
	}
}

// A path out of the repository named in a ticket's text is no file of it, even when the
// repository's files aren't known.
func TestTicketFootprintDropsTextPathsOutsideTheRepository(t *testing.T) {
	for _, tracked := range [][]string{trackedHere, nil} {
		tk := Ticket{Title: "Say hello", Description: "Read ../x.go and internal/../../y.go, then .//z.go."}
		if fp := TicketFootprint(tk, tracked, ""); !fp.Empty() {
			t.Errorf("files known: %t: %+v, want nothing named", tracked != nil, fp)
		}
	}
}
