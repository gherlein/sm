// Package agents maps (agent, scope) to the directory the agent reads skills from.
package agents

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/brightsign-playground/sm/internal/config"
)

var Known = []string{"claude-code", "copilot", "hax", "oh-my-pi", "pi"}

type dirs struct{ global, project []string }

var registry = map[string]dirs{
	"claude-code": {[]string{".claude", "skills"}, []string{".claude", "skills"}},
	"copilot":     {[]string{".copilot", "skills"}, []string{".github", "skills"}},
	"hax":         {[]string{".config", "hax", "skills"}, []string{".agents", "skills"}},
	"pi":          {[]string{".pi-go", "skills"}, []string{".pi", "skills"}},
	"oh-my-pi":    {[]string{".config", "agents", "skills"}, []string{".agents", "skills"}},
}

func TargetDir(id, scope, root string) (string, error) {
	d, ok := registry[id]
	if !ok {
		return "", fmt.Errorf("unknown agent %q (known: %v)", id, Known)
	}
	var parts []string
	switch scope {
	case "global":
		parts = d.global
	case "project":
		parts = d.project
	default:
		return "", fmt.Errorf("unknown scope %q", scope)
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

func Enabled(m *config.Manifest) ([]string, error) {
	var out []string
	for id, on := range m.Agents {
		if _, ok := registry[id]; !ok {
			return nil, fmt.Errorf("unknown agent %q in [agents] (known: %v)", id, Known)
		}
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}
