package project

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteFeatureRequest writes the description, every line of it, in place of an earlier
// interview's, and returns its path in the checkout.
func TestWriteFeatureRequestReplacesTheEarlierOne(t *testing.T) {
	repo := t.TempDir()
	for _, request := range []string{"An earlier feature, longer than the next one.", "- Add a --json flag\n- to the list command"} {
		rel, err := WriteFeatureRequest(repo, request)
		if err != nil {
			t.Fatal(err)
		}
		if rel != RunPath("feature-request.md") {
			t.Errorf("the description is at %s", rel)
		}
		b, err := os.ReadFile(filepath.Join(repo, rel))
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != request+"\n" {
			t.Errorf("the file holds %q, want %q", b, request+"\n")
		}
	}
}
