// Package state records which entries sm created in each agent skills
// directory, plans identity-based reconciliation against the desired set, and
// serializes concurrent runs with a lock beside the state file.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/gherlein/sm/internal/pathutil"
)

const (
	stateVersion = 1
	// The state file lists every directory sm manages and the store paths
	// behind them; it is nobody else's business, so it and its directory are
	// private to the owner.
	stateDirMode  = 0o700
	stateFileMode = 0o600

	stateTempPattern = ".state-*.tmp"

	stateDirName  = "skills-mapper"
	stateFileName = "state.json"

	// lockSuffix names the lock file beside the state file; it is a separate
	// file because state.json is replaced by rename on every save, which would
	// silently drop a lock held on the old inode.
	lockSuffix   = ".lock"
	lockFileMode = 0o600
)

// StateLockHolder names the tools that take the state lock in its contention
// message: sm sync and sm clean hold it, and so does ms (so a sync never sees
// a store skill mid-swap), and the message cannot tell which one holds it.
const StateLockHolder = "sm or ms"

// Target holds what sm created in one agent directory. Links are symlink
// entries; Copies are directories sm materialized in copy mode. Both are keyed
// by skill name and valued by the store skill path. They are separate ledgers
// so each reconciles independently and neither mode disturbs the other's
// entries. Copies is omitempty so state written by a symlink-only setup is
// byte-identical to before copy mode existed.
type Target struct {
	Links  map[string]string `json:"links"`
	Copies map[string]string `json:"copies,omitempty"`
}

