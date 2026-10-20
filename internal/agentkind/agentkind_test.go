package agentkind

import "testing"

// A kind is its row of the table: "" the default kind's, Claude Code's, and a kind orchestra doesn't
// know one of its name that is given nothing; as a terminal names an agent, "" is no kind.
func TestOfReadsTheKindsRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		want Kind
	}{
		{"", Claude},
		{"claude", Claude},
		{"codex", Codex},
		{"aider", Kind{Name: "aider"}},
	} {
		if got := Of(tc.name); got != tc.want {
			t.Errorf("Of(%q) = %+v, want %+v", tc.name, got, tc.want)
		}
	}
	if got := Named(""); got != (Kind{}) {
		t.Errorf("Named(\"\") = %+v, want no kind", got)
	}
	if got := Named("claude"); got != Claude {
		t.Errorf("Named(\"claude\") = %+v, want Claude", got)
	}
	if Default != Claude {
		t.Errorf("Default = %+v, want Claude", Default)
	}
}

// Every row has a name of its own, for Of to find it by.
func TestEachKindHasANameOfItsOwn(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range kinds {
		if k.Name == "" || seen[k.Name] {
			t.Errorf("kind %+v: its name is empty or another kind's", k)
		}
		seen[k.Name] = true
	}
}
