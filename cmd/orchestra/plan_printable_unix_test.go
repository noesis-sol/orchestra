//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra plan prints a link's reason, a files entry kept as written, and bd's error text without
// the escape sequences in them: a ticket's author can't set the terminal's title or clipboard.
func TestPlanPrintsTicketTextPrintable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// the files entry holds a window title's sequence, in JSON's escapes
	files := `{"files":["internal/a\u001b]0;pwned\u0007.go"]}`
	list := `[{"id":"k-2","title":"Retry","status":"open","priority":3,"metadata":` + files + `},` +
		`{"id":"k-1","title":"Log","status":"open","priority":1,"metadata":` + files + `}]`
	listFile := filepath.Join(dir, "list.json")
	if err := os.WriteFile(listFile, []byte(list+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	faketool.Write(t, dir, "bd", "#!/bin/sh\ncase \"$1\" in\nlist) cat '"+listFile+"' ;;\n"+
		"dep) printf 'refused \\033]52;c;Y2xpcA==\\007 here\\n' >&2; exit 1 ;;\n*) exit 1 ;;\nesac\n")
	onPath(t, dir)
	repo, _ := gitRepo(t)

	stdout, stderr, err := runIn(t, repo, nil, "plan", "--apply")
	if err == nil {
		t.Fatalf("plan --apply with a failing bd dep add succeeded:\n%s", stdout)
	}
	for name, out := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if strings.ContainsAny(out, "\x1b\x07") {
			t.Errorf("plan's %s keeps an escape sequence: %q", name, out)
		}
	}
	if want := "k-2 (P3) waits for k-1 (P1): both touch internal/a.go"; !strings.Contains(stdout, want) {
		t.Errorf("plan output lacks %q:\n%s", want, stdout)
	}
	if !strings.Contains(stderr, "orchestra plan: bd dep add k-2 k-1:") || !strings.Contains(stderr, "refused") {
		t.Errorf("plan's stderr doesn't give bd's error:\n%s", stderr)
	}
}
