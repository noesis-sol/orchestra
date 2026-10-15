package tui

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// applyAnswers sets the Workers stage's answers on the choice, as asked, trims the text typed and
// takes the prefix typed, the current one when cleared.
func TestApplyAnswers(t *testing.T) {
	for _, tc := range []struct {
		about      string
		ask        project.Questions
		answers    workersAnswers
		concurrent int      // the choice's Concurrent after, from 2
		mcp        []string // the choice's MCP after; nil for none set
		err        bool
	}{
		{"nothing asked", project.Questions{}, workersAnswers{option: 4, servers: []string{"exa"}}, 2, nil, false},
		{"an option", project.Questions{Concurrent: true}, workersAnswers{option: 4}, 4, nil, false},
		{"a number typed", project.Questions{Concurrent: true},
			workersAnswers{option: customConcurrency, custom: " 12 "}, 12, nil, false},
		{"a number out of range", project.Questions{Concurrent: true},
			workersAnswers{option: customConcurrency, custom: "0"}, customConcurrency, nil, true},
		{"servers chosen", project.Questions{MCP: true},
			workersAnswers{mcp: true, servers: []string{"exa", "pg"}}, 2, []string{"exa", "pg"}, false},
		{"no servers offered", project.Questions{MCP: true}, workersAnswers{servers: []string{"exa"}}, 2, nil, false},
	} {
		t.Run(tc.about, func(t *testing.T) {
			c := project.Choice{Concurrent: 2, Unasked: project.Questions{Concurrent: true, MCP: true},
				Setup: "  make deps ", PrefixWas: "app", Prefix: ""}
			a := tc.answers
			err := applyAnswers(&c, Ask{Questions: tc.ask}, &a, nil)
			if (err != nil) != tc.err || c.Concurrent != tc.concurrent {
				t.Errorf("err %v, Concurrent %d; want error %t, Concurrent %d", err, c.Concurrent, tc.err, tc.concurrent)
			}
			if c.Unasked.Concurrent == tc.ask.Concurrent {
				t.Errorf("Unasked.Concurrent %t with Concurrent asked %t", c.Unasked.Concurrent, tc.ask.Concurrent)
			}
			switch {
			case tc.mcp == nil && (c.MCP != nil || !c.Unasked.MCP):
				t.Errorf("MCP %v, Unasked.MCP %t: want none set", c.MCP, c.Unasked.MCP)
			case tc.mcp != nil && (c.MCP == nil || !slices.Equal(*c.MCP, tc.mcp) || c.Unasked.MCP):
				t.Errorf("MCP %v, Unasked.MCP %t: want %q", c.MCP, c.Unasked.MCP, tc.mcp)
			}
			if c.MCP != nil && len(*c.MCP) > 0 && &(*c.MCP)[0] == &a.servers[0] {
				t.Error("the choice shares the form's slice of servers")
			}
			if c.Setup != "make deps" || c.Prefix != "" {
				t.Errorf("setup %q, prefix %q: want it trimmed, the prefix untouched when not asked", c.Setup, c.Prefix)
			}
		})
	}
}

// Asked, the prefix cleared is the current one, and one typed is taken as NormalPrefix gives it.
func TestApplyAnswersTakesThePrefixTyped(t *testing.T) {
	for typed, want := range map[string]string{"": "app", "web": "web", " web ": project.NormalPrefix(" web ")} {
		c := project.Choice{PrefixWas: "app", Prefix: typed}
		if err := applyAnswers(&c, Ask{Questions: project.Questions{Prefix: true}}, &workersAnswers{}, nil); err != nil ||
			c.Prefix != want {
			t.Errorf("typed %q: prefix %q (%v), want %q", typed, c.Prefix, err, want)
		}
	}
}
