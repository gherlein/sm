// Package link performs the filesystem work for sm and ms: symlinks and copied
// skill directories. Every delete is identity-checked (a symlink only while its
// readlink equals the recorded target, a copy only while it is owned by the
// current user and its marker names the recorded store path) so a user's own
// entries are never removed, and every copy is written through an os.Root
// opened on a private random staging directory so a planted symlink cannot
// redirect it.
package link

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/skillmeta"
)

const targetDirMode fs.FileMode = 0o755

// copyMarker is written at the root of every directory sm materializes in copy
// mode. Its content is the store path the copy came from, which is also what
// the state ledger records; a copy is sm's only when the two match, so neither
// a hand-placed directory nor a forged marker can make sm clobber or delete it.
const copyMarker = ".sm-copy"

// copyMarkerMode is the marker file's permission: readable, owner-writable.
const copyMarkerMode fs.FileMode = 0o644

// markerReadCap bounds how much of a marker is read. A store path is far
// shorter; anything larger is not a marker sm wrote.
const markerReadCap = 4096

// stagePrefix and stagePattern name the random staging directory a copy is
// built in. It lives beside the final path so the rename stays on one
// filesystem, and its name is unpredictable so it cannot be pre-planted.
const (
	stagePrefix  = ".sm-stage-"
	stagePattern = stagePrefix + "*"
)

// stageSentinel is written first into every stage. SweepStages removes only
// directories carrying it, so an unrelated entry that merely matches the name
// pattern is never deleted.
const stageSentinel = ".sm-stage-sentinel"

// stageSentinelMode keeps the sentinel private to its owner.
const stageSentinelMode fs.FileMode = 0o600

// skillFileName is the manifest every copied skill must carry.
const skillFileName = "SKILL.md"

// asidePattern names the private directory an existing entry is moved into
// while it is replaced or removed. After a crash it may hold user data, so
// nothing ever sweeps it automatically.
const asidePattern = ".sm-old-*"

const (
	// groupOtherWrite is stripped from everything copied so a copy never
	// becomes writable by other users.
	groupOtherWrite fs.FileMode = 0o022
	// ownerAll keeps copied directories traversable and writable by the owner
	// so a restrictive source mode cannot make the stage unwritable mid-copy.
	ownerAll fs.FileMode = 0o700
	// worldWrite marks a target directory any user could plant entries in.
	worldWrite fs.FileMode = 0o002
)

// renameStageIntoPlace is the final rename of a finished stage. It is a
// variable only so tests can inject a failure at the one step that is
// otherwise impossible to make fail without also breaking the restore.
var renameStageIntoPlace = os.Rename

// Test seams. Each runs at a point where another process could swap an entry
// between a check and the action that relies on it; tests replace one to make
// that swap happen deterministically. Production code never reassigns them.
var (
	// beforeMoveAside runs after an entry passed its identity check and
	// before it is renamed into the aside directory.
	beforeMoveAside = func(entryPath string) {}
	// beforeStageOpen runs after a stage is created and before it is opened.
	beforeStageOpen = func(stagePath string) {}
	// afterStageBuilt runs after a stage is filled and before it is verified
	// and renamed into place.
	afterStageBuilt = func(stagePath string) {}
	// beforePrivateDirDelete runs after a private directory was verified and
	// before its content is deleted.
	beforePrivateDirDelete = func(path string) {}
	// beforeOwnedCopyOpen runs after a copy entry was inspected and before it
	// is opened to read its marker.
	beforeOwnedCopyOpen = func(entryPath string) {}
	// copyOwnerCheck decides whether a copy entry and its marker belong to the
	// current user; tests substitute it because a foreign-owned directory
	// cannot be created without root.
	copyOwnerCheck = ownedByCurrentUser
)

// CopyReport lists, one human-readable line each, the source entries a copy
// deliberately did not reproduce (special files, absolute or escaping
// symlinks, reserved names at the skill root) in Skipped, and in Leftovers any
// previous version that was replaced but could not be deleted afterwards. A
// leftover is not an error: the new copy is already in place and must be
// recorded as such.
type CopyReport struct {
	Skipped   []string
	Leftovers []string
}

// skip records a source entry the copy did not reproduce. Names and link
// targets come from the source tree, which may be third-party content, so the
// line is made terminal-safe here, once, for every caller that prints it.
func (report *CopyReport) skip(sourcePath, reason string) {
	report.Skipped = append(report.Skipped, pathutil.Printable(fmt.Sprintf("skipped %s: %s", sourcePath, reason)))
}

// leftoverLine describes a set-aside directory sm could not delete.
func leftoverLine(asideDir string, cause error) string {
	return pathutil.Printable(fmt.Sprintf("previous version kept at %s; remove it manually (%v)", asideDir, cause))
}

// CopyOptions tunes CopyDir and ReplaceDir.
type CopyOptions struct {
	// RequireSkillFile fails the copy, before anything is placed, unless the
	// copied tree has a regular SKILL.md (following symlinks that stay inside
	// the copy): a skill without its manifest is not a skill.
	RequireSkillFile bool
}

// StorePin is what store enumeration recorded about a skill it validated;
// sm's agent copies are made only from exactly these files.
type StorePin struct {
	// Dir is the os.Stat of the skill directory (its real directory).
	Dir fs.FileInfo
	// Manifest is the os.FileInfo of the SKILL.md file validation read (the
	// target when SKILL.md is a symlink); a dereferenced manifest must be
	// this same file.
	Manifest fs.FileInfo
}

// copyPolicy is how one tree is copied.
type copyPolicy struct {
	// dereferenceSkillFile copies a root SKILL.md that is a symlink as a
	// regular file with the link's content. sm's agent copies use it: the
	// store is the user's own validated data, and a copied link that leaves
	// the skill would otherwise be dropped and leave a skill without a
	// manifest.
	dereferenceSkillFile bool
	// pinnedManifest is the validated manifest a dereferenced SKILL.md must
	// be.
	pinnedManifest   fs.FileInfo
	requireSkillFile bool
	// provenance, when not empty, is the store path written into the copy's
	// marker; a neutral copy (ms into the store) carries no marker.
	provenance string
}

