package dispatch

import (
	"fmt"
	"path"
	"strings"
	"testing"
	"time"
)

// A glob with several ** in the files metadata, which a model or a person may write, matches the
// repository's files in time linear in them: **/**/**/**/**/* on 200 files 13 folders deep took
// seconds when each ** tried every split of the parts after it.
func TestAGlobOfManyAnyFoldersMatchesInLinearTime(t *testing.T) {
	t.Parallel()
	var tracked []string
	for i := range 200 {
		var dirs []string
		for d := range 13 {
			dirs = append(dirs, fmt.Sprintf("d%d%c", d, 'a'+i%3))
		}
		tracked = append(tracked, path.Join(append(dirs, fmt.Sprintf("f%d.go", i))...))
	}
	repo := newRepoFiles(tracked, "")
	for _, c := range []struct {
		pattern string
		want    int
	}{
		{"**/**/**/**/**/*", 200},
		{"**/**/**/**/**/**/**/*.go", 200},
		{"d0a/**/**/**/**/**/**/**/f*.go", 67},
		{"**/d5b/**/**/**/**/**/**/**/*", 67},
		{"**/**/**/**/**/**/**/**/**/**/*.ts", 0}, // minutes, trying every split
	} {
		done := make(chan []string, 1)
		go func() {
			files, _ := repo.glob(c.pattern)
			done <- files
		}()
		select {
		case files := <-done:
			if len(files) != c.want {
				t.Errorf("%s matches %d files, want %d", c.pattern, len(files), c.want)
			}
		case <-time.After(10 * time.Second): // linear, it takes milliseconds under -race
			t.Fatalf("%s took over 10s to match %d files", c.pattern, len(tracked))
		}
	}
}

// globMatch takes consecutive ** as one, and ** as matching no folders as well as several.
func TestGlobMatchTakesAnyFolders(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"**", "", true},
		{"**/**", "a", true},
		{"**/**/x", "x", true},
		{"a/**/**/x", "a/x", true},
		{"a/**/**/x", "a/b/c/x", true},
		{"a/**/b/**/x", "a/b/x", true},
		{"a/**/b/**/x", "a/c/x", false},
		{"a/**/*.go", "a/b/c.ts", false},
		{"*/**", "", false},
		{"x", "", false},
	} {
		var parts []string
		if c.name != "" {
			parts = strings.Split(c.name, "/")
		}
		if got := globMatch(strings.Split(c.pattern, "/"), parts); got != c.want {
			t.Errorf("globMatch(%s, %q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}
