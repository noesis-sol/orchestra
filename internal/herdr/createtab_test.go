package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tabsHerdr puts a herdr on PATH that keeps its tabs, one "<id> <label>" line each, in the file
// tabs in the directory it returns, starting with those given. 'tab list' lists them, 'tab close'
// closes one (tab_not_found if it has none by that ID), and 'tab create' opens one, w1:t<n> from
// w1:t10 on, and answers with the file answer, @ID@ in it replaced by the new tab's ID. With a file
// nocreate it opens none; a file listfail fails the next 'tab list' and goes, and a file listbroken
// fails every one. It logs each call's arguments to the file calls.
func tabsHerdr(t *testing.T, tabs ...string) string {
	t.Helper()
	dir := t.TempDir()
	var lines strings.Builder
	for _, tab := range tabs {
		lines.WriteString(tab + "\n")
	}
	writeFile(t, dir, "tabs", lines.String())
	writeFile(t, dir, "next", "10\n")
	herdrScript(t, `#!/bin/sh
d='`+dir+`'
echo "$*" >> "$d/calls"
case "$1 $2" in
"tab list")
	if [ -e "$d/listbroken" ] || [ -e "$d/listfail" ]; then
		rm -f "$d/listfail"
		echo '{"error":{"code":"server_busy","message":"try again"}}' >&2
		exit 1
	fi
	printf '{"id":"cli:tab:list","result":{"tabs":['
	sep=
	while read -r id label; do
		printf '%s{"agent_status":"idle","label":"%s","tab_id":"%s","workspace_id":"w1"}' "$sep" "$label" "$id"
		sep=,
	done < "$d/tabs"
	printf '],"type":"tab_list"}}\n'
	;;
"tab create")
	while [ $# -gt 0 ]; do
		[ "$1" = --label ] && label=$2
		shift
	done
	n=$(cat "$d/next")
	echo $((n + 1)) > "$d/next"
	id=w1:t$n
	[ -e "$d/nocreate" ] || echo "$id $label" >> "$d/tabs"
	sed "s/@ID@/$id/g" "$d/answer"
	;;
"tab close")
	if ! grep -q "^$3 " "$d/tabs"; then
		echo '{"error":{"code":"tab_not_found","message":"tab not found"},"id":"cli:tab:close"}' >&2
		exit 1
	fi
	grep -v "^$3 " "$d/tabs" > "$d/tabs.new"
	mv "$d/tabs.new" "$d/tabs"
	;;
esac
`)
	return dir
}

