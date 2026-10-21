package main

import (
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// orchestra writes a long flag with two dashes, --limit, and a one-letter shorthand with one, -c (orchestra-6rdu).
// Go's flag package takes either, and its PrintDefaults lists every flag with one dash, which had the help, the
// messages and the docs writing the older flags one way and the newer ones the other.

// helpArgs are the commands whose -h lists flags: the run's and each subcommand's.
var helpArgs = [][]string{{"-h"}, {"init", "-h"}, {"plan", "-h"}, {"requires", "-h"}, {"update", "-h"}}

// listedFlag is a line of a help's flag listing: its dashes and the flag's name.
var listedFlag = regexp.MustCompile(`(?m)^  (-+)([\w-]+)`)

// Each help lists its long flags as --name and its shorthands as -c, -v.
func TestHelpListsLongFlagsWithTwoDashes(t *testing.T) {
	t.Parallel()
	wants := map[string][]string{
		"-h":       {"  --limit int\n", "  -c int\n    \tshorthand for --concurrent\n", "  -v\tshorthand for --version\n"},
		"init":     {"  --check-fast string\n", "  -c int\n"},
		"plan":     {"  --apply\n    \tadd the proposed links with bd dep add\n"},
		"requires": {"  --no-install\n"},
		"update":   {"  --check\n", "  --force\n"},
	}
	for _, args := range helpArgs {
		_, help, err := runIn(t, t.TempDir(), nil, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		listed := listedFlag.FindAllStringSubmatch(help, -1)
		if len(listed) == 0 {
			t.Errorf("%v lists no flags:\n%s", args, help)
		}
		for _, m := range listed {
			if want := flagName(m[2]); m[1]+m[2] != want {
				t.Errorf("%v lists %s%s, not %s", args, m[1], m[2], want)
			}
		}
		for _, want := range wants[args[0]] {
			if !strings.Contains(help, want) {
				t.Errorf("%v lacks %q:\n%s", args, want, help)
			}
		}
	}
}

// The listing is PrintDefaults' but for the dashes, and but for a flag whose usage gives its default already
// ("(default: .orchestra/settings.json, else on)"), which isn't followed by a second.
func TestFlagListingKeepsPrintDefaultsLayout(t *testing.T) {
	t.Parallel()
	fs := flag.NewFlagSet("orchestra", flag.ContinueOnError)
	defineRunFlags(fs, func(string) string { return "" })
	var ours, gos strings.Builder
	fs.SetOutput(&ours)
	printFlags(fs)
	fs.SetOutput(&gos)
	fs.PrintDefaults()
	want := regexp.MustCompile(`(?m)^  -(\w[\w-]+)`).ReplaceAllString(gos.String(), "  --$1")
	want = regexp.MustCompile(`(?m)^(    \t.*\(default: .*) \(default [^)]*\)$`).ReplaceAllString(want, "$1")
	if ours.String() != want {
		t.Errorf("listing:\n%s\nwant:\n%s", ours.String(), want)
	}
	if strings.Contains(ours.String(), "else on) (default true)") {
		t.Errorf("--resolve-conflicts gives its default twice:\n%s", ours.String())
	}

	small := flag.NewFlagSet("small", flag.ContinueOnError)
	small.String("agent", "claude", "agent `kind`")
	small.Int("c", 0, "shorthand for --concurrent")
	small.Int("limit", 40, "stop after this many tickets")
	small.Bool("notify", true, "notifications")
	small.Bool("resolve", true, "resolve conflicts (default: settings.json, else on)")
	small.Duration("timeout", 0, "time limit")
	small.Bool("v", false, "shorthand for --version")
	small.Bool("yes", false, "gone")
	var b strings.Builder
	small.SetOutput(&b)
	printFlags(small, "yes")
	if want := "  --agent kind\n    \tagent kind (default \"claude\")\n" +
		"  -c int\n    \tshorthand for --concurrent\n" +
		"  --limit int\n    \tstop after this many tickets (default 40)\n" +
		"  --notify\n    \tnotifications (default true)\n" +
		"  --resolve\n    \tresolve conflicts (default: settings.json, else on)\n" +
		"  --timeout duration\n    \ttime limit\n" +
		"  -v\tshorthand for --version\n"; b.String() != want {
		t.Errorf("listing:\n%s\nwant:\n%s", b.String(), want)
	}
}

// The help texts the ticket found stale say what is true now, --limit's default once.
func TestHelpOfLimitTicketsAndOrganEffort(t *testing.T) {
	t.Parallel()
	_, help, err := runIn(t, t.TempDir(), nil, "-h")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  --limit int\n    \tstop after this many tickets in total (default: none) [LIMIT]\n",
		"whether to run a spec or talk a new feature through instead [ORCHESTRA_TICKETS=1]\n",
		"else low for triage and the predictor, medium for the reviewer and init's scout) [ORGAN_EFFORT]\n",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("-h lacks %q:\n%s", want, help)
		}
	}
	for _, stale := range []string{"screening", "planning", "plan a new feature", "9223372036854775807"} {
		if strings.Contains(help, stale) {
			t.Errorf("-h still says %q:\n%s", stale, help)
		}
	}
}

