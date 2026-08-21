// Package discovery finds skills (dirs with a SKILL.md) in a source tree.
package discovery

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Skill struct {
	Alias, Name, SourceDir, Description string
}

var ignored = map[string]bool{
	".git": true, "node_modules": true, ".next": true, ".turbo": true,
	"vendor": true, "dist": true, "build": true, "__pycache__": true,
}

func Discover(root, alias string) ([]Skill, error) {
	var out []Skill
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && ignored[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, desc := parseFrontmatter(data)
		if name == "" {
			return nil // not standard-compliant; skip
		}
		dir := filepath.Dir(path)
		out = append(out, Skill{Alias: alias, Name: filepath.Base(dir), SourceDir: dir, Description: desc})
		return nil
	})
	return out, err
}

func parseFrontmatter(b []byte) (name, description string) {
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return "", ""
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if yaml.Unmarshal([]byte(rest[:end]), &fm) != nil {
		return "", ""
	}
	return fm.Name, fm.Description
}
