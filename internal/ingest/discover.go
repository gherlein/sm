// Package ingest discovers skills in a proprietary agent's skills tree so
// they can be normalized into the canonical store.
package ingest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/skillmeta"
)

const (
	// FormatClaudeCode is the only source format currently supported.
	FormatClaudeCode = "claude-code"
	skillFileName    = "SKILL.md"
	// pluginsDirectoryName marks third-party skills: Claude Code installs
	// plugin content beneath a directory with this name.
	pluginsDirectoryName = "plugins"

	// leavingSkillFileMessage explains why a skill whose SKILL.md links out of
	// it is not ingested.
	leavingSkillFileMessage = "SKILL.md is a symlink that leaves the skill; not ingested"
)

// prunedDirectoryNames are never descended: they hold third-party or
// generated content whose SKILL.md files are not the user's skills.
var prunedDirectoryNames = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"dist":         true,
	"build":        true,
}

// SourceSkill is a conforming skill found in a source tree.
type SourceSkill struct {
	// Name is the validated frontmatter name, used as the target name.
	Name string
	// Dir is the absolute, symlink-free path of the source skill directory.
	Dir string
	// FromPlugin is true when the path the skill was reached by, or its real
	// path, has a component named "plugins" (relative to the source root when
	// inside it), so a link can never pass a plugin skill off as personal.
	FromPlugin bool
	// ModTime is the modification time of the skill's SKILL.md.
	ModTime time.Time
	// Identity is the os.Stat of Dir taken just before the skill was
	// validated; the copy is made only from that very directory (see
	// link.CopyDir).
	Identity fs.FileInfo
}

// DiscoverOptions tunes Discover.
type DiscoverOptions struct {
	// FollowSymlinks descends symlinked directories inside the source tree,
	// each real directory at most once.
	FollowSymlinks bool
}

// DiscoverResult is the outcome of Discover.
type DiscoverResult struct {
	// Skills holds the winning skill per name, sorted by Name.
	Skills []SourceSkill
	// Warnings has one line per skipped entry.
	Warnings []string
	// Skipped counts every entry that was not ingested: invalid skills,
	// duplicates, unreadable directories and skipped or refused symlinks. A
	// directory re-reached through a link is not counted: nothing is lost.
	Skipped int
}

type walker struct {
	options DiscoverOptions
	root    string
	// visited maps each real directory already entered to the path it was
	// reached by, so symlinks cannot cause loops or ingest a skill twice.
	visited    map[string]string
	candidates []SourceSkill
	result     DiscoverResult
}

// Discover recursively finds every conforming skill under sourceDir. The
// root itself is symlink-resolved and must be a readable directory; problems
// below the root become warnings counted in Skipped, never errors. Duplicate
// names are resolved by priority: personal skills before plugin skills, then
// the newest SKILL.md among plugin copies, then real path order.
func Discover(sourceDir, format string, options DiscoverOptions) (DiscoverResult, error) {
	if format != FormatClaudeCode {
		return DiscoverResult{}, fmt.Errorf("unsupported source format %q", format)
	}
	absoluteRoot, err := filepath.Abs(sourceDir)
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("cannot resolve %s: %w", sourceDir, err)
	}
	// A symlinked root (common for ~/.claude) is resolved up front: a walk
	// that Lstats the root would never descend it and silently find nothing.
	root, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("cannot resolve source %s: %w", sourceDir, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("cannot read source %s: %w", sourceDir, err)
	}
	if !info.IsDir() {
		return DiscoverResult{}, fmt.Errorf("source %s is not a directory", sourceDir)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("cannot read source %s: %w", sourceDir, err)
	}

	walk := &walker{options: options, root: root, visited: map[string]string{root: root}}
	walk.visitEntries(root, root, entries)
	walk.resolveDuplicates()
	return walk.result, nil
}

// skip records a warning and counts the entry as skipped. Paths and names in
// the tree may be third-party content, so every warning is made terminal-safe.
func (walk *walker) skip(format string, arguments ...any) {
	walk.warn(format, arguments...)
	walk.result.Skipped++
}

func (walk *walker) warn(format string, arguments ...any) {
	walk.result.Warnings = append(walk.result.Warnings, pathutil.Printable(fmt.Sprintf(format, arguments...)))
}

// visitDirectory enters the directory whose real path is realPath; logicalPath
// is the path it was reached by, used for messages and plugin classification.
func (walk *walker) visitDirectory(realPath, logicalPath string) {
	if previous, seen := walk.visited[realPath]; seen {
		walk.warn("%s is the same directory as %s; ingesting it once", logicalPath, previous)
		return
	}
	walk.visited[realPath] = logicalPath
	entries, err := os.ReadDir(realPath)
	if err != nil {
		walk.skip("skipping %s: %v", logicalPath, err)
		return
	}
	walk.visitEntries(realPath, logicalPath, entries)
}

