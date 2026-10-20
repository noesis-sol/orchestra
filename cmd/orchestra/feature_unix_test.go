//go:build unix

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/faketool"
)

// featureTools puts on PATH a claude that records each call and ends, a bd that knows each ID it is
// shown as an open epic and has nothing else, and a herdr that fails. It returns the folder where
// claude keeps the number of its calls (claude-n) and each one's arguments and input (args.N, in.N),
// and bd its calls, one per line with each argument followed by |.
func featureTools(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	faketool.Write(t, dir, "claude", `#!/bin/sh
d='`+dir+`'
n=$(cat "$d/claude-n" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$d/claude-n"
printf '[%s]\n' "$@" > "$d/args.$n"
cat > "$d/in.$n"
echo 'fake claude: bye'
`)
	faketool.Write(t, dir, "bd", `#!/bin/sh
d='`+dir+`'
call=; for a in "$@"; do call=$call$a'|'; done; printf '%s\n' "$call" >> "$d/bd-calls"
case "$1" in
show) echo "[{\"id\":\"$2\",\"status\":\"open\",\"issue_type\":\"epic\"}]" ;;
list|ready) echo '[]' ;;
esac
`)
	faketool.Write(t, dir, "herdr", "#!/bin/sh\necho 'herdr: not in this test' >&2\nexit 1\n")
	onPath(t, dir)
	return dir
}

// runFeatureIn is orchestra with these arguments in a repository set up for it, with a README,
// inside a Herdr pane, without notifications, triage or the run report.
func runFeatureIn(t *testing.T, args ...string) (repo, stdout, stderr string, code int) {
	t.Helper()
	return runFeatureFrom(t, strings.NewReader(""), args...)
}

// runFeatureFrom is runFeatureIn reading stdin from in.
func runFeatureFrom(t *testing.T, in io.Reader, args ...string) (repo, stdout, stderr string, code int) {
	t.Helper()
	return runFeatureAfter(t, func(string) {}, in, args...)
}

// runFeatureAfter is runFeatureFrom with setup given the repository before orchestra runs in it.
func runFeatureAfter(
	t *testing.T, setup func(repo string), in io.Reader, args ...string,
) (repo, stdout, stderr string, code int) {
	t.Helper()
	repo = configFixture(t, `{"concurrent": 1}`)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# lister\n\nLists things.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := command.Output(context.Background(), 0, repo, "sh", "-c",
		"git add README.md && git -c user.name=t -c user.email=t@t commit -q -m readme"); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{"NOTIFY": "0", "TRIAGE": "0", "REVIEW": "0"} {
		setenv(t, k, v)
	}
	setup(repo)
	var out, errOut strings.Builder
	err := run(testCtx(context.Background(), t), append([]string{"orchestra"}, args...), testCmdEnv(t, in, &out, &errOut))
	return repo, out.String(), errOut.String(), exitOf(err)
}
