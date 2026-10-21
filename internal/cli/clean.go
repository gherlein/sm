package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/state"
)

// noOrphansSummary is the clean --orphans output when every state target is
// still mapped by agents.toml.
const noOrphansSummary = "clean: no orphaned targets"

// withCleanHint adds, to a refusal of an unsafe agent directory, how to clean
// the other agents while it is being fixed; the fix itself is in the refusal.
// A run already scoped with --agent (only) asked for this agent alone, so it
// gets no suggestion, and neither does a configuration with no other agent.
func withCleanHint(cfg agents.Config, agent agents.Agent, only string, err error) error {
	var unsafe *link.UnsafeDirError
	if !errors.As(err, &unsafe) || only != "" {
		return err
	}
	var others []string
	for _, other := range cfg.Agents {
		if other.Name != agent.Name {
			others = append(others, "sm clean --agent "+other.Name)
		}
	}
	if len(others) == 0 {
		return err
	}
	return fmt.Errorf("%w; to clean the other agents meanwhile, run: %s", err, strings.Join(others, ", "))
}

// doClean removes only entries recorded in state whose identity still matches
// the record (a link's readlink, a copy's marker), so unmanaged entries and
// user-replaced links survive even if a name collides. With orphans set it
// acts on the state targets no configured agent maps instead of on the
// agents (see cleanOrphans); it cannot be combined with only.
func doClean(cfg agents.Config, statePath string, only string, orphans bool, out io.Writer) (summary string, err error) {
	if orphans && only != "" {
		return "", errors.New("--orphans cannot be combined with --agent")
	}
	// Without agents a plain clean has nothing in scope; saying so beats a
	// bare "clean:" that reads like a silent no-op. --orphans still applies,
	// and a named --agent is still reported as unknown below.
	if !orphans && only == "" && len(cfg.Agents) == 0 {
		return noAgentsMessage, nil
	}
	inScope := cfg.Agents
	if only != "" {
		inScope = nil
		for _, agent := range cfg.Agents {
			if agent.Name == only {
				inScope = append(inScope, agent)
			}
		}
		if len(inScope) == 0 {
			return "", fmt.Errorf("unknown agent %q", only)
		}
	}

	unlock, err := state.Lock(statePath, stateLockHolder)
	if err != nil {
		return "", err
	}
	// The named results are assigned so a failed unlock is never lost.
	defer func() {
		if unlockErr := unlock(); unlockErr != nil {
			summary, err = "", errors.Join(err, unlockErr)
		}
	}()

	current, err := state.Load(statePath)
	if err != nil {
		return "", err
	}
	if orphans {
		parts, changed, err := cleanOrphans(cfg, &current, statePath, out)
		if err != nil {
			return "", err
		}
		if changed {
			if err := current.Save(statePath); err != nil {
				return "", err
			}
		}
		if len(parts) == 0 {
			return noOrphansSummary, nil
		}
		return "clean: " + strings.Join(parts, "   "), nil
	}

	var parts []string
	for _, agent := range inScope {
		// Records kept under an earlier spelling of this directory (an alias
		// key) describe entries in it too; without adopting them first, clean
		// would leave those links behind. Adoption is identity-checked, so a
		// record whose entry the user replaced is never claimed.
		if err := adoptAllAliasRecords(&current, agent, out); err != nil {
			return "", persistThenFail(current, statePath, fmt.Errorf("agent %s: %w", agent.Name, err))
		}
		removedCount, err := cleanTarget(&current, agent, statePath, out)
		if err != nil {
			return "", withCleanHint(cfg, agent, only, err)
		}
		parts = append(parts, fmt.Sprintf("%s -%d", agent.Name, removedCount))
	}

	if err := current.Save(statePath); err != nil {
		return "", err
	}
	return "clean: " + strings.Join(parts, "   "), nil
}

// unmappedKind says why a state target is not mapped by a configured agent,
// which decides whether clean --orphans may touch it.
type unmappedKind int

const (
	// orphanTarget: an agent removed from agents.toml; clean --orphans
	// removes its sm entries.
	orphanTarget unmappedKind = iota + 1
	// aliasTarget: another spelling of a configured agent's real directory;
	// its records describe that agent's own entries, so nothing is removed.
	aliasTarget
	// provisionedTarget: a configured agent re-rooted by sync --agent-home;
	// only an explicit clean --agent-home may remove it.
	provisionedTarget
	// unclassifiableTarget: the key could not be resolved (a symlink loop, an
	// unreadable parent), so nothing is known about it and nothing is done.
	unclassifiableTarget
)

// unmappedTarget is a state target no configured agent maps. agentName is the
// agent an alias or provisioned target belongs to; agentDir is the alias
// owner's own state key; agentHome is the root a provisioned target was
// re-rooted under; reason says why a target is unclassifiable.
type unmappedTarget struct {
	dir       string
	kind      unmappedKind
	agentName string
	agentDir  string
	agentHome string
	reason    string
}