// validateName requires a single clean path segment so a name can never
// escape targetDir.
func validateName(name string) error {
	if !pathutil.IsSingleSegment(name) {
		return fmt.Errorf("invalid link name %q: must be a single path segment", name)
	}
	return nil
}

// Create ensures targetDir exists and symlinks targetDir/name to storePath.
func Create(targetDir, name, storePath string) error {
	if err := validateName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(targetDir, targetDirMode); err != nil {
		return fmt.Errorf("create target dir %s: %w", targetDir, err)
	}
	linkPath := filepath.Join(targetDir, name)
	if err := os.Symlink(storePath, linkPath); err != nil {
		return fmt.Errorf("create link %s: %w", linkPath, err)
	}
	return nil
}

// Entry describes what is at targetDir/name without following symlinks.
// Target is the raw readlink string of a symlink (empty otherwise): identity
// is that exact string, so it is never cleaned or resolved, and a dangling
// link still reports what it points at.
type Entry struct {
	Exists    bool
	IsSymlink bool
	IsDir     bool
	Target    string
}

// Probe reports the Entry at targetDir/name.
func Probe(targetDir, name string) (Entry, error) {
	if err := validateName(name); err != nil {
		return Entry{}, err
	}
	entryPath := filepath.Join(targetDir, name)
	info, err := os.Lstat(entryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return Entry{}, nil
	}
	if err != nil {
		return Entry{}, fmt.Errorf("inspect %s: %w", entryPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return Entry{Exists: true, IsDir: info.IsDir()}, nil
	}
	target, err := os.Readlink(entryPath)
	if err != nil {
		return Entry{}, fmt.Errorf("read link %s: %w", entryPath, err)
	}
	return Entry{Exists: true, IsSymlink: true, Target: target}, nil
}

// RemoveResult is the outcome of RemoveIfTarget.
type RemoveResult int

const (
	// Removed: the entry was sm's symlink and is gone.
	Removed RemoveResult = iota + 1
	// Absent: nothing was at the name.
	Absent
	// NotOurs: something else is at the name; it was left untouched.
	NotOurs
)

func (result RemoveResult) String() string {
	switch result {
	case Removed:
		return "Removed"
	case Absent:
		return "Absent"
	case NotOurs:
		return "NotOurs"
	default:
		return fmt.Sprintf("RemoveResult(%d)", int(result))
	}
}

// symlinkTargetIs reports whether path is a symlink whose raw readlink equals
// wantTarget; absent reports whether nothing is there.
func symlinkTargetIs(path, wantTarget string) (matches, absent bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, true, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, false, nil
	}
	target, err := os.Readlink(path)
	if err != nil {
		return false, false, fmt.Errorf("read link %s: %w", path, err)
	}
	return target == wantTarget, false, nil
}

// RemoveIfTarget deletes targetDir/name only when it is a symlink whose raw
// readlink equals wantTarget (the target sm recorded when it made the link).
// Being a symlink is not enough: a user may have replaced sm's link with one
// of their own. Like RemoveCopy, the link is moved into a private aside
// directory and checked again there before it is deleted, so an entry swapped
// in between the check and the delete is restored instead of removed.
func RemoveIfTarget(targetDir, name, wantTarget string) (RemoveResult, error) {
	if err := validateName(name); err != nil {
		return 0, err
	}
	if wantTarget == "" {
		return 0, errors.New("remove link: recorded target must not be empty")
	}
	if err := checkExistingTargetDir(targetDir); err != nil {
		return 0, err
	}
	linkPath := filepath.Join(targetDir, name)
	matches, absent, err := symlinkTargetIs(linkPath, wantTarget)
	if err != nil {
		return 0, err
	}
	if absent {
		return Absent, nil
	}
	if !matches {
		return NotOurs, nil
	}
	aside, err := moveAside(linkPath)
	if err != nil {
		return 0, err
	}
	held := filepath.Join(aside.path, name)
	stillMatches, _, err := symlinkTargetIs(held, wantTarget)
	if err != nil {
		return 0, errors.Join(err, aside.restore(linkPath))
	}
	if !stillMatches {
		if err := aside.restore(linkPath); err != nil {
			return 0, err
		}
		return NotOurs, nil
	}
	if err := os.Remove(held); err != nil {
		return 0, errors.Join(fmt.Errorf("remove link %s: %w", linkPath, err), aside.restore(linkPath))
	}
	// The link is already gone; a failure here only leaves an empty private
	// directory behind, which the error names so it can be removed by hand.
	if err := aside.removeEmpty(); err != nil {
		return Removed, fmt.Errorf("link %s removed, but its empty aside directory remains: %w", linkPath, err)
	}
	return Removed, nil
}

// CheckTargetDir creates dir if needed and refuses it unless it is a directory
// (a symlinked directory is judged by its target) owned by the current user
// and not world-writable: another user able to add entries there could race
// sm's writes or plant entries for it to trust.
func CheckTargetDir(dir string) error {
	if err := os.MkdirAll(dir, targetDirMode); err != nil {
		return fmt.Errorf("create target dir %s: %w", dir, err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("inspect target dir %s: %w", dir, err)
	}
	return judgeTargetDir(dir, info)
}

// checkExistingTargetDir is CheckTargetDir without creating dir. Removals and
// sweeps use it: they must not create a directory, a missing one holds nothing
// to remove, and in a directory another user can write to, the entries they
// would delete could have been swapped in by that user.
func checkExistingTargetDir(dir string) error {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect target dir %s: %w", dir, err)
	}
	return judgeTargetDir(dir, info)
}

