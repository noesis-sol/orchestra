package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FiledEpic reads a feature's epic from feature.json, and says what is wrong with one that names none.
func TestFiledEpicReadsAFeaturesEpic(t *testing.T) {
	for _, tc := range []struct {
		file, epic, err string
	}{
		{`{"epic":"f-1"}` + "\n", "f-1", ""},
		{`{"epic":" f-2 "}`, "f-2", ""},
		{`{"epic":""}`, "", "names no epic"},
		{`{}`, "", "names no epic"},
		{`f-1`, "", "invalid character"},
	} {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Join(repo, Dir, RunName), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, RunPath(FeatureName)), []byte(tc.file), 0o644); err != nil {
			t.Fatal(err)
		}
		epic, err := FiledEpic(repo, RefineFeature)
		if epic != tc.epic || (tc.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%q: got %q, %v", tc.file, epic, err)
		}
	}
}