// classifyUnmapped returns, sorted by directory, the state targets no
// configured agent maps, each classified. Raw key comparison alone is not
// enough to call a target an orphan: an alias key shares its physical entries
// with a live agent, and a --agent-home profile is deliberately provisioned,
// so treating either as an orphan would delete entries something still uses.
func classifyUnmapped(cfg agents.Config, current state.State) ([]unmappedTarget, error) {
	mapped := make(map[string]bool, len(cfg.Agents))
	ownerByRealDir := make(map[string]agents.Agent, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		mapped[agent.Dir] = true
		realDir, err := pathutil.ResolveExisting(agent.Dir)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", agent.Name, err)
		}
		ownerByRealDir[realDir] = agent
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	resolvedHome, err := pathutil.ResolveExisting(home)
	if err != nil {
		return nil, err
	}
	homes := []string{home, resolvedHome}

	var unmapped []unmappedTarget
	for _, dir := range sortedTargetDirs(current) {
		if mapped[dir] {
			continue
		}
		// One stale key must not make status or clean unusable; without a
		// resolved path nothing about it is known, so it is only reported.
		realDir, err := pathutil.ResolveExisting(dir)
		if err != nil {
			unmapped = append(unmapped, unmappedTarget{dir: dir, kind: unclassifiableTarget, reason: err.Error()})
			continue
		}
		if owner, isAlias := ownerByRealDir[realDir]; isAlias {
			unmapped = append(unmapped, unmappedTarget{dir: dir, kind: aliasTarget, agentName: owner.Name, agentDir: owner.Dir})
			continue
		}
		if agentName, agentHome, isProvisioned := provisionedUnder(cfg, homes, dir); isProvisioned {
			unmapped = append(unmapped, unmappedTarget{dir: dir, kind: provisionedTarget, agentName: agentName, agentHome: agentHome})
			continue
		}
		unmapped = append(unmapped, unmappedTarget{dir: dir, kind: orphanTarget})
	}
	return unmapped, nil
}

// provisionedUnder reports whether dir is a configured agent's directory
// re-rooted the way sync --agent-home does it: <agent home>/<the agent dir
// relative to home>. It mirrors rerootAgentsFromHomes, which accepts an agent
// dir spelled under any of the spellings of home (the unresolved $HOME and its
// symlink-resolved form), so homes must be the same spellings. The longest
// matching relative path wins, so an agent at ~/skills does not claim
// /ctr/.pi/skills from an agent at ~/.pi/skills (which would also give the
// wrong --agent-home root).
func provisionedUnder(cfg agents.Config, homes []string, dir string) (agentName, agentHome string, isProvisioned bool) {
	longest := 0
	for _, agent := range cfg.Agents {
		relative, found := relativeToAnyHome(homes, agent.Dir)
		if !found {
			continue
		}
		suffix := string(filepath.Separator) + relative
		if !strings.HasSuffix(dir, suffix) {
			continue
		}
		root := strings.TrimSuffix(dir, suffix)
		if root == "" || len(suffix) <= longest {
			continue
		}
		longest = len(suffix)
		agentName, agentHome, isProvisioned = agent.Name, root, true
	}
	return agentName, agentHome, isProvisioned
}

// relativeToAnyHome returns dir relative to the first spelling of home it lies
// strictly under; dir equal to home has no re-rootable part.
func relativeToAnyHome(homes []string, dir string) (string, bool) {
	for _, home := range homes {
		relative, err := filepath.Rel(home, dir)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		return relative, true
	}
	return "", false
}

// mergeAliasRecords moves into the owner's key every record the alias key
// holds that the owner's ledger lacks, but only while the entry still
// verifies by identity at the owner's directory. Without this, a user whose
// previous release recorded only the alias spelling would have those entries
// untracked for good, warned about as "not managed by sm" on every sync.
// Records that do not verify are left out and their entries are not touched.
// Each merge is applied to current immediately, so a failure part way leaves
// only verified records in place for the caller to persist.
func mergeAliasRecords(current *state.State, alias unmappedTarget) (int, error) {
	from := current.Targets[alias.dir]
	owner := current.Targets[alias.agentDir]
	if owner.Links == nil {
		owner.Links = map[string]string{}
	}
	if owner.Copies == nil {
		owner.Copies = map[string]string{}
	}
	current.Targets[alias.agentDir] = owner
	mergedLinks, err := adoptVerifiedRecords(alias.agentDir, from.Links, owner.Links, linkVerifies)
	if err != nil {
		return mergedLinks, err
	}
	mergedCopies, err := adoptVerifiedRecords(alias.agentDir, from.Copies, owner.Copies, link.IsOwnedCopy)
	return mergedLinks + mergedCopies, err
}

