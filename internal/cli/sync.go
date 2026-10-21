package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/state"
	"github.com/gherlein/sm/internal/store"
)

const (
	// stateLockHolder names the tools that share the state lock in its
	// contention message.
	stateLockHolder = state.StateLockHolder

	// noAgentsMessage is the whole sync output when agents.toml enables
	// nothing; a bare "sync:" would read as a silent no-op instead of a
	// configuration gap.
	noAgentsMessage = "no agents enabled in agents.toml"
)

// afterEnumerate runs once the store has been enumerated and validated, before
// anything is copied or linked. It is a test seam: tests swap a store entry
// here to prove a skill replaced after validation is never copied.
// Production code never reassigns it.
var afterEnumerate = func(listing store.Listing) {}

// syncOptions are the sync flags. AllowEmpty lets a store with no skills at
// all prune every entry sm recorded, which is otherwise refused.
type syncOptions struct {
	CopyMode   bool
	DryRun     bool
	Verbose    bool
	AllowEmpty bool
}

// syncCounts is one agent's per-sync tally. repointed is used in symlink mode
// only (a link re-aimed at a moved store skill); refreshed in copy mode only.
type syncCounts struct {
	created   int
	repointed int
	refreshed int
	removed   int
}

// format renders one agent's summary term. Link mode adds "~R" only when a
// link was repointed so the everyday summary keeps its familiar "+N -M" shape;
// copy mode always shows "~R" because every kept copy is refreshed.
func (counts syncCounts) format(agentName string, copyMode bool) string {
	if copyMode {
		return fmt.Sprintf("%s +%d ~%d -%d", agentName, counts.created, counts.refreshed, counts.removed)
	}
	term := fmt.Sprintf("%s +%d -%d", agentName, counts.created, counts.removed)
	if counts.repointed > 0 {
		term += fmt.Sprintf(" ~%d", counts.repointed)
	}
	return term
}

// doSync fans the store out into every agent directory, as symlinks or, when
// opts.CopyMode is set, as real copied directories. A missing store aborts
// before any agent is touched: treating it as "no skills" would reconcile away
// every entry sm ever created. Outside a dry run the state lock is held from
// before state is read until after it is saved.
//
// A copy that fails for one skill (an unreadable source file, say) does not
// stop the run: the other skills and agents are still processed, state is
// saved, and both the summary and the joined per-skill errors are returned.
// Any other error (probe, identity, state, lock) stops the run and returns an
// empty summary.
func doSync(cfg agents.Config, storeDir, statePath string, opts syncOptions, log io.Writer) (summary string, err error) {
	if len(cfg.Agents) == 0 {
		return noAgentsMessage, nil
	}
	listing, err := store.Enumerate(storeDir)
	if err != nil {
		fmt.Fprintf(log, "warning: %v; no changes made\n", err)
		return "", err
	}
	if err := store.RequireNotWorldWritable(storeDir); err != nil {
		return "", err
	}
	afterEnumerate(listing)
	for _, warning := range listing.Warnings {
		fmt.Fprintf(log, "warning: %s\n", warning)
	}

	if !opts.DryRun {
		unlock, lockErr := state.Lock(statePath, stateLockHolder)
		if lockErr != nil {
			return "", lockErr
		}
		// The named results are assigned so a failed unlock is never lost.
		defer func() {
			if unlockErr := unlock(); unlockErr != nil {
				summary, err = "", errors.Join(err, unlockErr)
			}
		}()
	}

	current, err := state.Load(statePath)
	if err != nil {
		return "", err
	}
	// Adoption runs before the empty-store guard so records only an old
	// spelling held are counted by it; otherwise an empty store would see no
	// records, let the run through, and prune every adopted entry.
	adoptedAny := false
	for _, agent := range cfg.Agents {
		adopted, adoptErr := adoptAliasRecords(&current, agent, opts, log)
		if adopted > 0 {
			adoptedAny = true
		}
		if adoptErr != nil {
			return "", agentError(current, agent, statePath, opts.DryRun, adoptErr)
		}
	}
	if !opts.AllowEmpty {
		if err := refuseEmptyStore(cfg, current, listing, storeDir, opts.CopyMode); err != nil {
			// Adoption already said it happened, so it is kept; a refusal
			// with nothing adopted leaves the state file byte-identical.
			if adoptedAny && !opts.DryRun {
				return "", persistThenFail(current, statePath, err)
			}
			return "", err
		}
	}

	desired := make(map[string]string, len(listing.Skills))
	// pins ties each copy to the directory and manifest enumeration validated.
	pins := make(map[string]link.StorePin, len(listing.Skills))
	for _, skill := range listing.Skills {
		desired[skill.Name] = skill.Dir
		pins[skill.Name] = link.StorePin{Dir: skill.Identity, Manifest: skill.ManifestIdentity}
	}
	unusable := make(map[string]bool, len(listing.Unusable))
	for _, name := range listing.Unusable {
		unusable[name] = true
	}

	var parts []string
	var skillErrors []error
	for _, agent := range cfg.Agents {
		var counts syncCounts
		var applyErr error
		if opts.CopyMode {
			var agentSkillErrors []error
			counts, agentSkillErrors, applyErr = applyCopyTarget(&current, agent, desired, pins, unusable, statePath, opts, log)
			skillErrors = append(skillErrors, agentSkillErrors...)
		} else {
			counts, applyErr = applyLinkTarget(&current, agent, desired, unusable, statePath, opts, log)
		}
		if applyErr != nil {
			return "", errors.Join(append(skillErrors, withSyncHint(agent, applyErr))...)
		}
		parts = append(parts, counts.format(agent.Name, opts.CopyMode))
	}

	if !opts.DryRun {
		if err := current.Save(statePath); err != nil {
			return "", errors.Join(append(skillErrors, err)...)
		}
	}
	return "sync: " + strings.Join(parts, "   "), errors.Join(skillErrors...)
}

