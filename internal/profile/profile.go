// Package profile parses and locates skill-subset profiles: a hand-edited list
// of skill names to project from the store into a chosen target folder.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

// Parse decodes a profile's TOML (`skills = [...]`) and returns the skill names
// de-duplicated in first-seen order, the sorted list of names that appeared more
// than once (so the caller can warn), and an error if the TOML is malformed or
// the profile names no skills. A profile that maps nothing is a mistake, not a
// no-op, so an empty or absent list is an error.
func Parse(data []byte) (names []string, duplicates []string, err error) {
	var p struct {
		Skills []string `toml:"skills"`
	}
	if err := toml.Unmarshal(data, &p); err != nil {
		return nil, nil, fmt.Errorf("parse profile: %w", err)
	}
	seen := map[string]bool{}
	dupSet := map[string]bool{}
	for _, s := range p.Skills {
		if seen[s] {
			dupSet[s] = true
			continue
		}
		seen[s] = true
		names = append(names, s)
	}
	if len(names) == 0 {
		return nil, nil, fmt.Errorf("profile lists no skills")
	}
	for d := range dupSet {
		duplicates = append(duplicates, d)
	}
	sort.Strings(duplicates)
	return names, duplicates, nil
}

// DefaultDir is where named profiles live:
// <XDG_CONFIG_HOME|~/.config>/skills-mapper/profiles.
func DefaultDir() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "skills-mapper", "profiles"), nil
}

// NamePath maps a profile dir and a profile name to the profile file path.
func NamePath(dir, name string) string {
	return filepath.Join(dir, name+".toml")
}
