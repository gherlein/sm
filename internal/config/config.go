// SPDX-License-Identifier: MIT
// Package config parses, validates, and writes the skills-mapper manifest.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

var invalidAliasChars = "/\\.:"

func (m *Manifest) AddSource(alias string, s Source) error {
	if alias == "" || strings.ContainsAny(alias, invalidAliasChars) {
		return fmt.Errorf("invalid alias %q (no / \\ . :)", alias)
	}
	if _, ok := m.Skills[alias]; ok {
		return fmt.Errorf("source %q already exists", alias)
	}
	if (s.Git != "") == (s.Path != "") {
		return fmt.Errorf("source %q: specify exactly one of git or path", alias)
	}
	if m.Skills == nil {
		m.Skills = map[string]Source{}
	}
	m.Skills[alias] = s
	return nil
}

func (m *Manifest) RemoveSource(alias string) bool {
	if _, ok := m.Skills[alias]; !ok {
		return false
	}
	delete(m.Skills, alias)
	return true
}

func Save(path string, m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(m)
}

// ProjectManifestPath returns the nearest skills.toml at or below the repo,
// walking up from startDir and stopping before home (home itself excluded).
func ProjectManifestPath(startDir, home string) (string, bool) {
	dir := startDir
	for {
		if dir == home || dir == filepath.Dir(dir) {
			return "", false
		}
		p := filepath.Join(dir, "skills.toml")
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		dir = filepath.Dir(dir)
	}
}