// adoptAllAliasRecords runs sync's alias adoption for both ledgers, since
// clean removes links and copies alike.
func adoptAllAliasRecords(current *state.State, agent agents.Agent, out io.Writer) error {
	for _, copyMode := range []bool{false, true} {
		if _, err := adoptAliasRecords(current, agent, syncOptions{CopyMode: copyMode}, out); err != nil {
			return err
		}
	}
	return nil
}

// recordVerifier reports whether the entry at dir/name is still the one sm
// recorded, where recorded is the ledger value (a link's target, a copy's
// store path): link.IsOwnedCopy for copies, linkVerifies for links.
type recordVerifier func(dir, name, recorded string) (bool, error)

// linkVerifies applies the symlink identity rule: the entry is sm's only while
// it is a symlink whose raw readlink equals the recorded target.
func linkVerifies(dir, name, recordedTarget string) (bool, error) {
	entry, err := link.Probe(dir, name)
	if err != nil {
		return false, err
	}
	return entry.IsSymlink && entry.Target == recordedTarget, nil
}

// adoptVerifiedRecords moves into owner every record of alias that verify
// confirms describes the entry at ownerDir; it returns how many records moved.
// A name owner already records keeps the owner's record while that record
// verifies (the verified owner wins, so ownership never flips); an owner
// record that does NOT verify is stale and is replaced by a verified alias
// record, since otherwise sync would warn that the entry is no longer sm's
// while the alias still proves it is. A record that does not verify stays in
// alias; no entry is ever touched. Each record moves as soon as it verifies,
// so a failure part way leaves only verified records moved, and none
// duplicated, for the caller to persist.
func adoptVerifiedRecords(ownerDir string, alias, owner map[string]string, verify recordVerifier) (int, error) {
	adopted := 0
	for _, name := range sortedKeys(alias) {
		if ownerRecord, present := owner[name]; present {
			ownerVerified, err := verify(ownerDir, name, ownerRecord)
			if err != nil {
				return adopted, err
			}
			if ownerVerified {
				continue
			}
		}
		verified, err := verify(ownerDir, name, alias[name])
		if err != nil {
			return adopted, err
		}
		if !verified {
			continue
		}
		owner[name] = alias[name]
		delete(alias, name)
		adopted++
	}
	return adopted, nil
}

