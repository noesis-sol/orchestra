package dispatch

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// A predicted path holding a space or a comma is cached on the ticket as a JSON list and read back
// whole: the ticket's footprint names the file, in this run and the next, not the words its path
// splits into (docs/User, a new file in an existing folder).
func TestPredictedPathWithASpaceReadsBackWhole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.commitFiles("docs/User Guide.md", "docs/notes, draft.md", "docs/other.md")
	h.beads.add("B", "Say hello", 1) // names nothing
	bin, _ := fakePredictor(t, `echo '{"structured_output":{"files":["docs/User Guide.md","docs/notes, draft.md"]}}'`)
	o := h.loop()
	o.organ = organ.Client{Bin: bin}
	b, err := h.beads.Show(t.Context(), "B")
	if err != nil {
		t.Fatal(err)
	}
	o.predict(t.Context(), b)

	want := []string{"docs/User Guide.md", "docs/notes, draft.md"}
	var cached []string
	if raw := h.beads.metadata("B", PredictedKey); json.Unmarshal([]byte(raw), &cached) != nil || !slices.Equal(cached, want) {
		t.Errorf("cached %q, want the JSON list of %q", raw, want)
	}
	if b, err = h.beads.Show(t.Context(), "B"); err != nil { // as bd hands it back, with the prediction
		t.Fatal(err)
	}
	if got := metadataList(b.Metadata, PredictedKey); !slices.Equal(got, want) {
		t.Errorf("read back %q, want %q", got, want)
	}
	o.readFiles(t.Context())
	if fp := o.footprintOf(b); !slices.Equal(fp.Files, want) || !fp.Predicted {
		t.Errorf("footprint in this run: %+v", fp)
	}
	tracked := o.checkout.TrackedFiles(t.Context(), h.repo)
	if fp := TicketFootprint(b, tracked, ""); !slices.Equal(fp.Files, want) || !fp.Predicted {
		t.Errorf("footprint in the next run: %+v", fp)
	}
}

// A prediction cached before it was a JSON list, joined with commas, still reads as it did.
func TestPredictionCachedWithCommasStillReads(t *testing.T) {
	meta := []byte(`{"predicted_files": "internal/dispatch/run.go,internal/tui/run.go"}`)
	if got := metadataList(meta, PredictedKey); !slices.Equal(got, []string{"internal/dispatch/run.go", "internal/tui/run.go"}) {
		t.Errorf("read back %q", got)
	}
	fp := TicketFootprint(Ticket{Title: "Say hello", Metadata: meta}, trackedHere, "")
	if !slices.Equal(fp.Files, []string{"internal/dispatch/run.go", "internal/tui/run.go"}) || !fp.Predicted {
		t.Errorf("footprint: %+v", fp)
	}
}