// judgeTargetDir applies the target directory rules to dir's (followed) info.
func judgeTargetDir(dir string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("refusing target dir %s: not a directory", dir)
	}
	owned, err := ownedByCurrentUser(info)
	if err != nil {
		return fmt.Errorf("inspect target dir %s: %w", dir, err)
	}
	if !owned {
		return &UnsafeDirError{Dir: dir, Problem: "not owned by the current user", Hint: "the directory must be owned by you"}
	}
	if info.Mode().Perm()&worldWrite != 0 {
		return &UnsafeDirError{Dir: dir, Problem: "world-writable", Hint: "fix with: chmod o-w " + pathutil.ShellQuote(dir)}
	}
	return nil
}

// UnsafeDirError is the refusal of a target directory another user could
// write to or owns. It is a type so commands can add their own advice (such
// as how to carry on with other agents) to the fix Hint gives.
type UnsafeDirError struct {
	Dir     string
	Problem string
	Hint    string
}

func (unsafe *UnsafeDirError) Error() string {
	return fmt.Sprintf("refusing target dir %s: %s (%s)", unsafe.Dir, unsafe.Problem, unsafe.Hint)
}

// CopyDir copies the src tree (symlinks in src itself resolved first) to dst as
// a plain directory with no provenance marker. dst must not exist. srcIdentity
// is the os.Stat of src taken when src was validated; the copy is refused if
// src no longer resolves to that same directory, so a skill swapped for a link
// to another directory after validation is never copied. The copy is built in
// a private stage and renamed into place, so a partial copy never appears at
// dst. A dst that equals, contains or lies inside the source is refused.
func CopyDir(src string, srcIdentity fs.FileInfo, dst string, options CopyOptions) (CopyReport, error) {
	return copySource(src, srcIdentity, dst, false, nil, copyPolicy{requireSkillFile: options.RequireSkillFile})
}

// ReplaceDir is CopyDir for a dst that may already exist. The new tree is
// fully built before the old entry is touched; the old entry is moved aside,
// restored if placing the new tree fails, and deleted only once the new tree
// is in place, so a failed copy never loses it.
func ReplaceDir(src string, srcIdentity fs.FileInfo, dst string, options CopyOptions) (CopyReport, error) {
	return copySource(src, srcIdentity, dst, true, nil, copyPolicy{requireSkillFile: options.RequireSkillFile})
}

// CreateCopy materializes storePath at targetDir/name with a provenance marker
// recording storePath. pin is what enumeration recorded for storePath: the
// copy is made only from that directory (see CopyDir), and a root SKILL.md
// that is a symlink is copied as a regular file with its content only when it
// still leads to the manifest that was validated. targetDir must pass
// CheckTargetDir and targetDir/name must not exist.
func CreateCopy(targetDir, name, storePath string, pin StorePin) (CopyReport, error) {
	if err := validateName(name); err != nil {
		return CopyReport{}, err
	}
	if storePath == "" {
		return CopyReport{}, errors.New("create copy: store path must not be empty")
	}
	if err := CheckTargetDir(targetDir); err != nil {
		return CopyReport{}, err
	}
	return copySource(storePath, pin.Dir, filepath.Join(targetDir, name), false, nil, agentCopyPolicy(storePath, pin))
}

// RefreshCopy replaces the sm copy at targetDir/name with a fresh copy of
// storePath (pinned to pin, see CreateCopy). The entry must be an sm
// copy of recordedSource (the ledger value); it is checked again after being
// moved aside, and it is deleted only once the new copy is in place.
func RefreshCopy(targetDir, name, storePath string, pin StorePin, recordedSource string) (CopyReport, error) {
	if storePath == "" {
		return CopyReport{}, errors.New("refresh copy: store path must not be empty")
	}
	owned, err := IsOwnedCopy(targetDir, name, recordedSource)
	if err != nil {
		return CopyReport{}, err
	}
	entryPath := filepath.Join(targetDir, name)
	if !owned {
		return CopyReport{}, fmt.Errorf("refusing to refresh %s: not an sm copy of %s", entryPath, recordedSource)
	}
	if err := CheckTargetDir(targetDir); err != nil {
		return CopyReport{}, err
	}
	verifyOld := func(asideDir string) error {
		stillOwned, err := IsOwnedCopy(asideDir, name, recordedSource)
		if err != nil {
			return err
		}
		if !stillOwned {
			return fmt.Errorf("refusing to refresh %s: it changed and is no longer an sm copy of %s", entryPath, recordedSource)
		}
		return nil
	}
	return copySource(storePath, pin.Dir, entryPath, true, verifyOld, agentCopyPolicy(storePath, pin))
}

// agentCopyPolicy is how sm copies a store skill into an agent directory.
func agentCopyPolicy(storePath string, pin StorePin) copyPolicy {
	return copyPolicy{dereferenceSkillFile: true, pinnedManifest: pin.Manifest, requireSkillFile: true, provenance: storePath}
}

// copySource opens src pinned to its validated identity, refuses a dst that
// overlaps it, and builds dst from the pinned handle through stageAndSwap.
// The source is read only through that handle, so swapping src after this
// point cannot change what is copied.
func copySource(src string, srcIdentity fs.FileInfo, dst string, replace bool, verifyOld func(asideDir string) error, policy copyPolicy) (report CopyReport, err error) {
	srcRoot, realSrc, err := openPinnedSource(src, srcIdentity)
	if err != nil {
		return CopyReport{}, err
	}
	defer func() {
		if closeErr := srcRoot.Close(); closeErr != nil {
			report, err = CopyReport{}, errors.Join(err, fmt.Errorf("close source %s: %w", realSrc, closeErr))
		}
	}()
	if err := refuseOverlap(realSrc, dst); err != nil {
		return CopyReport{}, err
	}
	build := func(stage *os.Root) error {
		if err := copyTree(srcRoot, realSrc, stage, policy, &report); err != nil {
			return err
		}
		if policy.requireSkillFile {
			if err := requireRegularSkillFile(stage, realSrc); err != nil {
				return err
			}
		}
		if policy.provenance != "" {
			// The marker is written inside the stage so a copy is never
			// visible at its final path without it.
			if err := writeNewFile(stage, copyMarker, []byte(policy.provenance+"\n"), copyMarkerMode); err != nil {
				return fmt.Errorf("write provenance marker: %w", err)
			}
		}
		return nil
	}
	if err := stageAndSwap(dst, replace, verifyOld, &report, build); err != nil {
		return CopyReport{}, err
	}
	return report, nil
}

