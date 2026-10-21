package ingest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/pathutil"
)

// WriteResult reports the outcome of WriteSkill.
//
// Written is true only when the skill was actually written to disk. Skipped
// is non-empty only when the skill was not written because the target already
// exists and force was not set. In dry-run, a would-write yields
// Written=false with Skipped empty, which distinguishes it from a would-skip
// (Written=false with Skipped set).
//
// Report lists source entries the copy deliberately did not reproduce
// (special files, absolute or escaping symlinks, reserved names) in Skipped,
// and any replaced previous version that could not be deleted in Leftovers;
// the caller surfaces each as a warning.
type WriteResult struct {
	Written bool
	Skipped string
	Report  link.CopyReport
}

// OverlapError is WriteSkill's refusal of a target that equals, contains, or
// lies inside the skill's source (or whose existing referent does). It names
// one skill only, so a caller can skip that skill and carry on: replacing such
// a target would delete the source itself.
type OverlapError struct {
	Target string
	Source string
}

func (overlap *OverlapError) Error() string {
	return fmt.Sprintf("refusing to write %s: overlaps source %s", overlap.Target, overlap.Source)
}

// WriteSkill writes skill as destDir/<skill.Name>, creating destDir if
// needed. A target that equals, contains, or lies inside the source is refused
// with an *OverlapError before anything is touched, even with force or dryRun:
// replacing such a target would delete the source itself. An existing target
// is left untouched unless force is set, in which case it is replaced by a
// swap: the new copy is fully built before the old one is moved aside, and the
// old one is restored if placing the new one fails. With dryRun nothing on
// disk is changed. The written skill carries no provenance marker: destDir is
// a neutral store.
func WriteSkill(skill SourceSkill, destDir string, force, dryRun bool) (WriteResult, error) {
	// The name becomes a path component; reject anything that could escape destDir.
	if !pathutil.IsSingleSegment(skill.Name) {
		return WriteResult{}, fmt.Errorf("unsafe skill name %q", skill.Name)
	}
	absoluteDestination, err := filepath.Abs(destDir)
	if err != nil {
		return WriteResult{}, fmt.Errorf("resolve %s: %w", destDir, err)
	}
	// Only the parent is resolved: the entry itself is what gets skipped or
	// replaced, so a symlink at destDir/name is swapped out as an entry and
	// never followed into whatever it points at.
	resolvedDestination, err := pathutil.ResolveExisting(absoluteDestination)
	if err != nil {
		return WriteResult{}, err
	}
	target := filepath.Join(resolvedDestination, skill.Name)
	source, err := filepath.EvalSymlinks(skill.Dir)
	if err != nil {
		return WriteResult{}, fmt.Errorf("resolve source %s: %w", skill.Dir, err)
	}
	if pathutil.Overlaps(target, source) {
		return WriteResult{}, &OverlapError{Target: target, Source: source}
	}

	_, statErr := os.Lstat(target)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return WriteResult{}, fmt.Errorf("cannot inspect %s: %w", target, statErr)
	}
	if exists {
		// A replaced symlink never touches its referent, but a referent that
		// holds the source is still refused so the intent is never ambiguous.
		referent, err := filepath.EvalSymlinks(target)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return WriteResult{}, fmt.Errorf("resolve %s: %w", target, err)
		}
		if err == nil && pathutil.Overlaps(referent, source) {
			return WriteResult{}, &OverlapError{Target: referent, Source: source}
		}
	}

	if exists && !force {
		return WriteResult{Skipped: filepath.Join(destDir, skill.Name) + " exists; use --force to overwrite"}, nil
	}
	if dryRun {
		return WriteResult{}, nil
	}

	if err := link.CheckTargetDir(resolvedDestination); err != nil {
		return WriteResult{}, err
	}
	copyFunction := link.CopyDir
	if exists {
		copyFunction = link.ReplaceDir
	}
	// The source is pinned to the directory discovery validated, and the
	// copy must keep a regular SKILL.md: a skill without its manifest is never
	// placed in the store.
	report, err := copyFunction(skill.Dir, skill.Identity, target, link.CopyOptions{RequireSkillFile: true})
	if err != nil {
		return WriteResult{}, fmt.Errorf("cannot write %s: %w", target, err)
	}
	return WriteResult{Written: true, Report: report}, nil
}
