package organ

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ---- Predictor -----------------------------------------------------------------------

// predict picks, for each ready ticket that names no files, the files it will likely change, which
// the orchestrator caches on the ticket and schedules it by.
var predict = spec{name: "predict", limit: 2 * time.Minute, effort: "low", schema: predictSchema,
	system: systemPrompt("predict", fromTheRun)}

const predictSchema = `{"type":"object","properties":{"files":{"type":"array",` +
	`"items":{"type":"string"}}},"required":["files"]}`

// MaxPredicted is the most files a prediction keeps.
const MaxPredicted = 8

// maxListed is the most repository files an organ's input lists: enough for most projects, and a
// few hundred kilobytes at most.
const maxListed = 5000

// Footprint is the evidence for predicting a ticket's files.
type Footprint struct {
	ID     string
	Ticket string   // bd show
	Files  []string // git ls-files
}

// predictInput names the ticket by ID only, like triageInput: its title stays inside the tags, and
// the request comes last, after them.
func predictInput(f Footprint) string {
	listed := f.Files
	more := ""
	if len(listed) > maxListed {
		listed, more = listed[:maxListed], fmt.Sprintf("\n(… and %d more)", len(f.Files)-maxListed)
	}
	id := EvidenceID()
	return Section(id, "Ticket (bd show)", f.Ticket) +
		Section(id, "Repository files (git ls-files)", strings.Join(listed, "\n")+more) +
		"Predict the files ticket " + f.ID + " will change.\n"
}

// parsePrediction keeps the predicted files that are repository files, without repeats, up to
// MaxPredicted.
func parsePrediction(r Result, tracked []string) ([]string, error) {
	var p struct {
		Files []string `json:"files"`
	}
	if err := r.decode(&p); err != nil {
		return nil, fmt.Errorf("unreadable prediction: %w", err)
	}
	known := map[string]bool{}
	for _, f := range tracked {
		known[f] = true
	}
	files := []string{}
	for _, f := range p.Files {
		f = strings.TrimPrefix(strings.TrimSpace(f), "./")
		if known[f] && !slices.Contains(files, f) && len(files) < MaxPredicted {
			files = append(files, f)
		}
	}
	return files, nil
}

// PredictFiles predicts the repository files a ticket naming none will change; empty when the
// ticket gives no clue.
func (g Client) PredictFiles(ctx context.Context, f Footprint) ([]string, error) {
	return askAs(ctx, g, predict, predictInput(f), func(r Result) ([]string, error) {
		return parsePrediction(r, f.Files)
	})
}