// openPinnedSource resolves src, opens the result as an os.Root, and requires
// the opened directory to be the very one identity describes. Validation
// (enumeration or discovery) happened earlier by path; between then and now a
// writer could have replaced src with a link to any directory, and only the
// open handle can prove it did not.
func openPinnedSource(src string, identity fs.FileInfo) (*os.Root, string, error) {
	if identity == nil {
		return nil, "", fmt.Errorf("copy %s: the source's validated identity is required", src)
	}
	realSrc, err := filepath.EvalSymlinks(src)
	if err != nil {
		return nil, "", fmt.Errorf("resolve source %s: %w", src, err)
	}
	srcRoot, err := os.OpenRoot(realSrc)
	if err != nil {
		return nil, "", fmt.Errorf("open source %s: %w", realSrc, err)
	}
	opened, err := srcRoot.Stat(".")
	if err != nil {
		return nil, "", errors.Join(fmt.Errorf("inspect source %s: %w", realSrc, err), srcRoot.Close())
	}
	if !opened.IsDir() || !os.SameFile(opened, identity) {
		return nil, "", errors.Join(fmt.Errorf("source changed since it was validated: %s", src), srcRoot.Close())
	}
	return srcRoot, realSrc, nil
}

// refuseOverlap fails when dst equals, contains or lies inside realSrc. A
// destination inside the source would be copied into itself through its own
// stage without end, and one containing it would be replaced together with
// the source. Only dst's parent is resolved: dst itself is an entry that is
// created or replaced, never followed.
func refuseOverlap(realSrc, dst string) error {
	absoluteDst, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", dst, err)
	}
	resolvedParent, err := pathutil.ResolveExisting(filepath.Dir(absoluteDst))
	if err != nil {
		return err
	}
	resolvedDst := filepath.Join(resolvedParent, filepath.Base(absoluteDst))
	if pathutil.Overlaps(realSrc, resolvedDst) {
		return fmt.Errorf("refusing to copy %s to %s: the destination overlaps the source", realSrc, resolvedDst)
	}
	return nil
}

// requireRegularSkillFile fails unless the stage has a SKILL.md that is, or
// links inside the stage to, a regular file.
func requireRegularSkillFile(stage *os.Root, realSrc string) error {
	info, err := stage.Stat(skillFileName)
	if err != nil {
		return fmt.Errorf("the copy of %s has no regular %s: %w", realSrc, skillFileName, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("the copy of %s has no regular %s (found %v)", realSrc, skillFileName, info.Mode().Type())
	}
	return nil
}

// IsOwnedCopy reports whether targetDir/name is a copy sm made of
// recordedSource: a real directory (not a symlink) whose .sm-copy marker is a
// regular file whose trimmed content equals recordedSource. Markers written by
// earlier releases (exactly storePath + "\n") match. Every other shape is
// false; I/O failures are errors.
func IsOwnedCopy(targetDir, name, recordedSource string) (owned bool, err error) {
	if err := validateName(name); err != nil {
		return false, err
	}
	if recordedSource == "" {
		return false, errors.New("ownership check: recorded source must not be empty")
	}
	entryPath := filepath.Join(targetDir, name)
	info, err := os.Lstat(entryPath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", entryPath, err)
	}
	if !info.IsDir() {
		return false, nil
	}
	// The marker's content is world-readable, so anyone could forge one; only
	// a directory and marker the current user owns can be sm's copy.
	ownedDir, err := copyOwnerCheck(info)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", entryPath, err)
	}
	if !ownedDir {
		return false, nil
	}
	beforeOwnedCopyOpen(entryPath)
	entryRoot, err := os.OpenRoot(entryPath)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", entryPath, err)
	}
	defer func() {
		if closeErr := entryRoot.Close(); closeErr != nil {
			owned, err = false, errors.Join(err, fmt.Errorf("close %s: %w", entryPath, closeErr))
		}
	}()
	// The entry could have been swapped for a symlink after the Lstat; the
	// root must be the very directory that was inspected.
	opened, err := entryRoot.Stat(".")
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", entryPath, err)
	}
	if !os.SameFile(opened, info) {
		return false, nil
	}
	content, found, err := readMarker(entryRoot)
	if err != nil {
		return false, fmt.Errorf("read marker in %s: %w", entryPath, err)
	}
	if !found {
		return false, nil
	}
	return strings.TrimSpace(content) == recordedSource, nil
}

