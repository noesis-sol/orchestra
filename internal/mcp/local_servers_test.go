package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// localServers gives each root's local-scope servers in the roots' order, those of a root two keys
// name in the keys' sorted order, and nothing for a root ~/.claude.json has no record of.
func TestLocalServersFollowTheRoots(t *testing.T) {
	user := userFile{Projects: map[string]json.RawMessage{
		"/main":      json.RawMessage(`{"mcpServers": {"pg": {"command": "main-pg"}}}`),
		"/worktree":  json.RawMessage(`{"mcpServers": {"pg": {"command": "wt-pg"}}}`),
		"/worktree/": json.RawMessage(`{"mcpServers": {"pg": {"command": "wt-slash-pg"}}}`),
		"/other":     json.RawMessage(`{"mcpServers": {"pg": {"command": "other-pg"}}}`),
		"/empty":     json.RawMessage(`{"allowedTools": []}`),
	}}
	for _, tc := range []struct {
		roots []string
		want  []string // each definition's command, in order
	}{
		{nil, nil},
		{[]string{"/nowhere"}, nil},
		{[]string{"/main"}, []string{"main-pg"}},
		{[]string{"/worktree", "/main"}, []string{"wt-pg", "wt-slash-pg", "main-pg"}},
		{[]string{"/main", "/worktree"}, []string{"main-pg", "wt-pg", "wt-slash-pg"}},
		{[]string{"/empty"}, []string{""}},
	} {
		local, err := localServers(user, "/home/.claude.json", tc.roots)
		if err != nil {
			t.Fatalf("%v: %v", tc.roots, err)
		}
		var got []string
		for _, defs := range local {
			var d struct{ Command string }
			_ = json.Unmarshal(defs["pg"], &d) // a record without pg has no command
			got = append(got, d.Command)
		}
		if strings.Join(got, ",") != strings.Join(tc.want, ",") || len(got) != len(tc.want) {
			t.Errorf("%v: %q, want %q", tc.roots, got, tc.want)
		}
	}
}

// A repository's record that isn't an object with mcpServers fails, naming the file and the key.
func TestLocalServersNameABadRecord(t *testing.T) {
	user := userFile{Projects: map[string]json.RawMessage{"/main": json.RawMessage(`{"mcpServers": []}`)}}
	_, err := localServers(user, "/home/.claude.json", []string{"/main"})
	if err == nil || !strings.HasPrefix(err.Error(), `/home/.claude.json: projects["/main"]: `) {
		t.Errorf("err = %v, want one naming the file and the key", err)
	}
}
