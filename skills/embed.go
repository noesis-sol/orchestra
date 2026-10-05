// Package skills holds the skills orchestra installs into a project, built into the binary: go:embed
// can't reach a parent folder, so the package that embeds them lives beside them.
package skills

import (
	"embed"
	"io/fs"
)

// CreateCheckSuiteName is the create-check-suite skill's folder name, in this folder and in a
// project's skill folder.
const CreateCheckSuiteName = "create-check-suite"

//go:embed create-check-suite
var files embed.FS

// CreateCheckSuite is the create-check-suite skill's files, SKILL.md at its top.
func CreateCheckSuite() fs.FS {
	sub, err := fs.Sub(files, CreateCheckSuiteName)
	if err != nil {
		panic(err) // the folder is embedded above: fs.Sub fails only on an invalid name
	}
	return sub
}