// readMarker returns the marker's content when it is a regular file of at
// most markerReadCap bytes; found is false for any other shape.
func readMarker(entryRoot *os.Root) (content string, found bool, err error) {
	info, err := entryRoot.Lstat(copyMarker)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !info.Mode().IsRegular() || info.Size() > markerReadCap {
		return "", false, nil
	}
	ownedMarker, err := copyOwnerCheck(info)
	if err != nil {
		return "", false, err
	}
	if !ownedMarker {
		return "", false, nil
	}
	file, err := entryRoot.OpenFile(copyMarker, readOnlyNoBlockFlags, 0)
	if err != nil {
		return "", false, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			content, found, err = "", false, errors.Join(err, closeErr)
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return "", false, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(opened, info) {
		return "", false, nil
	}
	data, err := io.ReadAll(io.LimitReader(file, markerReadCap+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > markerReadCap {
		return "", false, nil
	}
	return string(data), true, nil
}

// RemoveCopy deletes targetDir/name only when it is an sm copy of
// recordedSource. The entry is first moved into a private directory and its
// identity re-checked there, so an entry swapped in after the first check is
// restored rather than deleted. removed is false with no error when the entry
// is absent or not an sm copy. Once the verified copy has left its name it
// counts as removed: if deleting the set-aside directory then fails, that is
// returned as a leftover line, not an error, so callers drop the record.
func RemoveCopy(targetDir, name, recordedSource string) (removed bool, leftovers []string, err error) {
	if err := checkExistingTargetDir(targetDir); err != nil {
		return false, nil, err
	}
	owned, err := IsOwnedCopy(targetDir, name, recordedSource)
	if err != nil {
		return false, nil, err
	}
	if !owned {
		return false, nil, nil
	}
	entryPath := filepath.Join(targetDir, name)
	aside, err := moveAside(entryPath)
	if err != nil {
		return false, nil, err
	}
	stillOwned, err := IsOwnedCopy(aside.path, name, recordedSource)
	if err == nil && !stillOwned {
		err = fmt.Errorf("refusing to remove %s: it changed and is no longer an sm copy of %s", entryPath, recordedSource)
	}
	if err != nil {
		return false, nil, errors.Join(err, aside.restore(entryPath))
	}
	if err := aside.removeAll(); err != nil {
		return true, []string{leftoverLine(aside.path, err)}, nil
	}
	return true, nil, nil
}

// SweepStages removes staging directories a crash between staging and rename
// orphaned in targetDir. Only entries that are provably sm stages are removed:
// named .sm-stage-*, a real directory (not a symlink), owned by the current
// user, and carrying a regular sentinel file. Set-aside .sm-old-* directories
// and pre-hardening <name>.sm-stage entries are never touched. A missing
// targetDir is a no-op. Housekeeping must not block a sync, so an entry that
// vanishes mid-sweep is ignored and one that cannot be read or removed for
// lack of permission is returned as a skipped line instead of an error.
func SweepStages(targetDir string) (removed int, skipped []string, err error) {
	if err := checkExistingTargetDir(targetDir); err != nil {
		return 0, nil, err
	}
	entries, err := os.ReadDir(targetDir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("sweep stages in %s: %w", targetDir, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), stagePrefix) {
			continue
		}
		stagePath := filepath.Join(targetDir, entry.Name())
		stage, skipReason, err := orphanedStage(stagePath)
		if err != nil {
			return removed, skipped, err
		}
		if skipReason != "" {
			skipped = append(skipped, skipReason)
			continue
		}
		if stage == nil {
			continue
		}
		removeErr := stage.removeAll()
		switch {
		case removeErr == nil:
			removed++
		case errors.Is(removeErr, fs.ErrNotExist):
		case errors.Is(removeErr, fs.ErrPermission):
			// Stage names come from whoever can write the directory, so the
			// line is made terminal-safe.
			skipped = append(skipped, pathutil.Printable(fmt.Sprintf("could not remove orphaned stage %s: %v", stagePath, removeErr)))
		default:
			return removed, skipped, fmt.Errorf("remove stage: %w", removeErr)
		}
	}
	return removed, skipped, nil
}

// orphanedStage returns path as a privateDir when it is an sm stage, nil when
// it is anything else or has vanished, and a non-empty skipReason when it
// cannot be inspected for lack of permission.
func orphanedStage(path string) (stage *privateDir, skipReason string, err error) {
	// unreadable turns a permission failure into a skip and a vanished entry
	// into "not a stage"; anything else stays an error.
	unreadable := func(inspectErr error) (*privateDir, string, error) {
		if errors.Is(inspectErr, fs.ErrNotExist) {
			return nil, "", nil
		}
		if errors.Is(inspectErr, fs.ErrPermission) {
			return nil, pathutil.Printable(fmt.Sprintf("could not inspect possible stage %s: %v", path, inspectErr)), nil
		}
		return nil, "", fmt.Errorf("inspect %s: %w", path, inspectErr)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return unreadable(err)
	}
	if !info.IsDir() {
		return nil, "", nil
	}
	owned, err := ownedByCurrentUser(info)
	if err != nil {
		return nil, "", fmt.Errorf("inspect %s: %w", path, err)
	}
	if !owned {
		return nil, "", nil
	}
	stageRoot, err := os.OpenRoot(path)
	if err != nil {
		return unreadable(err)
	}
	defer func() {
		if closeErr := stageRoot.Close(); closeErr != nil {
			stage, skipReason, err = nil, "", errors.Join(err, fmt.Errorf("close %s: %w", path, closeErr))
		}
	}()
	opened, err := stageRoot.Stat(".")
	if err != nil {
		return unreadable(err)
	}
	if !os.SameFile(opened, info) {
		return nil, "", nil
	}
	sentinel, err := stageRoot.Lstat(stageSentinel)
	if err != nil {
		return unreadable(err)
	}
	if !sentinel.Mode().IsRegular() {
		return nil, "", nil
	}
	return &privateDir{path: path, info: info}, "", nil
}

// privateDir is a directory sm created with os.MkdirTemp, remembered by
// identity so it is only ever removed while it is still that directory.
type privateDir struct {
	path string
	info fs.FileInfo
}

func newPrivateDir(parent, pattern string) (*privateDir, error) {
	path, err := os.MkdirTemp(parent, pattern)
	if err != nil {
		return nil, fmt.Errorf("create private dir in %s: %w", parent, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", path, err)
	}
	if err := requireOwnedDirectory(path, info); err != nil {
		return nil, fmt.Errorf("%w; leaving it in place", err)
	}
	return &privateDir{path: path, info: info}, nil
}

// requireOwnedDirectory refuses anything but a real directory owned by the
// current user. A freshly created private directory that fails this was
// swapped by someone else, and nothing must be written into it.
func requireOwnedDirectory(path string, info fs.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("%s is not the directory sm just created", path)
	}
	owned, err := ownedByCurrentUser(info)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !owned {
		return fmt.Errorf("%s is not owned by the current user", path)
	}
	return nil
}

// verify fails unless path still names the directory that was created.
func (dir *privateDir) verify() error {
	info, err := os.Lstat(dir.path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir.path, err)
	}
	if !info.IsDir() || !os.SameFile(info, dir.info) {
		return fmt.Errorf("%s is no longer the directory sm created; leaving it in place", dir.path)
	}
	return nil
}

// openVerified opens the directory as an os.Root and confirms, by path and
// then through the handle, that it is still the directory sm created. Work done
// through the returned root stays inside that directory even if its name is
// swapped afterwards.
func (dir *privateDir) openVerified() (*os.Root, error) {
	if err := dir.verify(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir.path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir.path, err)
	}
	opened, err := root.Stat(".")
	if err != nil {
		return nil, errors.Join(fmt.Errorf("inspect %s: %w", dir.path, err), root.Close())
	}
	if !os.SameFile(opened, dir.info) {
		return nil, errors.Join(fmt.Errorf("%s is no longer the directory sm created; leaving it in place", dir.path), root.Close())
	}
	return root, nil
}