// A long flag still takes one dash, as Go's flags do: scripts written either way keep working.
func TestLongFlagTakesOneDashToo(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"-limit", "5", "-plain"}, {"--limit", "5", "--plain"}} {
		f, problems, err := readFlags(args, func(string) string { return "" }, &strings.Builder{})
		if err != nil || len(problems) > 0 || f.Limit != 5 || !f.Plain {
			t.Errorf("%v: limit %d, plain %t, %v %q", args, f.Limit, f.Plain, err, problems)
		}
	}
}

// No help, message, comment or doc writes one of orchestra's long flags with one dash: not the help, the Go
// source's strings and comments, the docs, the skills, the prompts, nor the changelog's unreleased section
// (released ones stay as they were).
func TestNoTextWritesALongFlagWithOneDash(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra for each help text and reads the docs")
	}
	top, err := filepath.Abs("../..") // before runIn moves into its folder
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	var helps strings.Builder
	for _, args := range helpArgs {
		_, help, err := runIn(t, t.TempDir(), nil, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		helps.WriteString(help)
		for _, m := range listedFlag.FindAllStringSubmatch(help, -1) {
			if len(m[2]) > 1 && !slices.Contains(names, m[2]) {
				names = append(names, m[2])
			}
		}
	}
	// longest first, so -check-fast isn't read as -check
	slices.SortFunc(names, func(a, b string) int { return len(b) - len(a) })
	oneDash := regexp.MustCompile(`(?:^|[^\w\-/.>])(-(?:` + strings.Join(names, "|") + `))(?:[^\w-]|$)`)
	found := func(where, text string) {
		for _, m := range oneDash.FindAllStringSubmatch(text, -1) {
			t.Errorf("%s writes %s, not -%s", where, m[1], m[1])
		}
	}
	found("the help", helps.String())

	repo := os.DirFS(top)
	for _, root := range []string{"README.md", "CLAUDE.md", ".orchestra/worker-prompt.md", "docs", "skills",
		"internal", "cmd"} {
		err := fs.WalkDir(repo, root, func(p string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case d.IsDir() && d.Name() == "testdata":
				return fs.SkipDir
			case d.IsDir(), strings.HasSuffix(p, "_test.go"):
				return nil
			}
			b, err := fs.ReadFile(repo, p)
			if err != nil {
				return err
			}
			switch path.Ext(p) {
			case ".md":
				found(p, string(b))
			case ".go":
				for _, text := range goTexts(t, p, b) {
					found(p, text)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	changelog, err := fs.ReadFile(repo, "CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	unreleased := string(changelog)
	if released := regexp.MustCompile(`\n## \[\d`).FindStringIndex(unreleased); released != nil {
		unreleased = unreleased[:released[0]]
	}
	found("CHANGELOG.md's unreleased section", unreleased)
}

// goTexts are the comments of the Go source src, and its string literals that hold text: a literal of one
// word is a value, an argument or a suffix such as <repo>'s "-worktrees", not text naming a flag.
func goTexts(t *testing.T, name string, src []byte) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, g := range file.Comments {
		texts = append(texts, g.Text())
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && strings.ContainsAny(s, " \n") {
				texts = append(texts, s)
			}
		}
		return true
	})
	return texts
}
