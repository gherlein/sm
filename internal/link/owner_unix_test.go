//go:build unix

package link

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// inodeOf returns the inode of path without following a final symlink.
func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no inode for %s", path)
	}
	return stat.Ino
}

// A real foreign-owned copy cannot be built without root, so these tests
// substitute the ownership decision for one inode and prove IsOwnedCopy
// consults it for both the directory and its marker.
func TestIsOwnedCopy_RequiresCurrentUserOwnership(t *testing.T) {
	for _, foreign := range []string{"directory", "marker"} {
		t.Run(foreign, func(t *testing.T) {
			root := t.TempDir()
			store := makeStoreSkill(t, root, "alpha")
			agentDir := filepath.Join(root, "agent")
			if _, err := testCreateCopy(t, agentDir, "alpha", store); err != nil {
				t.Fatal(err)
			}
			foreignPath := filepath.Join(agentDir, "alpha")
			if foreign == "marker" {
				foreignPath = filepath.Join(foreignPath, copyMarker)
			}
			foreignInode := inodeOf(t, foreignPath)
			replaceSeam(t, &copyOwnerCheck, func(info fs.FileInfo) (bool, error) {
				stat, ok := info.Sys().(*syscall.Stat_t)
				if !ok {
					t.Fatalf("no inode for %s", info.Name())
				}
				return stat.Ino != foreignInode, nil
			})

			owned, err := IsOwnedCopy(agentDir, "alpha", store)
			if err != nil {
				t.Fatalf("IsOwnedCopy: %v", err)
			}
			if owned {
				t.Fatalf("a copy whose %s belongs to another user must not be owned", foreign)
			}
			removed, _, err := RemoveCopy(agentDir, "alpha", store)
			if err != nil || removed {
				t.Fatalf("RemoveCopy = %v, %v; want left alone", removed, err)
			}
			if _, err := os.Stat(filepath.Join(agentDir, "alpha", "SKILL.md")); err != nil {
				t.Fatalf("foreign copy touched: %v", err)
			}
		})
	}
}
