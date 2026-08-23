// Package link realizes an install plan as whole-directory symlinks, touching
// only links sm created.
package link

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/brightsign-playground/sm/internal/resolve"
)

// Apply realizes an install plan as whole-directory symlinks, updating the
// installed map to reflect the current state. It guarantees that only
// symlinks created by sm are modified or removed; real files and directories
// are protected and never overwritten. On any error after installed is
// initialized, returns the partially-updated installed map to allow callers
// to persist real progress.
func Apply(targetDir string, add []resolve.Link, remove []string, prev map[string]string) (map[string]string, []string, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, nil, err
	}
	installed := map[string]string{}
	for k, v := range prev {
		installed[k] = v
	}
	for _, name := range remove {
		// Skip unsafe names; do not touch disk, drop from installed.
		if !safeName(name) {
			delete(installed, name)
			continue
		}
		p := filepath.Join(targetDir, name)
		fi, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			// Unexpected error (permission denied, etc.) — return partial progress.
			return installed, nil, err
		}
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(p); err != nil {
				return installed, nil, err
			}
		}
		delete(installed, name)
	}
	var conflicts []string
	for _, l := range add {
		// Reject unsafe names as conflicts; skip creating anything.
		if !safeName(l.Name) {
			conflicts = append(conflicts, l.Name)
			continue
		}
		p := filepath.Join(targetDir, l.Name)
		fi, err := os.Lstat(p)
		switch {
		case err != nil && !os.IsNotExist(err):
			// Unexpected error (permission denied, etc.) — return partial progress.
			return installed, conflicts, err
		case err == nil && fi.Mode()&os.ModeSymlink == 0:
			// Real file/directory exists; protect it.
			conflicts = append(conflicts, l.Name)
			continue
		case err == nil:
			// Symlink exists; replace it.
			if err := os.Remove(p); err != nil {
				return installed, conflicts, err
			}
		}
		// Create the symlink (either no entry existed, or we just removed one).
		if err := os.Symlink(l.SourceDir, p); err != nil {
			return installed, conflicts, err
		}
		installed[l.Name] = l.SourceDir
	}
	return installed, conflicts, nil
}

// safeName reports whether name is safe to use as a link target: non-empty,
// not "." or "..", and containing no path separators.
func safeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/"+string(filepath.Separator))
}
