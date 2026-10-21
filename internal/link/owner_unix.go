//go:build unix

package link

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// readOnlyNoBlockFlags opens a file for reading without ever blocking: if a
// regular file is swapped for a FIFO between inspection and open, the open
// returns immediately and the caller's type check rejects it.
const readOnlyNoBlockFlags = os.O_RDONLY | syscall.O_NONBLOCK

// ownedByCurrentUser reports whether info belongs to the effective uid. Only a
// directory sm itself created can be trusted as a write target or swept.
func ownedByCurrentUser(info fs.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("no ownership information for %s", info.Name())
	}
	return stat.Uid == uint32(os.Geteuid()), nil
}
