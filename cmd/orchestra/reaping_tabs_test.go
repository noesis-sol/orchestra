package main

import (
	"context"
	"slices"
	"testing"
)

// closeOrder is a terminal that notes when its tab's processes are asked for and when it is closed.
type closeOrder struct {
	workerTerminal
	calls []string
}

func (c *closeOrder) TabProcesses(_ context.Context, tab string) ([]int, error) {
	c.calls = append(c.calls, "processes "+tab)
	return nil, nil
}

func (c *closeOrder) CloseTab(_ context.Context, tab string) error {
	c.calls = append(c.calls, "close "+tab)
	return nil
}

// The run loop's tabs note what a worker's tab runs before they close it, for what its agent leaves
// running to be stopped after.
func TestReapingTabsAskForTheProcessesBeforeTheClose(t *testing.T) {
	term := &closeOrder{}
	if err := (reapingTabs{term}).CloseTab(runCtx(t), "@1"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"processes @1", "close @1"}; !slices.Equal(term.calls, want) {
		t.Errorf("calls = %q, want %q", term.calls, want)
	}
}
