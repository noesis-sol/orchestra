package project

import (
	"os"
	"strings"
	"testing"
)

// Both worker prompts, the template init writes and this repository's own, have under Close a decision
// about another ticket filed as a question blocking that ticket and asked from the worker's own (so the
// answer talk shows where it came from), never left only in the final message, and every question given
// 2 to 4 options, one recommended, in its metadata and its description.
func TestWorkerPromptFilesQuestionsWithOptions(t *testing.T) {
	own, err := os.ReadFile("../../.orchestra/worker-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{"template": promptTemplate, "repository's": string(own)} {
		all := sections(prompt)
		i := indexOf(all, "Close")
		if i < 0 {
			t.Errorf("the %s prompt has no Close:\n%s", name, prompt)
			continue
		}
		closing := strings.Join(strings.Fields(all[i].text), " ")
		for _, want := range []string{
			"`bd create --type=task --labels human --deps blocks:TICKET_ID --title " +
				`"<the question, at most 60 characters>" --description ` +
				`"<the context, the options and your recommendation>" --metadata '<the options, as below>'` + "`",
			"A decision only the maintainer can make about another ticket, a follow-up you file or one you name, " +
				"is a question too: file it the same way, blocking that ticket and asked from yours " +
				"(`--deps 'blocks:<that ticket>,discovered-from:TICKET_ID'`), and go on with yours.",
			"Never leave it only in your final message: nobody reads that, and that ticket's worker would " +
				"decide it instead. Your final message carries nothing for the maintainer.",
			"Every question gives 2 to 4 options, each a short label and a one-line trade-off, one of them " +
				"recommended, in its description and in its metadata, in the same order:",
			`--metadata '{"options": [{"label": "<label>", "description": "<its trade-off>"}, ...], ` +
				`"recommended": <the recommended option's index, from 0>}'`,
			"`--metadata @<the file>`",
		} {
			if !strings.Contains(closing, want) {
				t.Errorf("the %s prompt's Close lacks %q:\n%s", name, want, all[i].text)
			}
		}
	}
}
