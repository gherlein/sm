//go:build unix

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

const (
	privateDirMode = 0o700
	// groupOtherBits are the permission bits that let anyone but the owner
	// list or enter a directory.
	groupOtherBits = 0o077
)

// tightenStateDir makes the existing directory holding statePath private
// (0700) when it belongs to the current user and is group or world
// accessible. Earlier releases created it 0755, and it reveals every
// directory sm manages and the store paths behind them. A missing directory
// is left for state.Lock to create privately; a symlinked one, or one owned by
// someone else, is not sm's to change.
func tightenStateDir(statePath string) error {
	dir := filepath.Dir(statePath)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect state dir %s: %w", dir, err)
	}
	if !info.IsDir() || info.Mode().Perm()&groupOtherBits == 0 {
		return nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("inspect state dir %s: no ownership information", dir)
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return nil
	}
	if err := os.Chmod(dir, privateDirMode); err != nil {
		return fmt.Errorf("make state dir %s private: %w", dir, err)
	}
	return nil
}
