package command

import "strings"

// ShellEnv is env, in the form of this package's env entries (NAME=value sets the variable, a name
// alone leaves it out), as lines of a /bin/sh script: an unset for a name alone, else an assignment
// and an export, so that what the script runs next has that environment.
func ShellEnv(env []string) string {
	var b strings.Builder
	for _, kv := range env {
		name, value, set := strings.Cut(kv, "=")
		if !set {
			b.WriteString("unset " + name + "\n")
			continue
		}
		b.WriteString(name + "=" + ShellQuote(value) + "\nexport " + name + "\n")
	}
	return b.String()
}
