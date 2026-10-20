package hookterm

import (
	"github.com/noesis-sol/orchestra/internal/agentio"
	"github.com/noesis-sol/orchestra/internal/agentkind"
	"github.com/noesis-sol/orchestra/internal/claude"
)

// AgentUI is what a hook-driven terminal knows of an agent kind's sessions: where the hooks it
// launches one with write their records, and what those records, with its screen, tell of it.
type AgentUI interface {
	// Records returns the folder the hooks args load (among a session's other arguments) write their
	// records to, and false when args load none.
	Records(args []string) (string, bool)
	// Read reads what the hooks writing to the folder records tell of their session (records "": the
	// folder of a session launched without hooks in dir): its state as they tell it, and settle, its
	// state given screen, which reads its visible screen only where the hooks can't tell.
	Read(records, dir string) (told agentio.State, settle func(screen func() (string, error)) agentio.State)
}

// agentUIs are the agent kinds whose sessions report through hooks, by the kind's name in
// internal/agentkind's table, as a terminal launches it (dispatch.Config.AgentKind): adding one is its
// package and a row here.
var agentUIs = map[string]AgentUI{
	agentkind.Claude.Name: claude.UI{},
}

// UI is the AgentUI of the agent kind; a kind without one, or none (""), launches with no records, and
// its state is unknown.
func UI(kind string) AgentUI {
	if ui, ok := agentUIs[agentkind.Named(kind).Name]; ok {
		return ui
	}
	return noUI{}
}

// Reports reports whether the agent kind's sessions report through hooks, which a hook-driven terminal
// needs to tell what they are doing.
func Reports(kind string) bool {
	_, ok := agentUIs[agentkind.Named(kind).Name]
	return ok
}

// noUI is the AgentUI of a kind whose sessions report through no hooks.
type noUI struct{}

func (noUI) Records([]string) (string, bool) { return "", false }

func (noUI) Read(string, string) (agentio.State, func(func() (string, error)) agentio.State) {
	return agentio.StateUnknown, func(func() (string, error)) agentio.State { return agentio.StateUnknown }
}
