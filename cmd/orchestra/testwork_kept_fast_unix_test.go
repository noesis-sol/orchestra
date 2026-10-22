//go:build unix

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// With a check-fast.sh init kept, the ticket that sets up tests from scratch doesn't say it checks nothing.
func TestInitScratchTicketSaysTheCheckFastKeptChecks(t *testing.T) {
	t.Parallel()
	dir := trackerBd(t, false)
	repo, _ := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, project.FastRunner), []byte("#!/bin/sh\nmake test\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	none := []project.Suite{}
	c := project.Choice{Tests: project.TestsScratch, Fast: &none, Full: &none}
	if _, err := project.ApplyRunners(context.Background(), repo, c); err != nil {
		t.Fatal(err)
	}
	fileTestWork(testCtx(context.Background(), t), repo, c)
	setUp := created(t, dir, "t-2")
	if !strings.Contains(setUp, project.FastRunner+" and "+project.FullRunner+" run the checks the project had") ||
		strings.Contains(setUp, "checking nothing") || strings.Contains(setUp, "nothing merged is checked") {
		t.Errorf("t-2 doesn't read the runners as written:\n%s", setUp)
	}
}
