// Package store enumerates conforming skill directories in the canonical,
// agent-neutral skills store.
package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gherlein/sm/internal/pathutil"
	"github.com/gherlein/sm/internal/skillmeta"
)

const (
	skillFileName      = "SKILL.md"
	storeDirectoryName = "skills"

	// worldWritable marks a store any user could plant or swap skills in.
	worldWritable fs.FileMode = 0o002
)

// Skill is a conforming skill directory in the store.
type Skill struct {
	// Name is the directory name, which is authoritative.
	Name string
	// Dir is the path to the skill directory.
	Dir string
	// Identity is the os.Stat of Dir (its real directory) taken just before
	// the skill was validated. A copy is made only from that very directory,
	// so an entry swapped for a link elsewhere during or after validation is
	// refused.
	Identity fs.FileInfo
	// ManifestIdentity is the os.FileInfo of the SKILL.md file validation
	// actually read (the symlink's target when SKILL.md is a link). sm --copy
	// copies a symlinked manifest by content and requires that same file, so
	// a target swapped after validation is never copied in its place.
	ManifestIdentity fs.FileInfo
}

// DefaultPath returns $XDG_DATA_HOME/skills, with the data home resolved by
// pathutil.DataHome (an empty or relative XDG_DATA_HOME is ignored).
func DefaultPath() (string, error) {
	dataHome, err := pathutil.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, storeDirectoryName), nil
}

// Listing is the result of enumerating a store.
type Listing struct {
	// Skills are the conforming skills sorted by name.
	Skills []Skill
	// Warnings describe entries that were skipped.
	Warnings []string
	// Unusable is the sorted set of entry names that exist but are
	// non-conforming or unreadable; callers must not treat them as absent.
	Unusable []string
}

// Enumerate returns the conforming immediate subdirectories of storeDir sorted
// by name. Non-conforming subdirectories yield warnings, not errors; the error
// is non-nil only when storeDir itself cannot be read. Entries beginning with
// "." (such as .git) are ignored silently.
func Enumerate(storeDir string) (Listing, error) {
	entries, err := os.ReadDir(storeDir)
	if err != nil {
		return Listing{}, fmt.Errorf("read skills store: %w", err)
	}

	var listing Listing
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		// Symlinked skill directories are resolved so a linked skill counts.
		skillDir := filepath.Join(storeDir, entry.Name())
		info, err := os.Stat(skillDir)
		if err != nil {
			listing.Warnings = append(listing.Warnings, pathutil.Printable(fmt.Sprintf("skipping %q: %v", entry.Name(), err)))
			listing.Unusable = append(listing.Unusable, entry.Name())
			continue
		}
		if !info.IsDir() {
			continue
		}

		manifest, problem := checkSkill(entry.Name(), skillDir)
		if problem != "" {
			listing.Warnings = append(listing.Warnings, pathutil.Printable(problem))
			listing.Unusable = append(listing.Unusable, entry.Name())
			continue
		}
		listing.Skills = append(listing.Skills, Skill{Name: entry.Name(), Dir: skillDir, Identity: info, ManifestIdentity: manifest})
	}

	sort.Slice(listing.Skills, func(first, second int) bool { return listing.Skills[first].Name < listing.Skills[second].Name })
	sort.Strings(listing.Unusable)
	return listing, nil
}

// RequireNotWorldWritable refuses a store directory (a symlinked one is judged
// by its target) that any user can add entries to: such a user could plant a
// skill, or swap one for a link to another directory, and sm would fan it out
// to every agent. A group-writable store and a store owned by someone else are
// left to the user's judgment.
func RequireNotWorldWritable(storeDir string) error {
	info, err := os.Stat(storeDir)
	if err != nil {
		return fmt.Errorf("inspect store %s: %w", storeDir, err)
	}
	if info.Mode().Perm()&worldWritable != 0 {
		return fmt.Errorf("store %s is world-writable; refusing", storeDir)
	}
	return nil
}

// checkSkill returns the identity of the manifest it read and an empty
// string when the directory conforms, otherwise a human-readable warning.
func checkSkill(directoryName, skillDir string) (fs.FileInfo, string) {
	if !skillmeta.ValidName(directoryName) {
		return nil, fmt.Sprintf("skipping %q: illegal skill name (must match %s)", directoryName, skillmeta.NamePattern)
	}
	declaredName, manifest, err := skillmeta.ReadNameInfo(filepath.Join(skillDir, skillFileName))
	if err != nil {
		return nil, fmt.Sprintf("skipping %q: %v", directoryName, err)
	}
	if declaredName != directoryName {
		return nil, fmt.Sprintf("skipping %q: name mismatch, %s declares %q", directoryName, skillFileName, declaredName)
	}
	return manifest, ""
}
