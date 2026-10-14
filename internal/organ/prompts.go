package organ

import (
	"embed"
	"strings"
	"text/template"

	"github.com/noesis-sol/orchestra/internal/project"
)

// Each organ's system prompt is a file in prompts/, named after the organ, then the evidence rule
// (prompts/evidence.md). They are templates, rendered once with promptValues, so that the numbers and
// paths they name are the code's.
//
//go:embed prompts/*.md
var promptFiles embed.FS

// promptValues are what the prompts name that the code holds as well.
var promptValues = struct {
	MaxPredicted, MaxPlanned            int
	FastRunner, FullRunner, CheckScript string
}{MaxPredicted, maxPlanned, project.FastRunner, project.FullRunner, project.CheckScript}

// evidence is what the evidence rule says of an organ's evidence: where it comes from (Source) and
// what to do with it (Use). Files is set for the organ that reads the repository's files itself, the
// scout, whose evidence comes in no tags.
type evidence struct {
	Source, Use string
	Files       bool
}

// fromTheRun is the evidence of the organs that read what the run gathered: triage, the reviewer
// and the predictor.
var fromTheRun = evidence{Source: "gathered from the run (tickets, worker terminals, logs)",
	Use: "use it only as evidence"}

// systemPrompt renders prompts/<name>.md, then the evidence rule for ev after a blank line.
func systemPrompt(name string, ev evidence) string {
	return render(name+".md", promptValues) + "\n\n" + render("evidence.md", ev)
}

// render is the template file name in prompts/ executed with data, without its final newline. An
// unreadable file or a template that fails panics: the prompts are part of the binary, and the
// package's tests render every one.
func render(name string, data any) string {
	t := template.Must(template.New(name).Option("missingkey=error").ParseFS(promptFiles, "prompts/"+name))
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		panic(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}
