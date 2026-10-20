package dispatch_test

import (
	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/cmux"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/tmux"
)

// The terminals and the reporter keep to the interfaces the loop reaches them through. The adapters
// import internal/agentio, not the loop, so the loop's tests say it.
var (
	_ dispatch.Tabs    = herdr.Terminal{}
	_ dispatch.Starter = herdr.Terminal{}
	_ dispatch.Namer   = herdr.Terminal{}
	_ dispatch.Agents  = herdr.Terminal{}

	_ dispatch.Tabs    = tmux.Terminal{}
	_ dispatch.Starter = tmux.Terminal{}
	_ dispatch.Namer   = tmux.Terminal{}
	_ dispatch.Agents  = tmux.Terminal{}

	_ dispatch.Tabs        = cmux.Terminal{}
	_ dispatch.Starter     = cmux.Terminal{}
	_ dispatch.Namer       = cmux.Terminal{}
	_ dispatch.Agents      = cmux.Terminal{}
	_ dispatch.PromptTyper = cmux.Terminal{}

	_ dispatch.Reporter    = claude.Reporter{}
	_ dispatch.EnvReporter = claude.Reporter{}
)
