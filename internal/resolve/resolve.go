// Package resolve turns discovered skills into an install plan and enforces the
// collision policy.
package resolve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gherlein/skills-mapper/internal/config"
	"github.com/gherlein/skills-mapper/internal/discovery"
)

type Link struct{ Name, SourceDir, Alias string }
type Plan struct{ Links []Link }

type CollisionError struct {
	Name    string
	Aliases []string
}

func (e *CollisionError) Error() string {
	return fmt.Sprintf("duplicate skill %q from sources %s (rename, drop one, or set prefix_on_collision)",
		e.Name, strings.Join(e.Aliases, ", "))
}

func Resolve(skills []discovery.Skill, opts config.Options) (*Plan, error) {
	if !opts.PrefixOnCollision {
		byName := map[string][]string{}
		for _, s := range skills {
			byName[s.Name] = append(byName[s.Name], s.Alias)
		}
		for name, aliases := range byName {
			if len(aliases) > 1 {
				sort.Strings(aliases)
				return nil, &CollisionError{Name: name, Aliases: aliases}
			}
		}
	}
	var links []Link
	for _, s := range skills {
		name := s.Name
		if opts.PrefixOnCollision {
			name = s.Alias + "-" + s.Name
		}
		links = append(links, Link{Name: name, SourceDir: s.SourceDir, Alias: s.Alias})
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Name < links[j].Name })
	return &Plan{Links: links}, nil
}