// State is the central record, keyed by absolute agent directory.
type State struct {
	Version   int               `json:"version"`
	Targets   map[string]Target `json:"targets"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// DefaultPath returns $XDG_DATA_HOME/skills-mapper/state.json. It lives here
// rather than in sm's command line because ms must find the same file to take
// its lock. The data home is resolved by pathutil.DataHome, like the store's.
func DefaultPath() (string, error) {
	dataHome, err := pathutil.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, stateDirName, stateFileName), nil
}

// Load reads the state file. A missing file yields an empty usable State so
// the first run needs no bootstrap step. Any version other than stateVersion
// (including an absent version or a null document) is an error: a file written
// by a newer release must never be silently downgraded and rewritten, and one
// without a version is not a file sm wrote.
func Load(path string) (State, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{Version: stateVersion, Targets: map[string]Target{}}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read state %s: %w", path, err)
	}
	var loaded State
	if err := json.Unmarshal(content, &loaded); err != nil {
		return State{}, fmt.Errorf("parse state %s: %w", path, err)
	}
	if loaded.Version != stateVersion {
		return State{}, fmt.Errorf("state %s: unsupported state version %d (this sm reads version %d); refusing to use or overwrite it", path, loaded.Version, stateVersion)
	}
	// Save always writes a targets object (possibly empty), so an absent or
	// null one is not a file sm wrote; treating it as empty would silently
	// drop every provenance record and let the next save erase them.
	if loaded.Targets == nil {
		return State{}, fmt.Errorf("state %s: version %d document has no targets object; refusing to use or overwrite it", path, stateVersion)
	}
	if err := validateTargets(loaded.Targets); err != nil {
		return State{}, fmt.Errorf("state %s: %w; refusing to use or overwrite it", path, err)
	}
	return loaded, nil
}

// validateTargets rejects records sm could never have written and must never
// act on: every removal joins a recorded name to its target directory and
// compares the entry against the recorded value, so a relative directory, a
// name that is not a single path segment, or an empty value would make sm act
// outside the directory or fail on that record forever.
func validateTargets(targets map[string]Target) error {
	for dir, target := range targets {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("target directory %q is not absolute", dir)
		}
		// sm only ever writes cleaned keys; a ".." segment or a redundant
		// separator would make the key name a different directory than it
		// appears to, and removals would act there.
		if filepath.Clean(dir) != dir {
			return fmt.Errorf("target directory %q is not a clean path", dir)
		}
		for ledger, records := range map[string]map[string]string{"links": target.Links, "copies": target.Copies} {
			for name, recorded := range records {
				if !pathutil.IsSingleSegment(name) {
					return fmt.Errorf("target %s: invalid recorded name %q in %s: must be a single path segment", dir, name, ledger)
				}
				if recorded == "" {
					return fmt.Errorf("target %s: empty recorded store path for %q in %s", dir, name, ledger)
				}
			}
		}
	}
	return nil
}

// Save writes the state, creating parent directories. It stamps Version and
// UpdatedAt on the written copy.
func (s State) Save(path string) error {
	s.Version = stateVersion
	s.UpdatedAt = time.Now().UTC()
	if s.Targets == nil {
		s.Targets = map[string]Target{}
	}
	content, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), stateDirMode); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}
	// The temp file lives beside the target so the rename stays on one
	// filesystem and is atomic; a crash can then never leave a truncated
	// state.json, which would wedge Load and orphan every recorded link.
	temp, err := os.CreateTemp(filepath.Dir(path), stateTempPattern)
	if err != nil {
		return fmt.Errorf("create temp state for %s: %w", path, err)
	}
	tempPath := temp.Name()
	if _, err := temp.Write(content); err != nil {
		return errors.Join(fmt.Errorf("write temp state %s: %w", tempPath, err), temp.Close(), os.Remove(tempPath))
	}
	if err := temp.Close(); err != nil {
		return errors.Join(fmt.Errorf("close temp state %s: %w", tempPath, err), os.Remove(tempPath))
	}
	// CreateTemp already uses 0600; setting it explicitly keeps the private
	// mode a stated property rather than an implementation detail of CreateTemp.
	if err := os.Chmod(tempPath, stateFileMode); err != nil {
		return errors.Join(fmt.Errorf("chmod temp state %s: %w", tempPath, err), os.Remove(tempPath))
	}
	if err := os.Rename(tempPath, path); err != nil {
		return errors.Join(fmt.Errorf("replace state %s: %w", path, err), os.Remove(tempPath))
	}
	return nil
}

// EntryInfo is what the planner needs to know about the on-disk entry at a
// name. Ours is computed by the caller from identity, never from type alone: a
// symlink is ours only if its readlink equals the recorded target, a copy only
// if its marker names the recorded store path. Ours is meaningful only for a
// recorded name.
type EntryInfo struct {
	Exists bool
	Ours   bool
}

// TargetPlan is the set of actions needed to reconcile one agent directory.
// Create and Repoint map a name to the desired store path. Keep lists owned
// entries already pointing at the desired path. Remove lists recorded names no
// longer desired whose entry is ours or already gone. Unmanaged lists desired
// names blocked by an entry sm does not own. Lost lists recorded names whose
// entry is no longer ours: their record must be dropped and the entry left
// alone. A desired name can be in both Unmanaged and Lost.
type TargetPlan struct {
	Create    map[string]string
	Repoint   map[string]string
	Keep      []string
	Remove    []string
	Unmanaged []string
	Lost      []string
}

// PlanTarget is pure: all filesystem knowledge arrives through inspect, which
// is called once per desired or recorded name. An entry sm does not own is
// never scheduled for any change, which is what protects hand-placed skills,
// real directories, and links the user re-pointed from being overwritten or
// removed.
func PlanTarget(desired, recorded map[string]string, inspect func(name string) EntryInfo) TargetPlan {
	plan := TargetPlan{Create: map[string]string{}, Repoint: map[string]string{}}

	for name, storePath := range desired {
		entry := inspect(name)
		recordedPath, isRecorded := recorded[name]
		switch {
		case !entry.Exists:
			plan.Create[name] = storePath
		case !isRecorded:
			plan.Unmanaged = append(plan.Unmanaged, name)
		case !entry.Ours:
			plan.Unmanaged = append(plan.Unmanaged, name)
			plan.Lost = append(plan.Lost, name)
		case recordedPath == storePath:
			plan.Keep = append(plan.Keep, name)
		default:
			plan.Repoint[name] = storePath
		}
	}

	for name := range recorded {
		if _, stillDesired := desired[name]; stillDesired {
			continue
		}
		entry := inspect(name)
		if entry.Exists && !entry.Ours {
			plan.Lost = append(plan.Lost, name)
			continue
		}
		plan.Remove = append(plan.Remove, name)
	}

	sort.Strings(plan.Keep)
	sort.Strings(plan.Remove)
	sort.Strings(plan.Unmanaged)
	sort.Strings(plan.Lost)
	return plan
}
