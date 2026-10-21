package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/state"
	"github.com/gherlein/sm/internal/store"
)

// agentCounts classifies every relevant name at one agent. The categories
// are disjoint for store skills (owned, unmanaged, missing, or dangling when
// sm's link there is broken); dangling also covers recorded names whose store
// skill is gone, whatever is on disk.
type agentCounts struct {
	owned     int
	missing   int
	unmanaged int
	dangling  int
}

// ownedLinkIsBroken reports whether the entry at agentDir/name, already known
// to be sm's link by identity, no longer resolves: its target was moved or
// deleted (for example the store moved and sync has not repointed yet).
func ownedLinkIsBroken(agentDir, name string) (bool, error) {
	_, err := os.Stat(filepath.Join(agentDir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect link target of %s: %w", filepath.Join(agentDir, name), err)
	}
	return false, nil
}

// isOwnedLink applies the same identity rule sync uses: a recorded symlink is
// sm's only while its raw readlink equals the recorded target.
func isOwnedLink(entry link.Entry, target state.Target, name string) bool {
	recordedTarget, recorded := target.Links[name]
	return recorded && entry.IsSymlink && entry.Target == recordedTarget
}

// classifyAgent applies one rule per store skill: an entry that is sm's by
// identity (a link whose readlink is the recorded target, a copy whose marker
// names the recorded store path) is owned, unless it is a link that no longer
// resolves, which is dangling; any other entry at that name is unmanaged (so a
// name is never counted as both unmanaged and missing); nothing there is
// missing. A recorded name absent from the store is dangling. A recorded name
// whose store entry exists but is unusable is kept by sync, so it is dangling
// only when its link is broken.
func classifyAgent(agent agents.Agent, storeNames, unusable map[string]bool, target state.Target) (agentCounts, error) {
	var counts agentCounts
	for name := range storeNames {
		entry, err := link.Probe(agent.Dir, name)
		if err != nil {
			return agentCounts{}, fmt.Errorf("agent %s: %w", agent.Name, err)
		}
		recordedSource, recordedCopy := target.Copies[name]
		switch {
		case !entry.Exists:
			counts.missing++
		case isOwnedLink(entry, target, name):
			broken, err := ownedLinkIsBroken(agent.Dir, name)
			if err != nil {
				return agentCounts{}, fmt.Errorf("agent %s: %w", agent.Name, err)
			}
			if broken {
				counts.dangling++
			} else {
				counts.owned++
			}
		case recordedCopy:
			ownedCopy, err := link.IsOwnedCopy(agent.Dir, name, recordedSource)
			if err != nil {
				return agentCounts{}, fmt.Errorf("agent %s: %w", agent.Name, err)
			}
			if ownedCopy {
				counts.owned++
			} else {
				counts.unmanaged++
			}
		default:
			counts.unmanaged++
		}
	}

	recordedNames := make(map[string]bool, len(target.Links)+len(target.Copies))
	for name := range target.Links {
		recordedNames[name] = true
	}
	for name := range target.Copies {
		recordedNames[name] = true
	}
	for name := range recordedNames {
		if storeNames[name] {
			continue
		}
		if !unusable[name] {
			counts.dangling++
			continue
		}
		entry, err := link.Probe(agent.Dir, name)
		if err != nil {
			return agentCounts{}, fmt.Errorf("agent %s: %w", agent.Name, err)
		}
		if !isOwnedLink(entry, target, name) {
			continue
		}
		broken, err := ownedLinkIsBroken(agent.Dir, name)
		if err != nil {
			return agentCounts{}, fmt.Errorf("agent %s: %w", agent.Name, err)
		}
		if broken {
			counts.dangling++
		}
	}
	return counts, nil
}

// doStatus is strictly read-only: it never creates, removes or saves. With
// orphanScope it also lists state targets no configured agent maps; that is
// only meaningful when the agent dirs are the configured ones, not re-rooted.
func doStatus(cfg agents.Config, storeDir, statePath string, orphanScope bool, out io.Writer) error {
	listing, err := store.Enumerate(storeDir)
	if err != nil {
		return err
	}
	storeNames := make(map[string]bool, len(listing.Skills))
	for _, skill := range listing.Skills {
		storeNames[skill.Name] = true
	}
	unusable := make(map[string]bool, len(listing.Unusable))
	for _, name := range listing.Unusable {
		unusable[name] = true
	}
	current, err := state.Load(statePath)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "store: %d skills (%s)\n", len(listing.Skills), storeDir)
	for _, warning := range listing.Warnings {
		fmt.Fprintf(out, "warning: %s\n", warning)
	}
	for _, agent := range cfg.Agents {
		counts, err := classifyAgent(agent, storeNames, unusable, current.Targets[agent.Dir])
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%-14s owned=%d missing=%d unmanaged=%d dangling=%d  %s\n",
			agent.Name, counts.owned, counts.missing, counts.unmanaged, counts.dangling, agent.Dir)
	}
	if !orphanScope {
		return nil
	}
	unmapped, err := classifyUnmapped(cfg, current)
	if err != nil {
		return err
	}
	for _, target := range unmapped {
		entries := len(current.Targets[target.dir].Links) + len(current.Targets[target.dir].Copies)
		switch target.kind {
		case aliasTarget:
			fmt.Fprintf(out, "aliased target: %s (same directory as agent %s)\n", target.dir, target.agentName)
		case provisionedTarget:
			fmt.Fprintf(out, "provisioned target: %s (%d entries; clean with: sm clean --agent-home %s)\n", target.dir, entries, target.agentHome)
		case orphanTarget:
			fmt.Fprintf(out, "orphaned target: %s (%d entries)\n", target.dir, entries)
		case unclassifiableTarget:
			fmt.Fprintf(out, "unclassifiable target: %s (%s)\n", target.dir, target.reason)
		default:
			return fmt.Errorf("state target %s: unknown classification %d", target.dir, target.kind)
		}
	}
	return nil
}
