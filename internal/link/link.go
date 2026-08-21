// Package link realizes an install plan as whole-directory symlinks, touching
// only links sm created.
package link

import (
	"os"
	"path/filepath"

	"github.com/gherlein/skills-mapper/internal/resolve"
)

func Apply(targetDir string, add []resolve.Link, remove []string, prev map[string]string) (map[string]string, []string, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, nil, err
	}
	installed := map[string]string{}
	for k, v := range prev {
		installed[k] = v
	}
	for _, name := range remove {
		p := filepath.Join(targetDir, name)
		if isSymlink(p) {
			if err := os.Remove(p); err != nil {
				return nil, nil, err
			}
		}
		delete(installed, name)
	}
	var conflicts []string
	for _, l := range add {
		p := filepath.Join(targetDir, l.Name)
		fi, err := os.Lstat(p)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink == 0:
			conflicts = append(conflicts, l.Name) // protect unmanaged real entry
			continue
		case err == nil:
			if err := os.Remove(p); err != nil {
				return nil, nil, err
			}
		}
		if err := os.Symlink(l.SourceDir, p); err != nil {
			return nil, nil, err
		}
		installed[l.Name] = l.SourceDir
	}
	return installed, conflicts, nil
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
