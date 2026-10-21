package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/pathutil"
)

// rerootAgentsFromHomes rebases each agent's skills directory from home into
// agentHome, so a dir such as <home>/.claude/skills becomes
// <agentHome>/.claude/skills. This lets a whole set of agent skills be laid
// out under a folder that another process can be launched against as its HOME.
// An agent dir may be spelled under any of the given spellings of home (e.g.
// the unresolved $HOME and its symlink-resolved form), so both spellings
// re-root to the same directory.
//
// Re-rooting is defined only for directories under home: a dir outside it has
// no home-relative part to transplant, so it is a loud error rather than a
// silent passthrough that would write links to an unexpected place.
func rerootAgentsFromHomes(cfg agents.Config, homes []string, agentHome string) (agents.Config, error) {
	rerooted := make([]agents.Agent, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		relative, found := "", false
		for _, home := range homes {
			rel, err := filepath.Rel(home, agent.Dir)
			if err != nil {
				return agents.Config{}, fmt.Errorf("agent %s: locate %q under home %q: %w", agent.Name, agent.Dir, home, err)
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			relative, found = rel, true
			break
		}
		if !found {
			return agents.Config{}, fmt.Errorf("agent %s: skills dir %q is not under home %q; --agent-home cannot re-root it", agent.Name, agent.Dir, strings.Join(homes, " or "))
		}
		rerooted = append(rerooted, agents.Agent{Name: agent.Name, Dir: filepath.Join(agentHome, relative)})
	}
	return agents.Config{Agents: rerooted}, nil
}

// agentAlias records an agent dropped from the effective config because its
// directory resolves to the same real directory as owner's.
type agentAlias struct {
	name    string
	owner   string
	realDir string
}

// aliasWarnings renders one warning per dropped agent, naming both agents so
// the user can fix agents.toml.
func aliasWarnings(aliases []agentAlias) []string {
	warnings := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		warnings = append(warnings, fmt.Sprintf("agent %s maps to the same directory as agent %s (%s); syncing it once under %s", alias.name, alias.owner, alias.realDir, alias.owner))
	}
	return warnings
}

// applyOverrides is overrideAgents with the dropped aliases rendered as
// warnings, for the commands that only need to report them.
func applyOverrides(storeDir string, cfg agents.Config, storeFlag, agentHomeFlag string) (string, agents.Config, []string, error) {
	resolvedStore, effective, aliases, err := overrideAgents(storeDir, cfg, storeFlag, agentHomeFlag)
	if err != nil {
		return "", agents.Config{}, nil, err
	}
	return resolvedStore, effective, aliasWarnings(aliases), nil
}

// overrideAgents applies the --store and --agent-home flags. An empty flag is a
// no-op, so the defaults flow through unchanged. Flag paths expand only "~"
// (the shell has already expanded variables) and --agent-home is
// symlink-resolved so aliases of one directory key state identically.
//
// Agent directories are then validated: too-broad dirs and dirs overlapping the
// store are errors (copy mode would recurse into itself), and agents that share
// a real directory collapse to the first, each dropped agent returned as an
// alias of the one kept.
func overrideAgents(storeDir string, cfg agents.Config, storeFlag, agentHomeFlag string) (string, agents.Config, []agentAlias, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", agents.Config{}, nil, fmt.Errorf("resolve home directory: %w", err)
	}
	if storeFlag != "" {
		resolved, err := pathutil.Abs(storeFlag)
		if err != nil {
			return "", agents.Config{}, nil, err
		}
		storeDir = resolved
	}
	effectiveHome, err := pathutil.ResolveExisting(home)
	if err != nil {
		return "", agents.Config{}, nil, err
	}
	if agentHomeFlag != "" {
		absolute, err := pathutil.Abs(agentHomeFlag)
		if err != nil {
			return "", agents.Config{}, nil, err
		}
		agentHome, err := pathutil.ResolveExisting(absolute)
		if err != nil {
			return "", agents.Config{}, nil, err
		}
		cfg, err = rerootAgentsFromHomes(cfg, []string{home, effectiveHome}, agentHome)
		if err != nil {
			return "", agents.Config{}, nil, err
		}
		effectiveHome = agentHome
	}
	resolvedStore, err := pathutil.ResolveExisting(storeDir)
	if err != nil {
		return "", agents.Config{}, nil, err
	}

	kept := make([]agents.Agent, 0, len(cfg.Agents))
	ownerByRealDir := make(map[string]string, len(cfg.Agents))
	var aliases []agentAlias
	for _, agent := range cfg.Agents {
		resolvedDir, err := pathutil.ResolveExisting(agent.Dir)
		if err != nil {
			return "", agents.Config{}, nil, fmt.Errorf("agent %s: %w", agent.Name, err)
		}
		// Check the resolved forms so a symlink alias of home, or a spelling
		// using "..", cannot slip past the guard.
		if err := agents.CheckDir(resolvedDir, effectiveHome); err != nil {
			return "", agents.Config{}, nil, fmt.Errorf("agent %s: %w", agent.Name, err)
		}
		if pathutil.Overlaps(resolvedStore, resolvedDir) {
			return "", agents.Config{}, nil, fmt.Errorf("agent %s: skills dir %q overlaps the store %q", agent.Name, agent.Dir, storeDir)
		}
		if owner, seen := ownerByRealDir[resolvedDir]; seen {
			aliases = append(aliases, agentAlias{name: agent.Name, owner: owner, realDir: resolvedDir})
			continue
		}
		ownerByRealDir[resolvedDir] = agent.Name
		kept = append(kept, agent)
	}
	return storeDir, agents.Config{Agents: kept}, aliases, nil
}