// removeAll deletes the directory's content through a handle on the verified
// directory, so a directory swapped in under its name after the check is never
// recursed into, and then removes the emptied directory itself (removeEmpty,
// which checks its identity again).
func (dir *privateDir) removeAll() (err error) {
	root, err := dir.openVerified()
	if err != nil {
		return err
	}
	beforePrivateDirDelete(dir.path)
	contentErr := removeChildren(root)
	if closeErr := root.Close(); closeErr != nil {
		contentErr = errors.Join(contentErr, closeErr)
	}
	if contentErr != nil {
		return fmt.Errorf("remove %s: %w", dir.path, contentErr)
	}
	return dir.removeEmpty()
}

// removeChildren deletes every entry inside root, confined to root.
func removeChildren(root *os.Root) error {
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := root.RemoveAll(entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// removeEmpty deletes the directory only if it is still sm's and empty; it can
// never take content with it. The name is looked up and removed through a
// handle on the parent, so a swapped ancestor cannot redirect the removal; the
// one remaining window (the name swapped between this check and the rmdir)
// can at most remove an EMPTY directory, because rmdir refuses any other.
func (dir *privateDir) removeEmpty() (err error) {
	parentPath := filepath.Dir(dir.path)
	parent, err := os.OpenRoot(parentPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", parentPath, err)
	}
	defer func() {
		if closeErr := parent.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %s: %w", parentPath, closeErr))
		}
	}()
	name := filepath.Base(dir.path)
	info, err := parent.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir.path, err)
	}
	if !info.IsDir() || !os.SameFile(info, dir.info) {
		return fmt.Errorf("%s is no longer the directory sm created; leaving it in place", dir.path)
	}
	if err := parent.Remove(name); err != nil {
		return fmt.Errorf("remove %s: %w", dir.path, err)
	}
	return nil
}

// moveAside renames entryPath into a fresh private .sm-old-* directory beside
// it, keeping its base name. Inside the 0o700 aside directory no other user can
// swap it, and any entry type (directory, file, symlink) can be moved.
func moveAside(entryPath string) (*privateDir, error) {
	aside, err := newPrivateDir(filepath.Dir(entryPath), asidePattern)
	if err != nil {
		return nil, err
	}
	beforeMoveAside(entryPath)
	if err := os.Rename(entryPath, filepath.Join(aside.path, filepath.Base(entryPath))); err != nil {
		return nil, errors.Join(fmt.Errorf("move %s aside: %w", entryPath, err), aside.removeEmpty())
	}
	return aside, nil
}

// restore moves the set-aside entry back to entryPath and drops the then-empty
// aside directory. If the move back fails, the aside directory is kept and
// named in the error so the user's data can be recovered by hand.
func (dir *privateDir) restore(entryPath string) error {
	held := filepath.Join(dir.path, filepath.Base(entryPath))
	if err := os.Rename(held, entryPath); err != nil {
		return fmt.Errorf("restore %s: %w; the previous content is preserved at %s", entryPath, err, held)
	}
	return dir.removeEmpty()
}

// requireAbsent fails unless nothing exists at path.
func requireAbsent(path string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	return fmt.Errorf("%s already exists", path)
}

// stageAndSwap builds dst in a random private stage beside it and renames the
// stage into place. All writes go through an os.Root opened on the stage, and
// the stage must still be the directory that was created when it is renamed.
// With replace, an existing dst is moved aside only after the stage is
// complete, checked with verifyOld (when non-nil), restored if placing the
// stage fails, and deleted only after the new tree is in place (a failed
// delete is added to report.Leftovers). Without replace, dst must not exist.
// The stage is removed on every failure.
func stageAndSwap(dst string, replace bool, verifyOld func(asideDir string) error, report *CopyReport, build func(stage *os.Root) error) (err error) {
	if !replace {
		if err := requireAbsent(dst); err != nil {
			return err
		}
	}
	stage, err := newPrivateDir(filepath.Dir(dst), stagePattern)
	if err != nil {
		return fmt.Errorf("stage %s: %w", dst, err)
	}
	placed := false
	defer func() {
		if placed {
			return
		}
		if cleanupErr := stage.removeAll(); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()

	if err := populateStage(stage, build); err != nil {
		return err
	}
	afterStageBuilt(stage.path)
	if err := stage.verify(); err != nil {
		return err
	}

	var aside *privateDir
	if replace {
		aside, err = moveAsideIfPresent(dst)
		if err != nil {
			return err
		}
		if aside != nil && verifyOld != nil {
			if err := verifyOld(aside.path); err != nil {
				return errors.Join(err, aside.restore(dst))
			}
		}
	} else if err := requireAbsent(dst); err != nil {
		// Checked again because rename silently replaces an empty directory
		// created after the first check.
		return err
	}

	if err := renameStageIntoPlace(stage.path, dst); err != nil {
		placeErr := fmt.Errorf("place copy %s: %w", dst, err)
		if aside != nil {
			return errors.Join(placeErr, aside.restore(dst))
		}
		return placeErr
	}
	placed = true
	// The new tree is already at dst, so failing here would make callers keep
	// recording the old source while dst's marker names the new one; the
	// undeletable previous version is reported as a leftover instead.
	if aside != nil {
		if err := aside.removeAll(); err != nil {
			report.Leftovers = append(report.Leftovers, leftoverLine(aside.path, err))
		}
	}
	return nil
}

// moveAsideIfPresent moves dst aside, or returns nil when dst does not exist.
func moveAsideIfPresent(dst string) (*privateDir, error) {
	if _, err := os.Lstat(dst); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", dst, err)
	}
	return moveAside(dst)
}