// withSyncHint adds, to a refusal of an unsafe agent directory, that the run
// stopped there and how to get past it; the fix itself is in the refusal.
func withSyncHint(agent agents.Agent, err error) error {
	var unsafe *link.UnsafeDirError
	if !errors.As(err, &unsafe) {
		return err
	}
	return fmt.Errorf("%w; sync stops at this agent: fix the directory, or remove agent %s from agents.toml", err, agent.Name)
}

// adoptAliasRecords moves into the agent's own state key the records other
// keys hold for the same real directory, the ones made under an earlier
// spelling of it (for example through a symlinked parent). Only the ledger
// this mode reconciles is adopted, and only records whose entry verifies by
// identity at the agent's directory (adoptVerifiedRecords), so an entry the
// user replaced is never claimed. Keys are compared by resolved path, never by
// prefix, so a different directory is never an alias. Keys are visited in
// sorted order, so when two aliases record one name the first one that
// verifies wins and the other keeps its record for clean --orphans. An alias
// key left with no records is dropped. A dry run makes the same moves in
// memory, so its plan and counts match a real run, and never saves them.
func adoptAliasRecords(current *state.State, agent agents.Agent, opts syncOptions, log io.Writer) (int, error) {
	realDir, err := pathutil.ResolveExisting(agent.Dir)
	if err != nil {
		return 0, err
	}
	verify := recordVerifier(linkVerifies)
	if opts.CopyMode {
		verify = link.IsOwnedCopy
	}
	verb := "adopted"
	if opts.DryRun {
		verb = "would adopt"
	}
	total := 0
	for _, aliasDir := range sortedTargetDirs(*current) {
		if aliasDir == agent.Dir {
			continue
		}
		aliasRealDir, err := pathutil.ResolveExisting(aliasDir)
		// A key that cannot be resolved cannot be shown to be this directory;
		// status reports it as unclassifiable and sync leaves it alone.
		if err != nil || aliasRealDir != realDir {
			continue
		}
		alias := current.Targets[aliasDir]
		aliasLedger := alias.Links
		if opts.CopyMode {
			aliasLedger = alias.Copies
		}
		if len(aliasLedger) == 0 {
			continue
		}
		ownerLedger := ownLedger(current, agent.Dir, opts.CopyMode)
		adopted, err := adoptVerifiedRecords(agent.Dir, aliasLedger, ownerLedger, verify)
		if adopted > 0 {
			total += adopted
			fmt.Fprintf(log, "%s %d record(s) from %s into %s\n", verb, adopted, aliasDir, agent.Dir)
			if len(alias.Links) == 0 && len(alias.Copies) == 0 {
				delete(current.Targets, aliasDir)
			}
		}
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// ownLedger returns the agent key's ledger for this mode, creating it (and
// the key) when absent so adopted records have somewhere to go.
func ownLedger(current *state.State, dir string, copyMode bool) map[string]string {
	target := current.Targets[dir]
	if copyMode {
		if target.Copies == nil {
			target.Copies = map[string]string{}
		}
		current.Targets[dir] = target
		return target.Copies
	}
	if target.Links == nil {
		target.Links = map[string]string{}
	}
	current.Targets[dir] = target
	return target.Links
}

// refuseEmptyStore stops a sync that would prune every recorded entry because
// the store came back with nothing in it at all. An unmounted volume or a
// mistyped --store looks exactly like that, and the damage is the whole agent
// setup. Unusable entries alone do not prove this is the real store (a
// mistyped --store full of unrelated directories has plenty, and one of them
// can share a recorded name by chance); an agent is spared the refusal only
// when EVERY one of its recorded names is among them, which shows the real
// store with a temporary problem, and those records are all kept by
// plannable, so nothing is pruned. Only the ledger this mode reconciles
// matters, since the other mode's entries are never pruned by this sync.
func refuseEmptyStore(cfg agents.Config, current state.State, listing store.Listing, storeDir string, copyMode bool) error {
	if len(listing.Skills) > 0 {
		return nil
	}
	unusable := make(map[string]bool, len(listing.Unusable))
	for _, name := range listing.Unusable {
		unusable[name] = true
	}
	var affected []string
	for _, agent := range cfg.Agents {
		target := current.Targets[agent.Dir]
		ledger := target.Links
		if copyMode {
			ledger = target.Copies
		}
		if len(ledger) == 0 || recordsOnly(ledger, unusable) {
			continue
		}
		affected = append(affected, fmt.Sprintf("%s (%d)", agent.Name, len(ledger)))
	}
	if len(affected) == 0 {
		return nil
	}
	return fmt.Errorf("store %s has no skills, but sm recorded entries for %s; refusing to remove them (pass --allow-empty if the store is meant to be empty)",
		storeDir, strings.Join(affected, ", "))
}

// recordsOnly reports whether every name the ledger records is in names.
func recordsOnly(ledger map[string]string, names map[string]bool) bool {
	for name := range ledger {
		if !names[name] {
			return false
		}
	}
	return true
}

// plannable returns the recorded entries the planner may act on. A recorded
// name whose store entry exists but is unusable (unreadable, non-conforming,
// a dangling store symlink) is not a removed skill: it is withheld from the
// planner so its entry is neither removed nor disowned, and its record stays.
func plannable(log io.Writer, agent agents.Agent, recorded map[string]string, unusable map[string]bool) map[string]string {
	planned := make(map[string]string, len(recorded))
	for _, name := range sortedKeys(recorded) {
		if unusable[name] {
			fmt.Fprintf(log, "warning: %s: keeping %s: its store entry is unusable (see the store warning)\n", agent.Name, filepath.Join(agent.Dir, name))
			continue
		}
		planned[name] = recorded[name]
	}
	return planned
}

// setLinks / setCopies write one ledger for an agent dir while preserving the
// other, so a copy sync never erases recorded symlinks and vice versa.
func setLinks(s *state.State, dir string, links map[string]string) {
	target := s.Targets[dir]
	target.Links = links
	s.Targets[dir] = target
}

func setCopies(s *state.State, dir string, copies map[string]string) {
	target := s.Targets[dir]
	target.Copies = copies
	s.Targets[dir] = target
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// warnUnmanaged reports a desired name that is present on disk but not in the
// ledger this sync owns. If the other ledger owns it, the entry was made by sm
// in the other mode, so the message says how to switch rather than implying sm
// is a stranger to it.
func warnUnmanaged(log io.Writer, agent agents.Agent, name string, otherLedger map[string]string, otherMode, thisMode string) {
	full := filepath.Join(agent.Dir, name)
	if _, ok := otherLedger[name]; ok {
		fmt.Fprintf(log, "warning: %s: %s is managed by sm in %s mode; run 'sm clean' to switch this directory to %s mode; skipping\n",
			agent.Name, full, otherMode, thisMode)
		return
	}
	fmt.Fprintf(log, "warning: %s: %s exists and is not managed by sm; skipping\n", agent.Name, full)
}

// warnLost reports a recorded name whose entry sm no longer owns; its record
// is dropped and the entry is left exactly as the user made it.
func warnLost(log io.Writer, agent agents.Agent, name, kind string) {
	fmt.Fprintf(log, "warning: %s: %s is no longer an sm %s; leaving it in place\n", agent.Name, filepath.Join(agent.Dir, name), kind)
}

// warnPlan reports every name the plan leaves alone. A desired name whose
// recorded entry the user replaced is both Lost and Unmanaged; it gets only
// the Lost message, which is the one that explains what happened.
func warnPlan(log io.Writer, agent agents.Agent, plan state.TargetPlan, otherLedger map[string]string, otherMode, thisMode, kind string) {
	lost := make(map[string]bool, len(plan.Lost))
	for _, name := range plan.Lost {
		lost[name] = true
	}
	for _, name := range plan.Unmanaged {
		if !lost[name] {
			warnUnmanaged(log, agent, name, otherLedger, otherMode, thisMode)
		}
	}
	for _, name := range plan.Lost {
		warnLost(log, agent, name, kind)
	}
}

// agentError wraps cause with the agent name. Outside a dry run it first saves
// the progress made so far (earlier agents included) so state keeps matching
// the filesystem; a dry run never writes state.
func agentError(current state.State, agent agents.Agent, statePath string, dryRun bool, cause error) error {
	err := fmt.Errorf("agent %s: %w", agent.Name, cause)
	if dryRun {
		return err
	}
	return persistThenFail(current, statePath, err)
}

// planLinks plans one agent directory in symlink mode. A recorded link is
// ours only while its raw readlink still equals the recorded target; a user
// who re-pointed or replaced it now owns it. The first probe error (for
// example a permission failure) aborts the plan: an entry sm cannot inspect
// must never be treated as absent.
func planLinks(agent agents.Agent, desired, recorded map[string]string) (state.TargetPlan, error) {
	var probeErr error
	plan := state.PlanTarget(desired, recorded, func(name string) state.EntryInfo {
		if probeErr != nil {
			return state.EntryInfo{}
		}
		entry, err := link.Probe(agent.Dir, name)
		if err != nil {
			probeErr = err
			return state.EntryInfo{}
		}
		recordedTarget, isRecorded := recorded[name]
		return state.EntryInfo{
			Exists: entry.Exists,
			Ours:   isRecorded && entry.IsSymlink && entry.Target == recordedTarget,
		}
	})
	return plan, probeErr
}

// planCopies plans one agent directory in copy mode. A recorded copy is ours
// only while its marker names the recorded store path.
func planCopies(agent agents.Agent, desired, recorded map[string]string) (state.TargetPlan, error) {
	var probeErr error
	plan := state.PlanTarget(desired, recorded, func(name string) state.EntryInfo {
		if probeErr != nil {
			return state.EntryInfo{}
		}
		entry, err := link.Probe(agent.Dir, name)
		if err != nil {
			probeErr = err
			return state.EntryInfo{}
		}
		recordedSource, isRecorded := recorded[name]
		if !entry.Exists || !isRecorded {
			return state.EntryInfo{Exists: entry.Exists}
		}
		owned, err := link.IsOwnedCopy(agent.Dir, name, recordedSource)
		if err != nil {
			probeErr = err
			return state.EntryInfo{}
		}
		return state.EntryInfo{Exists: true, Ours: owned}
	})
	return plan, probeErr
}

// ownedAfterPlan starts the ledger this sync writes from the recorded entries
// minus the ones the plan found are no longer sm's. It includes the records
// plannable withheld, so those survive untouched.
func ownedAfterPlan(recorded map[string]string, plan state.TargetPlan) map[string]string {
	owned := make(map[string]string, len(recorded))
	for name, storePath := range recorded {
		owned[name] = storePath
	}
	for _, name := range plan.Lost {
		delete(owned, name)
	}
	return owned
}

// removeOwnedLink deletes a recorded link through RemoveIfTarget's identity
// check and updates owned to match the filesystem. RemoveIfTarget can report
// Removed together with an error (the link is gone but an empty private
// directory was left behind): the record goes because the link did, and the
// error is still returned. A link that is no longer sm's is left alone, its
// record dropped, and a warning logged.
func removeOwnedLink(log io.Writer, agent agents.Agent, owned map[string]string, name, recordedTarget string) (link.RemoveResult, error) {
	result, err := link.RemoveIfTarget(agent.Dir, name, recordedTarget)
	if err != nil {
		if result == link.Removed {
			delete(owned, name)
		}
		return result, err
	}
	delete(owned, name)
	if result == link.NotOurs {
		warnLost(log, agent, name, "link")
	}
	return result, nil
}

// applyLinkTarget reconciles one agent directory in symlink mode. It takes
// care to preserve the agent's Copies ledger when it rewrites state, and to
// name copy-mode entries clearly when they collide. Every removal is
// identity-checked against the recorded target by link.RemoveIfTarget.
func applyLinkTarget(current *state.State, agent agents.Agent, desired map[string]string, unusable map[string]bool, statePath string, opts syncOptions, log io.Writer) (syncCounts, error) {
	recorded := current.Targets[agent.Dir].Links
	otherLedger := current.Targets[agent.Dir].Copies
	plan, err := planLinks(agent, desired, plannable(log, agent, recorded, unusable))
	if err != nil {
		return syncCounts{}, agentError(*current, agent, statePath, opts.DryRun, err)
	}
	warnPlan(log, agent, plan, otherLedger, "copy", "symlink", "link")
	if opts.DryRun {
		return syncCounts{created: len(plan.Create), repointed: len(plan.Repoint), removed: len(plan.Remove)}, nil
	}

	owned := ownedAfterPlan(recorded, plan)
	setLinks(current, agent.Dir, owned)
	var counts syncCounts
	fail := func(cause error) (syncCounts, error) {
		return counts, persistThenFail(*current, statePath, fmt.Errorf("agent %s: %w", agent.Name, cause))
	}

	// A link planted in a directory another user can write to could be
	// swapped or shadowed by them, so symlink mode refuses the same
	// directories copy mode and ms do. Only a run that adds links needs it;
	// removals are identity-checked entry by entry.
	if len(plan.Create) > 0 || len(plan.Repoint) > 0 {
		if err := link.CheckTargetDir(agent.Dir); err != nil {
			return fail(err)
		}
	}

	for _, name := range sortedKeys(plan.Create) {
		if err := link.Create(agent.Dir, name, plan.Create[name]); err != nil {
			return fail(err)
		}
		owned[name] = plan.Create[name]
		counts.created++
		if opts.Verbose {
			fmt.Fprintf(log, "%s: linked %s\n", agent.Name, name)
		}
	}
	// Repoint is remove-then-create: the old link is deleted only while it
	// still points at the recorded target, and the record is dropped before
	// the new link is made so a failed create leaves state matching disk. A
	// link that vanished since planning is simply recreated.
	for _, name := range sortedKeys(plan.Repoint) {
		result, err := removeOwnedLink(log, agent, owned, name, recorded[name])
		if err != nil {
			return fail(err)
		}
		if result == link.NotOurs {
			continue
		}
		if err := link.Create(agent.Dir, name, plan.Repoint[name]); err != nil {
			return fail(err)
		}
		owned[name] = plan.Repoint[name]
		counts.repointed++
		if opts.Verbose {
			fmt.Fprintf(log, "%s: relinked %s\n", agent.Name, name)
		}
	}
	for _, name := range plan.Remove {
		result, err := removeOwnedLink(log, agent, owned, name, recorded[name])
		if err != nil {
			// Counts are intentionally discarded when sync returns an error;
			// what stays observable is the log line, and the record for a
			// Removed link is dropped by removeOwnedLink and persisted by the
			// failure path.
			logUnlinkedBeforeError(log, agent.Name, name, result)
			return fail(err)
		}
		tallyRemoval(&counts, log, agent.Name, name, result, opts.Verbose)
	}
	return counts, nil
}

// logUnlinkedBeforeError reports a real removal that arrived together with an
// error (the link is gone, an empty aside directory is not). It is logged even
// without --verbose because the failure is about to hide the summary.
func logUnlinkedBeforeError(log io.Writer, agentName, name string, result link.RemoveResult) {
	if result == link.Removed {
		fmt.Fprintf(log, "%s: unlinked %s\n", agentName, name)
	}
}

// tallyRemoval counts a reconciled removal. A link already gone (Absent) still
// had its record reconciled away, so counting it keeps the real run's numbers
// equal to the dry run's, but it is reported as a dropped record rather than
// as an unlink that did not happen. NotOurs is neither counted nor reported
// here because removeOwnedLink already warned.
func tallyRemoval(counts *syncCounts, log io.Writer, agentName, name string, result link.RemoveResult, verbose bool) {
	switch result {
	case link.Removed:
		counts.removed++
		if verbose {
			fmt.Fprintf(log, "%s: unlinked %s\n", agentName, name)
		}
	case link.Absent:
		counts.removed++
		if verbose {
			fmt.Fprintf(log, "%s: dropped record for %s (link already gone)\n", agentName, name)
		}
	}
}

// applyCopyTarget reconciles one agent directory in copy mode. Besides create
// and remove, it REFRESHES every copy still backed by the store (re-copying it)
// so copied skills track store edits the way a symlink would for free. Every
// mutation is guarded by provenance: sm only ever refreshes or removes a
// directory whose copy marker names the store path recorded in the ledger,
// never a hand-placed one or one with a forged marker.
//
// A create or refresh that fails for one skill is isolated: the error is
// returned in the second result and the remaining skills are still processed.
// A failed create leaves the ledger as it was (no copy was placed) and a
// failed refresh keeps the previous copy and its record (RefreshCopy only
// swaps on success), so state keeps matching the filesystem.
// Every other failure is returned as the third result and stops the run.
func applyCopyTarget(current *state.State, agent agents.Agent, desired map[string]string, pins map[string]link.StorePin, unusable map[string]bool, statePath string, opts syncOptions, log io.Writer) (syncCounts, []error, error) {
	recorded := current.Targets[agent.Dir].Copies
	otherLedger := current.Targets[agent.Dir].Links
	plan, err := planCopies(agent, desired, plannable(log, agent, recorded, unusable))
	if err != nil {
		return syncCounts{}, nil, agentError(*current, agent, statePath, opts.DryRun, err)
	}
	warnPlan(log, agent, plan, otherLedger, "symlink", "copy", "copy")

	// Every owned copy still backed by the store is refreshed, whether its
	// store path is unchanged (Keep) or moved (Repoint), so copied skills
	// track store edits the way a symlink would for free.
	refreshCandidates := append([]string{}, plan.Keep...)
	refreshCandidates = append(refreshCandidates, sortedKeys(plan.Repoint)...)
	sort.Strings(refreshCandidates)

	if opts.DryRun {
		return syncCounts{created: len(plan.Create), refreshed: len(refreshCandidates), removed: len(plan.Remove)}, nil, nil
	}
	var skillErrors []error
	fail := func(cause error) (syncCounts, []error, error) {
		return syncCounts{}, skillErrors, persistThenFail(*current, statePath, fmt.Errorf("agent %s: %w", agent.Name, cause))
	}
	skillFailed := func(name string, cause error) {
		skillErrors = append(skillErrors, fmt.Errorf("agent %s: %s: %w", agent.Name, name, cause))
	}

	// Clear any staging directory a crashed run orphaned before working. The
	// state lock is already held, so no stage here belongs to a live run.
	swept, sweepSkipped, err := link.SweepStages(agent.Dir)
	if err != nil {
		return fail(err)
	}
	warnLines(log, agent, sweepSkipped)
	if swept > 0 && opts.Verbose {
		fmt.Fprintf(log, "%s: swept %d orphaned stage dir(s)\n", agent.Name, swept)
	}

	// The directory itself is checked once, up front, so an unsafe agent
	// directory stops the agent instead of being reported as a failure of
	// every skill by the per-skill isolation below.
	if len(plan.Create) > 0 || len(refreshCandidates) > 0 {
		if err := link.CheckTargetDir(agent.Dir); err != nil {
			return fail(err)
		}
	}

	owned := ownedAfterPlan(recorded, plan)
	setCopies(current, agent.Dir, owned)

	var counts syncCounts
	// Create copies for names not present on disk (includes self-healing a
	// recorded copy a user deleted).
	for _, name := range sortedKeys(plan.Create) {
		report, err := link.CreateCopy(agent.Dir, name, plan.Create[name], pins[name])
		if err != nil {
			skillFailed(name, err)
			continue
		}
		warnCopyReport(log, agent, report)
		owned[name] = plan.Create[name]
		counts.created++
		if opts.Verbose {
			fmt.Fprintf(log, "%s: copied %s\n", agent.Name, name)
		}
	}

	for _, name := range refreshCandidates {
		ownedCopy, err := link.IsOwnedCopy(agent.Dir, name, recorded[name])
		if err != nil {
			return fail(err)
		}
		if !ownedCopy {
			warnLost(log, agent, name, "copy")
			delete(owned, name)
			continue
		}
		// RefreshCopy swaps the new copy in and only then deletes the old one,
		// so a failed copy leaves the previous copy (and its record) intact. An
		// old copy that could not be deleted comes back as a leftover; the new
		// copy is already in place, so the new store path is what is recorded.
		report, err := link.RefreshCopy(agent.Dir, name, desired[name], pins[name], recorded[name])
		if err != nil {
			skillFailed(name, err)
			continue
		}
		warnCopyReport(log, agent, report)
		owned[name] = desired[name]
		counts.refreshed++
		if opts.Verbose {
			fmt.Fprintf(log, "%s: refreshed %s\n", agent.Name, name)
		}
	}

	for _, name := range plan.Remove {
		result, err := removeOwnedCopy(log, agent, owned, name, recorded[name])
		if err != nil {
			return fail(err)
		}
		// A copy already gone still had its record reconciled away, so it is
		// counted like a removal: the dry run (which counts plan.Remove)
		// and the real run then print the same numbers.
		switch result {
		case link.Removed:
			counts.removed++
			if opts.Verbose {
				fmt.Fprintf(log, "%s: removed copy %s\n", agent.Name, name)
			}
		case link.Absent:
			counts.removed++
			if opts.Verbose {
				fmt.Fprintf(log, "%s: dropped record for %s (copy already gone)\n", agent.Name, name)
			}
		}
	}
	return counts, skillErrors, nil
}

// removeOwnedCopy deletes a recorded copy through RemoveCopy's identity check
// and updates owned to match. It reports link.Removed for a deleted copy,
// link.Absent for one already gone (its record is just dropped), and
// link.NotOurs for an entry that is no longer sm's copy, which is left alone,
// its record dropped, and a warning logged. A set-aside directory RemoveCopy
// could not delete is a warning, not an error, because the copy has already
// left its name.
func removeOwnedCopy(log io.Writer, agent agents.Agent, owned map[string]string, name, recordedSource string) (link.RemoveResult, error) {
	removed, leftovers, err := link.RemoveCopy(agent.Dir, name, recordedSource)
	if err != nil {
		return link.NotOurs, err
	}
	warnLines(log, agent, leftovers)
	result := link.Removed
	if !removed {
		entry, err := link.Probe(agent.Dir, name)
		if err != nil {
			return link.NotOurs, err
		}
		result = link.Absent
		if entry.Exists {
			result = link.NotOurs
			warnLost(log, agent, name, "copy")
		}
	}
	delete(owned, name)
	return result, nil
}

// warnCopyReport logs every source entry a copy deliberately did not
// reproduce, so a skill that silently lost a file is never mistaken for a
// faithful copy, and every previous version that could not be deleted, so the
// user can remove it by hand.
func warnCopyReport(log io.Writer, agent agents.Agent, report link.CopyReport) {
	warnLines(log, agent, report.Skipped)
	warnLines(log, agent, report.Leftovers)
}

func warnLines(log io.Writer, agent agents.Agent, lines []string) {
	for _, line := range lines {
		fmt.Fprintf(log, "warning: %s: %s\n", agent.Name, line)
	}
}

// persistThenFail saves the progress made before cause so state stays
// consistent with the filesystem, then returns cause.
func persistThenFail(current state.State, statePath string, cause error) error {
	if saveErr := current.Save(statePath); saveErr != nil {
		return errors.Join(cause, saveErr)
	}
	return cause
}