func (walk *walker) visitEntries(realPath, logicalPath string, entries []fs.DirEntry) {
	hasSkillFile := slices.ContainsFunc(entries, func(entry fs.DirEntry) bool { return entry.Name() == skillFileName })
	// A skill's bundled content is not a set of separate skills.
	if hasSkillFile && walk.considerSkill(realPath, logicalPath) {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if prunedDirectoryNames[name] {
			continue
		}
		childReal := filepath.Join(realPath, name)
		childLogical := filepath.Join(logicalPath, name)
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			walk.visitSymlink(childReal, childLogical)
		case entry.IsDir():
			walk.visitDirectory(childReal, childLogical)
		}
	}
}

// visitSymlink handles a symlink entry. Links to anything but a directory
// cannot hold skills and are ignored.
func (walk *walker) visitSymlink(linkPath, logicalPath string) {
	info, err := os.Stat(linkPath)
	if err != nil {
		walk.skip("skipping symlink %s: %v", logicalPath, err)
		return
	}
	if !info.IsDir() {
		return
	}
	if !walk.options.FollowSymlinks {
		walk.skip("skipping symlink %s (use --follow-symlinks)", logicalPath)
		return
	}
	target, err := filepath.EvalSymlinks(linkPath)
	if err != nil {
		walk.skip("skipping symlink %s: %v", logicalPath, err)
		return
	}
	// Following a link to an ancestor (such as ~) would walk everything
	// beneath it. The root itself is harmless: the visited set stops it.
	if target != walk.root && pathutil.Within(target, walk.root) {
		walk.skip("refusing to follow %s: it points to an ancestor of the source (%s)", logicalPath, target)
		return
	}
	walk.visitDirectory(target, logicalPath)
}

// considerSkill evaluates the SKILL.md in a directory and reports whether the
// directory is a skill (valid or not), which ends descent. A SKILL.md that is
// itself a directory does not make a skill.
func (walk *walker) considerSkill(realPath, logicalPath string) bool {
	// The identity is taken before anything is validated, so whatever the
	// checks below read, a later copy is made only from this very directory:
	// a directory swapped in during or after validation is refused at copy
	// time instead of being pinned as if it had been validated.
	identity, err := os.Stat(realPath)
	if err != nil {
		walk.skip("skipping %s: %v", logicalPath, err)
		return true
	}
	name, manifest, notSkill, err := readManifest(realPath, identity)
	if notSkill {
		return false
	}
	if errors.Is(err, errManifestLeavesSkill) || errors.Is(err, errManifestUnresolvable) {
		walk.skip("skipping %s: %v", logicalPath, err)
		return true
	}
	if err != nil {
		walk.skip("skipping %s: %v", logicalPath, err)
		return true
	}
	if !skillmeta.ValidName(name) {
		walk.skip("skipping %s: invalid skill name %q", logicalPath, name)
		return true
	}
	walk.candidates = append(walk.candidates, SourceSkill{
		Name: name,
		Dir:  realPath,
		// Which spelling reaches a directory first depends on sibling order,
		// so either path naming plugins marks the skill as plugin: the trust
		// notice must never under-report plugin origin.
		FromPlugin: walk.underPlugins(logicalPath) || walk.underPlugins(realPath),
		ModTime:    manifest.ModTime(),
		Identity:   identity,
	})
	return true
}

// errManifestLeavesSkill marks a SKILL.md the copy would not reproduce: it is
// reached through a link the copy drops (absolute, climbing out of the skill,
// or using ".." after a name) or through a reserved root name.
var errManifestLeavesSkill = errors.New(leavingSkillFileMessage)

// errManifestUnresolvable marks a SKILL.md whose links cannot be resolved at
// all: a loop, or a path through something that is not a directory.
var errManifestUnresolvable = errors.New("SKILL.md cannot be resolved inside the skill")

// maxManifestHops bounds the links followed to reach a manifest. It equals
// os.Root's own limit (rootMaxSymlinks, 8 in Go 1.26): the copy verifies the
// written SKILL.md through an os.Root, so discovery must never accept a
// longer chain that the real run would then refuse.
const maxManifestHops = 8

// readManifest reads the name from realPath's SKILL.md exactly as the copy
// will reproduce it. The copy keeps a link only when link.LinkStaysInside
// accepts it, never follows one, and skips reserved root names; a manifest
// reached any other way would arrive in the store without its manifest. So
// the path is resolved here one component and one link at a time through an
// os.Root on the skill, applying that same predicate to every link reached
// (on the final name and on every directory on the way), and only then is
// the final regular file read through the root. A manifest the copy would
// drop yields errManifestLeavesSkill, an unresolvable one
// errManifestUnresolvable, so a dry run refuses exactly what the real run
// would fail on. notSkill is true when SKILL.md resolves to a directory.
// identity must still describe the opened directory.
func readManifest(realPath string, identity fs.FileInfo) (name string, manifest fs.FileInfo, notSkill bool, err error) {
	skillRoot, err := os.OpenRoot(realPath)
	if err != nil {
		return "", nil, false, fmt.Errorf("cannot read %s: %w", skillFileName, err)
	}
	defer func() {
		if closeErr := skillRoot.Close(); closeErr != nil {
			name, manifest, notSkill, err = "", nil, false, errors.Join(err, fmt.Errorf("close %s: %w", realPath, closeErr))
		}
	}()
	opened, err := skillRoot.Stat(".")
	if err != nil {
		return "", nil, false, fmt.Errorf("cannot read %s: %w", realPath, err)
	}
	if !os.SameFile(opened, identity) {
		return "", nil, false, fmt.Errorf("%s changed during discovery", realPath)
	}
	resolved, target, err := resolveManifest(skillRoot)
	if err != nil {
		return "", nil, false, err
	}
	if target.IsDir() {
		return "", nil, true, nil
	}
	file, err := skillRoot.OpenFile(resolved, skillmeta.ReadOnlyNonBlocking, 0)
	if err != nil {
		return "", nil, false, fmt.Errorf("cannot read %s: %w", skillFileName, err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			name, manifest, notSkill, err = "", nil, false, errors.Join(err, fmt.Errorf("close %s: %w", skillFileName, closeErr))
		}
	}()
	name, manifest, err = skillmeta.ReadNameFromFile(file, filepath.Join(realPath, skillFileName))
	return name, manifest, false, err
}

