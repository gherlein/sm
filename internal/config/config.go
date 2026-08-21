// SPDX-License-Identifier: MIT
// Package config parses, validates, and writes the skills-mapper manifest.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

type Source struct {
	Git    string `toml:"git,omitempty"`
	Ref    string `toml:"ref,omitempty"`
	Subdir string `toml:"subdir,omitempty"`
	Path   string `toml:"path,omitempty"`
}

type Options struct {
	PrefixOnCollision bool `toml:"prefix_on_collision"`
}

type Manifest struct {
	Skills  map[string]Source `toml:"skills"`
	Agents  map[string]bool   `toml:"agents"`
	Options Options           `toml:"options"`
}

// Parse decodes and validates: each source has exactly one of git or path.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse skills.toml: %w", err)
	}
	if m.Skills == nil {
		m.Skills = map[string]Source{}
	}
	if m.Agents == nil {
		m.Agents = map[string]bool{}
	}
	for _, alias := range sortedKeys(m.Skills) {
		s := m.Skills[alias]
		if (s.Git != "") == (s.Path != "") {
			return nil, fmt.Errorf("source %q: specify exactly one of git or path", alias)
		}
	}
	return &m, nil
}

func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "skills-mapper", "skills.toml"), nil
}

func sortedKeys(m map[string]Source) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
