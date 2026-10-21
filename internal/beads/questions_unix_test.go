//go:build unix

package beads

import (
	"context"
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/tracker"
)

// Questions lists the open issues labelled human, with their metadata, where the options are.
func TestQuestionsListsTheOpenQuestionsWithTheirOptions(t *testing.T) {
	record := createBd(t, `{"schema_version":1,"data":[`+
		`{"id":"k-7","title":"Which encoding?","status":"open","labels":["human"],`+
		`"metadata":{"options":[{"label":"JSON"},{"label":"YAML"}],"recommended":0}},`+
		`{"id":"k-8","title":"Answered","status":"closed","labels":["human"]}]}`)
	qs, err := Tracker{Repo: t.TempDir()}.Questions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(qs) != 1 || qs[0].ID != "k-7" || qs[0].MetadataField("recommended") == nil {
		t.Errorf("questions %+v", qs)
	}
	if b, _ := os.ReadFile(record); string(b) != "list\n--json\n--limit\n0\n--label\nhuman\n" {
		t.Errorf("bd ran with\n%s", b)
	}
}

// Dependents lists what depends on a ticket, each with how it links to it and its notes.
func TestDependentsListsWhatWaitsOnATicket(t *testing.T) {
	record := createBd(t, `{"schema_version":1,"data":[`+
		`{"id":"k-1","title":"Encode","status":"open","notes":"Waiting on k-7","dependency_type":"blocks"},`+
		`{"id":"k-2","title":"Later","status":"open","dependency_type":"discovered-from"}]}`)
	ds, err := Tracker{Repo: t.TempDir()}.Dependents(context.Background(), "k-7")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 || ds[0].DependencyType != "blocks" || ds[0].Notes != "Waiting on k-7" ||
		ds[1].DependencyType != "discovered-from" || ds[1].Status != tracker.StatusOpen {
		t.Errorf("dependents %+v", ds)
	}
	if b, _ := os.ReadFile(record); string(b) != "dep\nlist\nk-7\n--direction\nup\n--json\n" {
		t.Errorf("bd ran with\n%s", b)
	}
	createBd(t, `Error: database is locked`)
	if _, err := (Tracker{Repo: t.TempDir()}).Dependents(context.Background(), "k-7"); err == nil {
		t.Error("output that isn't JSON should be an error")
	}
}
