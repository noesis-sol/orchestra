package organ

import (
	"encoding/json"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// FuzzParsePlan gives parsePlan any answer, and any tracked files, one per line. A plan it accepts
// keeps parsePlan's promises (checkPlan), and reads the same when written out and read again.
// go test runs the seeds; docs/development.md says how to fuzz.
func FuzzParsePlan(f *testing.F) {
	tracked := strings.Join(planTracked, "\n")
	for _, plan := range []string{
		goodPlan,
		ticketsJSON(`{}`, `{"blocked_by":["t1"]}`, `{"blocked_by":["t1","t2"]}`, `{"blocked_by":["t1"]}`),
		ticketsJSON(`{"files":["cmd/list.go","./cmd/new.go","NEW.md","internal/out","nowhere/x.go",` +
			`"../outside.go","/etc/passwd","cmd\\win.go"," "]}`),
		// Cleaned, these began or ended with a space, which reading them again trimmed off.
		ticketsJSON(`{"files":["./ NEW.md","x/../ new.go","NEW.md /.","cmd/ new.go"]}`),
		ticketsJSON(`{"key":"t1"}`, `{"key":" t1 "}`),
		ticketsJSON(`{}`, `{"blocked_by":["t9"," ",""]}`),
		ticketsJSON(`{"blocked_by":["t3"]}`, `{"blocked_by":["t1"]}`, `{"blocked_by":["t2"]}`),
		ticketsJSON(`{"type":"epic","priority":5}`),
		ticketsJSON(`{"priority":null}`),
		strings.Replace(ticketsJSON(`{}`), `"epic_title":"E"`, `"epic_title":" "`, 1),
		`{"epic_title":"","epic_description":"","tickets":[],"questions":[" Which commands get --json? ",""]}`,
		`{"epic_title":"","epic_description":"","tickets":[],"questions":[" "]}`,
		`"a plan"`,
		"",
	} {
		f.Add(plan, tracked)
	}
	f.Fuzz(func(t *testing.T, plan, lines string) {
		// git ls-files lists clean paths inside the repository.
		var tracked []string
		for l := range strings.SplitSeq(lines, "\n") {
			if filepath.IsLocal(l) && path.Clean(l) == l {
				tracked = append(tracked, l)
			}
		}
		p, err := parsePlan(Result{Structured: json.RawMessage(plan)}, tracked)
		if err != nil {
			return
		}
		checkPlan(t, p, tracked)
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("the plan %+v can't be written out: %v", p, err)
		}
		again, err := parsePlan(Result{Structured: out}, tracked)
		if err != nil {
			t.Fatalf("the plan %s, accepted from %q, fails when read again: %v", out, plan, err)
		}
		if len(again.Notes) != 0 {
			t.Errorf("the plan %s, accepted from %q, has notes when read again: %q", out, plan, again.Notes)
		}
		p.Notes = nil
		if !reflect.DeepEqual(again, p) {
			t.Errorf("the plan accepted from %q reads differently when read again:\n%+v\n%+v", plan, p, again)
		}
	})
}

// A file that, cleaned, starts or ends with a space is dropped: the model meant one without, and
// the plan, read again, would name that one.
func TestPlanDropsAFileCleanedToASpace(t *testing.T) {
	plan := ticketsJSON(`{"files":["./ NEW.md","x/../ new.go","NEW.md /.","cmd/ new.go"]}`)
	p, err := parsePlan(Result{Structured: []byte(plan)}, planTracked)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Tickets[0].Files; !slices.Equal(got, []string{"cmd/ new.go"}) {
		t.Errorf("kept %q", got)
	}
	want := []string{
		`dropped "./ NEW.md" from t1's files: it starts or ends with a space`,
		`dropped "x/../ new.go" from t1's files: it starts or ends with a space`,
		`dropped "NEW.md /." from t1's files: it starts or ends with a space`,
	}
	if !slices.Equal(p.Notes, want) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(p.Notes, "\n"), strings.Join(want, "\n"))
	}
}

// checkPlan fails t where plan p, which parsePlan accepted with the tracked files, breaks a promise
// of parsePlan's: questions and no tickets, or 1 to maxPlanned tickets under an epic with a title,
// with unique keys, titles, known types, priorities 0 to 4, blockers that are other tickets' keys
// with no cycle, and files that are clean paths inside the repository, tracked or new in a
// directory holding tracked files.
func checkPlan(t *testing.T, p FeaturePlan, tracked []string) {
	t.Helper()
	for _, q := range p.Questions {
		if q == "" || q != strings.TrimSpace(q) {
			t.Errorf("question %q is blank or untrimmed", q)
		}
	}
	if len(p.Tickets) == 0 {
		if len(p.Questions) == 0 {
			t.Error("a plan with no tickets and no questions was accepted")
		}
		return
	}
	if len(p.Tickets) > maxPlanned || p.Epic.Title == "" {
		t.Errorf("%d tickets under the epic %q", len(p.Tickets), p.Epic.Title)
	}
	keys := map[string]bool{}
	for _, tk := range p.Tickets {
		if tk.Key == "" || keys[tk.Key] {
			t.Errorf("the key %q is blank or taken", tk.Key)
		}
		keys[tk.Key] = true
		if tk.Title == "" || !slices.Contains([]string{"task", "feature", "bug", "chore"}, tk.Type) ||
			tk.Priority < 0 || tk.Priority > 4 {
			t.Errorf("ticket %s has the title %q, type %q and priority %d", tk.Key, tk.Title, tk.Type, tk.Priority)
		}
	}
	under := func(dir string) bool {
		return dir == "." || slices.ContainsFunc(tracked, func(f string) bool { return strings.HasPrefix(f, dir+"/") })
	}
	for _, tk := range p.Tickets {
		for i, b := range tk.BlockedBy {
			if b == tk.Key || !keys[b] || slices.Contains(tk.BlockedBy[:i], b) {
				t.Errorf("ticket %s is blocked by %q: itself, unknown or again", tk.Key, b)
			}
		}
		for i, f := range tk.Files {
			switch {
			case !filepath.IsLocal(f) || strings.Contains(f, `\`) || path.Clean(f) != f || strings.TrimSpace(f) != f:
				t.Errorf("ticket %s has the file %q, which isn't a clean path inside the repository", tk.Key, f)
			case slices.Contains(tk.Files[:i], f):
				t.Errorf("ticket %s has the file %q twice", tk.Key, f)
			case !slices.Contains(tracked, f) && (under(f) || !under(path.Dir(f))):
				t.Errorf("ticket %s has the file %q, which is a directory or in no directory of the repository",
					tk.Key, f)
			}
		}
	}
	// With no cycle, taking out the tickets whose blockers are out, again and again, takes out every one.
	out := map[string]bool{}
	for more := true; more; {
		more = false
		for _, tk := range p.Tickets {
			if !out[tk.Key] && !slices.ContainsFunc(tk.BlockedBy, func(b string) bool { return !out[b] }) {
				out[tk.Key], more = true, true
			}
		}
	}
	if len(out) != len(p.Tickets) {
		t.Errorf("the tickets block each other in a cycle: %+v", p.Tickets)
	}
}