// resolveManifest resolves SKILL.md inside skillRoot the way the copy's tree
// will resolve it, returning the link-free path of the final entry and its
// Lstat. Every component is inspected with Lstat; a link is followed only if
// the copy would keep it (link.LinkStaysInside, judged at the link's own path
// in the skill), and a ".." can only step back over a real directory already
// entered, which is what the kernel does for a path without links.
func resolveManifest(skillRoot *os.Root) (string, fs.FileInfo, error) {
	pending := splitComponents(skillFileName)
	var entered []string
	hops := 0
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			if len(entered) == 0 {
				return "", nil, errManifestLeavesSkill
			}
			entered = entered[:len(entered)-1]
			continue
		}
		candidate := filepath.Join(append(append([]string{}, entered...), part)...)
		if len(entered) == 0 && link.IsReservedRootName(part) {
			return "", nil, errManifestLeavesSkill
		}
		info, err := skillRoot.Lstat(candidate)
		if err != nil {
			return "", nil, fmt.Errorf("cannot read %s: %w", skillFileName, err)
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			hops++
			if hops > maxManifestHops {
				return "", nil, errManifestUnresolvable
			}
			target, err := skillRoot.Readlink(candidate)
			if err != nil {
				return "", nil, fmt.Errorf("cannot read %s: %w", skillFileName, err)
			}
			if !link.LinkStaysInside(candidate, target) {
				return "", nil, errManifestLeavesSkill
			}
			// The target is relative to the link's directory, which is the
			// directory already entered.
			pending = append(splitComponents(target), pending...)
			continue
		}
		if len(pending) > 0 {
			if !info.IsDir() {
				return "", nil, errManifestUnresolvable
			}
			entered = append(entered, part)
			continue
		}
		return candidate, info, nil
	}
	// Only components that cancel out remain: the chain ends at the skill
	// directory itself, which is not a manifest.
	return "", nil, errManifestUnresolvable
}

// splitComponents splits a slash- or separator-delimited relative path.
func splitComponents(path string) []string {
	return strings.Split(filepath.ToSlash(path), "/")
}

// underPlugins reports whether path has a component named plugins, judged
// relative to the root when path is inside it (the root's own ancestors are
// not the tree's layout) and in full otherwise.
func (walk *walker) underPlugins(path string) bool {
	judged := path
	if pathutil.Within(walk.root, path) {
		relative, err := filepath.Rel(walk.root, path)
		// Within already proved path lies under root, so Rel cannot fail.
		if err != nil {
			panic(fmt.Sprintf("path %s is not relative to %s: %v", path, walk.root, err))
		}
		judged = relative
	}
	return slices.Contains(pathComponents(judged), pluginsDirectoryName)
}

func pathComponents(path string) []string {
	return strings.Split(path, string(filepath.Separator))
}

// resolveDuplicates keeps the highest-priority candidate per name. Priority
// is applied only after the whole tree is walked because walk order is
// alphabetical, which would let plugins/ beat skills/ and 1.10 beat 1.9.
func (walk *walker) resolveDuplicates() {
	byName := make(map[string][]SourceSkill)
	for _, found := range walk.candidates {
		byName[found.Name] = append(byName[found.Name], found)
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		group := byName[name]
		sort.SliceStable(group, func(left, right int) bool { return outranks(group[left], group[right]) })
		winner := group[0]
		walk.result.Skills = append(walk.result.Skills, winner)
		for _, loser := range group[1:] {
			walk.skip("skipping %s: duplicate skill name %q (keeping %s)", loser.Dir, name, winner.Dir)
		}
	}
}

func outranks(left, right SourceSkill) bool {
	if left.FromPlugin != right.FromPlugin {
		return !left.FromPlugin
	}
	// Plugin caches keep several versions side by side; the newest is the
	// one the agent actually uses.
	if left.FromPlugin && !left.ModTime.Equal(right.ModTime) {
		return left.ModTime.After(right.ModTime)
	}
	// Real paths, compared component by component like the walk, keep the
	// result independent of which link spelling reached a skill first.
	return slices.Compare(pathComponents(left.Dir), pathComponents(right.Dir)) < 0
}