// populateStage opens the stage as an os.Root, confirms it is the directory
// that was created, writes the sentinel first (so a crash leaves a sweepable
// stage), runs build, and removes the sentinel again.
func populateStage(stage *privateDir, build func(stage *os.Root) error) (err error) {
	beforeStageOpen(stage.path)
	stageRoot, err := os.OpenRoot(stage.path)
	if err != nil {
		return fmt.Errorf("open stage %s: %w", stage.path, err)
	}
	defer func() {
		if closeErr := stageRoot.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close stage %s: %w", stage.path, closeErr))
		}
	}()
	opened, err := stageRoot.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect stage %s: %w", stage.path, err)
	}
	if !os.SameFile(opened, stage.info) {
		return fmt.Errorf("stage %s was replaced before use", stage.path)
	}
	if err := writeNewFile(stageRoot, stageSentinel, nil, stageSentinelMode); err != nil {
		return fmt.Errorf("write stage sentinel: %w", err)
	}
	if err := build(stageRoot); err != nil {
		return err
	}
	if err := stageRoot.Remove(stageSentinel); err != nil {
		return fmt.Errorf("remove stage sentinel: %w", err)
	}
	return nil
}

// writeNewFile creates name inside root exclusively (never through an existing
// entry) and writes content.
func writeNewFile(root *os.Root, name string, content []byte, mode fs.FileMode) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	return errors.Join(writeErr, file.Close())
}

// copiedDirMode keeps a directory owner-usable and strips group/other write
// and every special bit.
func copiedDirMode(mode fs.FileMode) fs.FileMode {
	return (mode.Perm() | ownerAll) &^ groupOtherWrite
}

// copiedFileMode strips group/other write and every special bit (Perm drops
// setuid, setgid and sticky).
func copiedFileMode(mode fs.FileMode) fs.FileMode {
	return mode.Perm() &^ groupOtherWrite
}

// copyTree copies the source open as srcRoot (realSrc is its path, used in
// messages) into stage. Source entries are read through srcRoot and written
// through stage, so a swap on either side cannot reach outside. Directories
// are created with Mkdir and files with O_EXCL, so a name that already exists
// (for instance a case-folded twin of a symlink copied earlier) fails loudly
// instead of being written through. Special files, absolute or escaping
// symlinks, and the reserved root names are skipped and reported. With
// policy.dereferenceSkillFile a root SKILL.md symlink is copied as the regular
// file it points to. The stage root takes the source root's mode last, so the
// stage stays private while it is being filled.
func copyTree(srcRoot *os.Root, realSrc string, stage *os.Root, policy copyPolicy, report *CopyReport) error {
	var rootMode fs.FileMode
	walkErr := fs.WalkDir(srcRoot.FS(), ".", func(slashRel string, entry fs.DirEntry, walkErr error) error {
		sourcePath := filepath.Join(realSrc, filepath.FromSlash(slashRel))
		if walkErr != nil {
			return fmt.Errorf("read %s: %w", sourcePath, walkErr)
		}
		if slashRel == "." {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect %s: %w", sourcePath, err)
			}
			rootMode = copiedDirMode(info.Mode())
			return nil
		}
		rel := filepath.FromSlash(slashRel)
		if filepath.Dir(rel) == "." && IsReservedRootName(entry.Name()) {
			report.skip(sourcePath, "reserved sm name at the skill root")
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case entry.IsDir():
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect %s: %w", sourcePath, err)
			}
			if err := stage.Mkdir(rel, copiedDirMode(info.Mode())); err != nil {
				return fmt.Errorf("copy %s: %w", sourcePath, err)
			}
			return nil
		case entry.Type()&fs.ModeSymlink != 0 && policy.dereferenceSkillFile && rel == skillFileName:
			return copyDereferencedSkillFile(srcRoot, realSrc, stage, sourcePath, policy.pinnedManifest)
		case entry.Type()&fs.ModeSymlink != 0:
			return copySymlink(srcRoot, stage, rel, sourcePath, report)
		case entry.Type().IsRegular():
			return copyFile(srcRoot, stage, rel, sourcePath, report)
		default:
			report.skip(sourcePath, specialFileReason(entry.Type()))
			return nil
		}
	})
	if walkErr != nil {
		return walkErr
	}
	if err := stage.Chmod(".", rootMode); err != nil {
		return fmt.Errorf("set mode of copied %s: %w", realSrc, err)
	}
	return nil
}

// IsReservedRootName reports whether a skill-root entry named name is never
// copied (it would collide with sm's marker or stage sentinel). ms discovery
// uses it to refuse a manifest the copy would drop. The comparison ignores
// case because on a case-folding filesystem ".SM-COPY" would occupy the
// marker's name and make its O_EXCL write fail.
func IsReservedRootName(name string) bool {
	return strings.EqualFold(name, copyMarker) || strings.EqualFold(name, stageSentinel)
}

func specialFileReason(mode fs.FileMode) string {
	switch {
	case mode&fs.ModeNamedPipe != 0:
		return "special file (named pipe)"
	case mode&fs.ModeSocket != 0:
		return "special file (socket)"
	case mode&fs.ModeDevice != 0:
		return "special file (device)"
	default:
		return fmt.Sprintf("special file (mode %v)", mode)
	}
}