// writeFile writes content to the file name in dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// openTabs returns the tabs a tabsHerdr has open, one "<id> <label>" line each.
func openTabs(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "tabs"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// CreateTab lists the tabs with its label, opens the tab, returns it and its pane, and leaves it open.
func TestCreateTabOpensATab(t *testing.T) {
	dir := tabsHerdr(t)
	writeFile(t, dir, "answer", `{"id":"cli:tab:create","result":{"root_pane":{"pane_id":"w1:p1","tab_id":"@ID@"},`+
		`"tab":{"label":"t-1","tab_id":"@ID@"},"type":"tab_created"}}`)
	tab, pane, err := (Terminal{}).CreateTab(context.Background(), "w1", "/Users/m/My Projects/app", "t-1")
	if tab != "w1:t10" || pane != "w1:p1" || err != nil {
		t.Fatalf("created: %q %q %v", tab, pane, err)
	}
	if got := openTabs(t, dir); got != "w1:t10 t-1\n" {
		t.Errorf("open tabs:\n%s", got)
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if want := "tab list --workspace w1\n" +
		"tab create --workspace w1 --cwd /Users/m/My Projects/app --label t-1 --no-focus\n"; string(calls) != want {
		t.Errorf("ran herdr:\n%s\nwant:\n%s", calls, want)
	}
}

// When Herdr's answer to 'tab create' can't be used, the tab it opened is closed again, found by its
// ID if the answer gives it and else by its label, while an earlier tab with that label is left be.
func TestCreateTabClosesATabItCannotUse(t *testing.T) {
	const earlier = "w1:t3 t-1"
	for _, c := range []struct {
		name, answer string
		jsonErr      bool // the error wraps json.Unmarshal's
	}{
		{"cut short", `{"id":"cli:tab:create","result":{"root_pane":{"pa`, true},
		{"empty", ``, true},
		{"no pane ID", `{"result":{"tab":{"tab_id":"@ID@"},"type":"tab_created"}}`, false},
		{"no IDs", `{"result":{"type":"tab_created"}}`, false},
		{"a pane ID that isn't a string", `{"result":{"tab":{"tab_id":"@ID@"},"root_pane":{"pane_id":7}}}`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := tabsHerdr(t, earlier)
			writeFile(t, dir, "answer", c.answer)
			tab, pane, err := (Terminal{}).CreateTab(context.Background(), "w1", "/tmp", "t-1")
			if tab != "" || pane != "" || err == nil {
				t.Fatalf("got %q %q %v", tab, pane, err)
			}
			const closed = "; tab w1:t10, which Herdr opened, is closed again"
			if !strings.HasPrefix(err.Error(), "unexpected 'herdr tab create' output") ||
				!strings.HasSuffix(err.Error(), closed) {
				t.Errorf("error %q, want it to say what the output was and end %q", err, closed)
			}
			var se *json.SyntaxError
			var te *json.UnmarshalTypeError
			if wrapped := errors.As(err, &se) || errors.As(err, &te); wrapped != c.jsonErr {
				t.Errorf("error %q wraps json.Unmarshal's: %v, want %v", err, wrapped, c.jsonErr)
			}
			if got := openTabs(t, dir); got != earlier+"\n" {
				t.Errorf("open tabs:\n%s\nwant only the earlier one", got)
			}
		})
	}
}

// When CreateTab can't tell which tab Herdr opened, it closes none and says which may be left.
func TestCreateTabSaysWhatItCannotClose(t *testing.T) {
	for _, c := range []struct {
		name string
		file string // the file that sets the fake herdr up
		says string // what the error adds about the tab, up to the error that kept it open
		open string // the tabs left open
	}{
		{"no tab opened", "nocreate", "; Herdr has no new tab labelled t-1", "w1:t3 t-1\n"},
		{"no tabs listed before", "listfail", "; tabs w1:t3, w1:t10, labelled t-1, left open: Herdr couldn't list " +
			"the tabs before, to tell the one it opened from earlier ones (herdr tab list", "w1:t3 t-1\nw1:t10 t-1\n"},
		{"no tabs listed", "listbroken", "; a tab labelled t-1 may be left open: Herdr couldn't list the tabs " +
			"(herdr tab list", "w1:t3 t-1\nw1:t10 t-1\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := tabsHerdr(t, "w1:t3 t-1")
			writeFile(t, dir, "answer", "not json")
			writeFile(t, dir, c.file, "")
			_, _, err := (Terminal{}).CreateTab(context.Background(), "w1", "/tmp", "t-1")
			want := "unexpected 'herdr tab create' output (invalid character 'o' in literal null (expecting 'u')): " +
				"not json" + c.says
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Errorf("error:\n%v\nwant it to start:\n%s", err, want)
			}
			if got := openTabs(t, dir); got != c.open {
				t.Errorf("open tabs:\n%s\nwant:\n%s", got, c.open)
			}
		})
	}
}

// A tab CreateTab can't close is left, and the error says why, wrapping only the answer's error.
func TestCreateTabSaysATabCouldNotBeClosed(t *testing.T) {
	dir := tabsHerdr(t)
	// An answer naming a tab Herdr doesn't have, and no pane.
	writeFile(t, dir, "answer", `{"result":{"tab":{"tab_id":"w1:t99"}}}`)
	_, _, err := (Terminal{}).CreateTab(context.Background(), "w1", "/tmp", "t-1")
	if err == nil || !strings.Contains(err.Error(), "; tab w1:t99, which Herdr opened, could not be closed (") {
		t.Errorf("error %v", err)
	}
	if HasCode(err, TabNotFound) {
		t.Errorf("the close's tab_not_found is told, not wrapped: %v", err)
	}
}
