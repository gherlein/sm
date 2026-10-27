// Package skill carries the one skill that teaches an agent to use sm and ms,
// embedded in the binary so a lone executable can install or print it with no
// other files present (see the Skill Self-Installation Specification). The
// embed directive lives
// one level above the skill directory because the embed package cannot reach a
// parent or sibling; the embedded directory's name is the installed skill's
// name and must equal the frontmatter name in its SKILL.md.
package skill

import "embed"

// Name is the carried skill's directory name, which equals the frontmatter
// name and becomes the store entry's name on install.
const Name = "using-skills-mapper"

// FS carries the whole skill directory, every file included (the all: prefix
// keeps dotfiles an ordinary embed would drop), so install-skill can recreate
// the tree faithfully.
//
//go:embed all:using-skills-mapper
var FS embed.FS