// copySymlink recreates a symlink only when it is relative and provably stays
// inside the skill; otherwise it is skipped and reported.
func copySymlink(srcRoot, stage *os.Root, rel, sourcePath string, report *CopyReport) error {
	target, err := srcRoot.Readlink(rel)
	if err != nil {
		return fmt.Errorf("read link %s: %w", sourcePath, err)
	}
	if filepath.IsAbs(target) {
		report.skip(sourcePath, "absolute symlink -> "+target)
		return nil
	}
	if !LinkStaysInside(rel, target) {
		report.skip(sourcePath, "symlink escapes the skill -> "+target)
		return nil
	}
	if err := stage.Symlink(target, rel); err != nil {
		return fmt.Errorf("copy link %s: %w", sourcePath, err)
	}
	return nil
}

// LinkStaysInside reports whether a symlink at rel (relative to the skill
// root) whose target is target resolves inside the skill; an absolute target
// never does. ms uses it to judge a SKILL.md link before ingesting. The
// lexical check (Clean(join(dir(rel), target)) does not
// climb out) is not enough on its own: the kernel resolves each component
// before applying a later "..", so "self/.." climbs out when self links to
// ".". Therefore ".." is accepted only as a leading run, where it walks up
// through real copied directories that the lexical check already bounds.
func LinkStaysInside(rel, target string) bool {
	if filepath.IsAbs(target) {
		return false
	}
	seenName := false
	for _, part := range strings.Split(filepath.ToSlash(target), "/") {
		switch part {
		case "", ".":
		case "..":
			if seenName {
				return false
			}
		default:
			seenName = true
		}
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(rel), target))
	return resolved != ".." && !strings.HasPrefix(resolved, ".."+string(filepath.Separator))
}

// copyDereferencedSkillFile copies the root SKILL.md symlink as a regular file
// holding its target's content. The link is read through the pinned source
// handle. A target inside the skill is opened through that handle too; any
// other target is the user's own declared manifest and can only be opened by
// path, so whatever sits there now need not be what was validated (a target
// swapped for a link to a private key, say). The open handle must therefore
// be the very file validation read (pinnedManifest), and it must be a regular
// file within the manifest size cap; everything is checked and copied on that
// one handle, so nothing swapped in later is read.
func copyDereferencedSkillFile(srcRoot *os.Root, realSrc string, stage *os.Root, sourcePath string, pinnedManifest fs.FileInfo) (err error) {
	if pinnedManifest == nil {
		return fmt.Errorf("copy %s: the validated manifest's identity is required", sourcePath)
	}
	target, err := srcRoot.Readlink(skillFileName)
	if err != nil {
		return fmt.Errorf("read link %s: %w", sourcePath, err)
	}
	var in *os.File
	if LinkStaysInside(skillFileName, target) {
		in, err = srcRoot.OpenFile(skillFileName, readOnlyNoBlockFlags, 0)
	} else {
		if !filepath.IsAbs(target) {
			target = filepath.Join(realSrc, target)
		}
		in, err = os.OpenFile(target, readOnlyNoBlockFlags, 0)
	}
	if err != nil {
		return fmt.Errorf("open %s (the target of %s): %w", target, sourcePath, err)
	}
	defer func() {
		if closeErr := in.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %s: %w", target, closeErr))
		}
	}()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("inspect %s: %w", target, err)
	}
	// An inode number can be reused once a file is deleted, so the size and
	// modification time validation saw must also be unchanged.
	if !os.SameFile(info, pinnedManifest) || info.Size() != pinnedManifest.Size() || !info.ModTime().Equal(pinnedManifest.ModTime()) {
		return fmt.Errorf("SKILL.md changed since it was validated: %s", sourcePath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s links to %s, which is not a regular file", sourcePath, target)
	}
	if info.Size() > skillmeta.MaxSkillFileBytes {
		return fmt.Errorf("%s links to %s, which exceeds the %d byte limit", sourcePath, target, skillmeta.MaxSkillFileBytes)
	}
	out, err := stage.OpenFile(skillFileName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, copiedFileMode(info.Mode()))
	if err != nil {
		return fmt.Errorf("copy %s: %w", sourcePath, err)
	}
	copied, copyErr := io.Copy(out, io.LimitReader(in, skillmeta.MaxSkillFileBytes+1))
	if joined := errors.Join(copyErr, out.Close()); joined != nil {
		return fmt.Errorf("copy %s: %w", sourcePath, joined)
	}
	// The file can grow after the size check; the copy must stay within the
	// cap the store's validation applied.
	if copied > skillmeta.MaxSkillFileBytes {
		return fmt.Errorf("%s links to %s, which exceeds the %d byte limit", sourcePath, target, skillmeta.MaxSkillFileBytes)
	}
	return nil
}

// copyFile copies one regular file. The source is opened without blocking and
// re-checked, so a file swapped for a FIFO after the walk is skipped rather
// than hanging the copy.
func copyFile(srcRoot, stage *os.Root, rel, sourcePath string, report *CopyReport) (err error) {
	in, err := srcRoot.OpenFile(rel, readOnlyNoBlockFlags, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", sourcePath, err)
	}
	defer func() {
		if closeErr := in.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close %s: %w", sourcePath, closeErr))
		}
	}()
	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("inspect %s: %w", sourcePath, err)
	}
	if !info.Mode().IsRegular() {
		report.skip(sourcePath, "no longer a regular file")
		return nil
	}
	out, err := stage.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, copiedFileMode(info.Mode()))
	if err != nil {
		return fmt.Errorf("copy %s: %w", sourcePath, err)
	}
	_, copyErr := io.Copy(out, in)
	if joined := errors.Join(copyErr, out.Close()); joined != nil {
		return fmt.Errorf("copy %s: %w", sourcePath, joined)
	}
	return nil
}