func sortedTargetDirs(current state.State) []string {
	dirs := make([]string, 0, len(current.Targets))
	for dir := range current.Targets {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

// cleanOrphans removes the sm entries of true orphans, drops the duplicate
// records of alias keys without touching the filesystem (the live agent's own
// key keeps the real record), and leaves provisioned targets alone, saying
// how to clean them. It reports whether state changed.
func cleanOrphans(cfg agents.Config, current *state.State, statePath string, out io.Writer) (parts []string, changed bool, err error) {
	unmapped, err := classifyUnmapped(cfg, *current)
	if err != nil {
		return nil, false, err
	}
	for _, target := range unmapped {
		switch target.kind {
		case aliasTarget:
			merged, err := mergeAliasRecords(current, target)
			if merged > 0 {
				changed = true
				fmt.Fprintf(out, "merged %d record(s) from %s into %s\n", merged, target.dir, target.agentDir)
			}
			if err != nil {
				return nil, changed, persistThenFail(*current, statePath, fmt.Errorf("state target %s: %w", target.dir, err))
			}
			delete(current.Targets, target.dir)
			changed = true
			fmt.Fprintf(out, "%s: same directory as agent %s; dropped its duplicate record, removed nothing\n", target.dir, target.agentName)
		case unclassifiableTarget:
			fmt.Fprintf(out, "unclassifiable target: %s (%s)\n", target.dir, target.reason)
		case provisionedTarget:
			fmt.Fprintf(out, "%s: provisioned with --agent-home; left alone (clean with: sm clean --agent-home %s)\n", target.dir, target.agentHome)
		case orphanTarget:
			// An orphan has no agent name any more; its directory names it.
			orphan := agents.Agent{Name: target.dir, Dir: target.dir}
			removedCount, err := cleanTarget(current, orphan, statePath, out)
			if err != nil {
				return nil, changed, err
			}
			changed = true
			parts = append(parts, fmt.Sprintf("%s -%d", orphan.Name, removedCount))
		default:
			return nil, changed, fmt.Errorf("state target %s: unknown classification %d", target.dir, target.kind)
		}
	}
	return parts, changed, nil
}

// cleanTarget removes every sm-owned entry recorded for one directory and
// drops the directory from state. Remaining records are kept in state and
// saved before an error is returned, so a mid-run failure leaves state
// matching the filesystem.
func cleanTarget(current *state.State, agent agents.Agent, statePath string, out io.Writer) (int, error) {
	target := current.Targets[agent.Dir]
	remainingLinks := make(map[string]string, len(target.Links))
	for name, storePath := range target.Links {
		remainingLinks[name] = storePath
	}
	remainingCopies := make(map[string]string, len(target.Copies))
	for name, storePath := range target.Copies {
		remainingCopies[name] = storePath
	}
	current.Targets[agent.Dir] = state.Target{Links: remainingLinks, Copies: remainingCopies}
	fail := func(removedCount int, cause error) (int, error) {
		return removedCount, persistThenFail(*current, statePath, fmt.Errorf("agent %s: %w", agent.Name, cause))
	}

	removedCount := 0
	// Only a link still pointing at the recorded target is ours. A real entry
	// or a link the user re-pointed is left on disk and its stale record
	// dropped, so one replaced name cannot block cleaning the rest.
	for _, name := range sortedKeys(remainingLinks) {
		result, err := removeOwnedLink(out, agent, remainingLinks, name, remainingLinks[name])
		// Removed can arrive with an error (the link is gone, an empty aside
		// directory is not); the removal is real, so it is reported first.
		if result == link.Removed {
			removedCount++
			fmt.Fprintf(out, "%s: unlinked %s\n", agent.Name, name)
		}
		if err != nil {
			return fail(removedCount, err)
		}
	}

	// Only a copy whose marker names the recorded store path is ours; a
	// replaced entry is left and its record dropped.
	for _, name := range sortedKeys(remainingCopies) {
		result, err := removeOwnedCopy(out, agent, remainingCopies, name, remainingCopies[name])
		if err != nil {
			return fail(removedCount, err)
		}
		if result == link.Removed {
			removedCount++
			fmt.Fprintf(out, "%s: removed copy %s\n", agent.Name, name)
		}
	}

	delete(current.Targets, agent.Dir)
	return removedCount, nil
}

func runStatus(ctx commandContext) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	storeFlag := flags.String("store", "", "override the canonical skills store (default $XDG_DATA_HOME/skills)")
	agentHomeFlag := flags.String("agent-home", "", "re-root every agent's skills dir from $HOME into this directory")
	if code, done := parseFlags(flags, ctx); done {
		return code
	}
	resolved, ok := resolvePaths(ctx)
	if !ok {
		return exitError
	}
	storeDir, cfg, warnings, err := applyOverrides(resolved.storeDir, resolved.cfg, *storeFlag, *agentHomeFlag)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	for _, warning := range warnings {
		fmt.Fprintf(ctx.stderr, "sm: warning: %s\n", warning)
	}
	// Re-rooted agent dirs never match the state keys of the real ones, so
	// orphans are only meaningful against the configured directories.
	orphanScope := *agentHomeFlag == ""
	if err := doStatus(cfg, storeDir, resolved.statePath, orphanScope, ctx.stdout); err != nil {
		fmt.Fprintf(ctx.stderr, "sm: status failed: %s\n", pathutil.PrintableLines(err.Error()))
		return exitError
	}
	return exitOK
}

func runClean(ctx commandContext) int {
	flags := flag.NewFlagSet("clean", flag.ContinueOnError)
	only := flags.String("agent", "", "clean only the named agent")
	agentHomeFlag := flags.String("agent-home", "", "clean the agent skills re-rooted under this directory")
	orphans := flags.Bool("orphans", false, "clean the state targets no agent in agents.toml maps any more")
	if code, done := parseFlags(flags, ctx); done {
		return code
	}
	if *orphans && (*only != "" || *agentHomeFlag != "") {
		fmt.Fprintln(ctx.stderr, "sm: clean: --orphans cannot be combined with --agent or --agent-home")
		return exitUsage
	}
	resolved, ok := resolvePaths(ctx)
	if !ok {
		return exitError
	}
	_, cfg, aliases, err := overrideAgents(resolved.storeDir, resolved.cfg, "", *agentHomeFlag)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	for _, warning := range aliasWarnings(aliases) {
		fmt.Fprintf(ctx.stderr, "sm: warning: %s\n", warning)
	}
	for _, alias := range aliases {
		if alias.name == *only {
			fmt.Fprintf(ctx.stderr, "sm: clean failed: agent %s shares a directory with agent %s; use --agent %s\n", alias.name, alias.owner, alias.owner)
			return exitError
		}
	}
	if err := tightenStateDir(resolved.statePath); err != nil {
		fmt.Fprintf(ctx.stderr, "sm: %v\n", err)
		return exitError
	}
	summary, err := doClean(cfg, resolved.statePath, *only, *orphans, ctx.stderr)
	if err != nil {
		fmt.Fprintf(ctx.stderr, "sm: clean failed: %s\n", pathutil.PrintableLines(err.Error()))
		return exitError
	}
	fmt.Fprintln(ctx.stdout, summary)
	return exitOK
}
