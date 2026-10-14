package project

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// The export offPath says to add puts bd's directory on the PATH as it is, with a space or a $ in it.
func TestOffPathQuotesADirectoryWithASpace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my tools", "$bin")
	advice := offPath(filepath.Join(dir, "bd"))
	_, export, ok := strings.Cut(advice, "(")
	export, _, _ = strings.Cut(export, ") and open")
	if !ok || !strings.HasPrefix(export, "export PATH=") {
		t.Fatalf("no export in %q", advice)
	}
	out, err := command.Output(context.Background(), command.ReadLimit, "", "sh", "-c",
		"PATH=/usr/bin:/bin; "+export+`; printf '%s' "$PATH"`)
	if want := "/usr/bin:/bin:" + dir; err != nil || out != want {
		t.Errorf("%s gives PATH %q (%v), want %q", export, out, err, want)
	}
}
